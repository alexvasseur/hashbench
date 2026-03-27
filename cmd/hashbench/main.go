package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"hashbench/internal/config"
	"hashbench/internal/metrics"
	"hashbench/internal/workload"
)

func main() {
	cfg := config.Config{}
	flag.StringVar(&cfg.Addr, "addr", config.DefaultAddr, "Redis address")
	flag.StringVar(&cfg.Password, "password", "", "Redis password")
	flag.IntVar(&cfg.DB, "db", 0, "Redis DB index")
	flag.BoolVar(&cfg.TLS, "tls", false, "Enable TLS")
	flag.BoolVar(&cfg.Cluster, "cluster", false, "Enable Redis Cluster mode")
	flag.IntVar(&cfg.Threads, "threads", config.DefaultThreads, "Number of worker threads")
	flag.IntVar(&cfg.Clients, "client", config.DefaultClients, "Redis connection pool size")
	flag.IntVar(&cfg.Keys, "keys", config.DefaultKeys, "Number of keys")
	flag.IntVar(&cfg.ValueBytes, "value-bytes", config.DefaultValueBytes, "Value size in bytes")
	flag.BoolVar(&cfg.LoadOnly, "load", false, "Load data only (writes only)")
	flag.StringVar(&cfg.RunRatio, "run", "1:1", "Run mode write:read ratio, e.g. 3:7 (uses HGETALL)")
	flag.Uint64Var(&cfg.Requests, "requests", config.DefaultRequests, "Total requests to execute (0 = disabled)")
	flag.IntVar(&cfg.QPS, "qps", config.DefaultQPS, "Global ops/sec limit (divided per client, 0 = unlimited)")
	flag.IntVar(&cfg.Pipeline, "pipeline", config.DefaultPipeline, "Pipeline depth")
	flag.Int64Var(&cfg.Seed, "seed", config.DefaultSeed, "Random seed (0 = time-based)")
	flag.DurationVar(&cfg.ReportInterval, "report-interval", config.DefaultReportInterval, "Reporting interval")
	flag.StringVar(&cfg.KeyPattern, "key-pattern", config.DefaultKeyPattern, "Key pattern: random or sequential")
	flag.BoolVar(&cfg.JSON, "json", false, "Output JSON summary")
	flag.Parse()

	if cfg.Seed == 0 {
		cfg.Seed = time.Now().UnixNano()
	}

	if cfg.LoadOnly && hasFlag("run") {
		fmt.Fprintln(os.Stderr, "config error: --load and --run are mutually exclusive")
		os.Exit(1)
	}

	runMode := false
	loadOnce := false
	writeRatio := 0.5
	if cfg.RunRatio != "" {
		ratio, err := parseRunRatio(cfg.RunRatio)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
		writeRatio = ratio
		runMode = true
	}
	if cfg.LoadOnly {
		writeRatio = 1.0
		loadOnce = true
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Redis Hash Bench\n")
	totalWorkers := cfg.Threads * cfg.Clients
	fmt.Printf("addr=%s db=%d tls=%v threads=%d client=%d total-workers=%d keys=%d\n", cfg.Addr, cfg.DB, cfg.TLS, cfg.Threads, cfg.Clients, totalWorkers, cfg.Keys)
	fmt.Printf("value-bytes=%d write-ratio=%.2f load=%v run=%v\n", cfg.ValueBytes, writeRatio, cfg.LoadOnly, runMode)
	fmt.Printf("requests=%d qps=%d pipeline=%d seed=%d key-pattern=%s\n", cfg.Requests, cfg.QPS, cfg.Pipeline, cfg.Seed, cfg.KeyPattern)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clients := make([]redis.UniversalClient, 0, cfg.Clients)
	if cfg.Cluster {
		addrs := splitAddrs(cfg.Addr)
		for i := 0; i < cfg.Clients; i++ {
			opt := &redis.ClusterOptions{
				Addrs:        addrs,
				Password:     cfg.Password,
				PoolSize:     cfg.Threads,
				MinIdleConns: cfg.Threads,
				PoolTimeout:  5 * time.Second,
				DialTimeout:  5 * time.Second,
				ReadTimeout:  5 * time.Second,
				WriteTimeout: 5 * time.Second,
			}
			if cfg.TLS {
				opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			clients = append(clients, redis.NewClusterClient(opt))
		}
	} else {
		for i := 0; i < cfg.Clients; i++ {
			opt := &redis.Options{
				Addr:         cfg.Addr,
				Password:     cfg.Password,
				DB:           cfg.DB,
				PoolSize:     cfg.Threads,
				MinIdleConns: cfg.Threads,
				PoolTimeout:  5 * time.Second,
				DialTimeout:  5 * time.Second,
				ReadTimeout:  5 * time.Second,
				WriteTimeout: 5 * time.Second,
			}
			if cfg.TLS {
				opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			clients = append(clients, redis.NewClient(opt))
		}
	}
	defer func() {
		for _, c := range clients {
			_ = c.Close()
		}
	}()

	m := metrics.New(200000, 65536)
	start := time.Now()

	if cfg.ReportInterval > 0 {
		go m.RunReporter(ctx, cfg.ReportInterval, func(total, read, write, errors uint64, opsSec, readOpsSec, writeOpsSec, errSec, avgMs, p95Ms, p99Ms float64, errorsMsg []string) {
			uptime := int(time.Since(start).Seconds())
			opsInt := int64(math.Round(opsSec))
			readInt := int64(math.Round(readOpsSec))
			writeInt := int64(math.Round(writeOpsSec))
			errsInt := int64(math.Round(errSec))
			fmt.Printf("[%03d s]\t%8d ops/s\t%8d read/s\t%8d write/s\t%6d errors/s\t%7.2f ms avg\t%7.2f ms p95\t%7.2f ms p99\n", uptime, opsInt, readInt, writeInt, errsInt, avgMs, p95Ms, p99Ms)
			for _, msg := range errorsMsg {
				fmt.Fprintf(os.Stderr, "[%03d s] %s\n", uptime, msg)
			}
		})
	}

	var completed uint64
	var cancelOnce sync.Once
	var nextLoadIdx uint64
	var loadDone uint32

	var perClientQPS float64
	if cfg.QPS > 0 {
		perClientQPS = float64(cfg.QPS) / float64(cfg.Clients)
	}
	nextSlots := make([]int64, cfg.Clients)
	nowNs := time.Now().UnixNano()
	for i := 0; i < cfg.Clients; i++ {
		nextSlots[i] = nowNs
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(totalWorkers)
	for i := 0; i < totalWorkers; i++ {
		workerID := i
		clientIdx := workerID % cfg.Clients
		client := clients[clientIdx]
		go func() {
			defer wg.Done()
			rng := workload.NewRNG(cfg.Seed, workerID)
			gen := workload.NewGenerator(cfg.Keys, cfg.ValueBytes, writeRatio, cfg.KeyPattern, rng)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				delta := uint64(1)
				if cfg.Pipeline <= 1 {
					if !pace(ctx, perClientQPS, &nextSlots[clientIdx]) {
						return
					}
					op := gen.NextOpType()
					if cfg.LoadOnly {
						op = workload.OpWrite
					}
					n := gen.NextFieldCount()
					var key string
					if loadOnce {
						idx := int(atomic.AddUint64(&nextLoadIdx, 1) - 1)
						if idx >= cfg.Keys {
							if atomic.CompareAndSwapUint32(&loadDone, 0, 1) {
								uptime := int(time.Since(start).Seconds())
								fmt.Printf("[%03d s] load complete: keys=%d\n", uptime, cfg.Keys)
							}
							cancelOnce.Do(cancel)
							continue
						}
						n = workload.FieldCountForIndex(idx)
						key = workload.KeyForIndex(n, idx)
					} else {
						idx := gen.NextIndex()
						n = workload.FieldCountForIndex(idx)
						key = workload.KeyForIndex(n, idx)
					}
					var err error
					switch op {
					case workload.OpWrite:
						args := gen.HSetArgs(n)
						startOp := time.Now()
						err = client.HSet(ctx, key, args...).Err()
						m.Record(metrics.OpWrite, time.Since(startOp), err)
					case workload.OpRead:
						if runMode {
							startOp := time.Now()
							_, err = client.HGetAll(ctx, key).Result()
							m.Record(metrics.OpRead, time.Since(startOp), err)
						} else {
							fields := gen.Fields(n)
							startOp := time.Now()
							res, rerr := client.HMGet(ctx, key, fields...).Result()
							if rerr != nil {
								err = rerr
							} else if len(res) != n {
								err = fmt.Errorf("hmget result len %d != %d", len(res), n)
							}
							m.Record(metrics.OpRead, time.Since(startOp), err)
						}
					}
				} else {
					depth := cfg.Pipeline
					opTypes := make([]workload.OpType, depth)
					keys := make([]string, depth)
					counts := make([]int, depth)
					fields := make([][]string, depth)
					args := make([][]interface{}, depth)
					actual := 0
					for j := 0; j < depth; j++ {
						if !pace(ctx, perClientQPS, &nextSlots[clientIdx]) {
							return
						}
						opTypes[j] = gen.NextOpType()
						if cfg.LoadOnly {
							opTypes[j] = workload.OpWrite
						}
						counts[j] = gen.NextFieldCount()
						if loadOnce {
							idx := int(atomic.AddUint64(&nextLoadIdx, 1) - 1)
							if idx >= cfg.Keys {
								if atomic.CompareAndSwapUint32(&loadDone, 0, 1) {
									uptime := int(time.Since(start).Seconds())
									fmt.Printf("[%03d s] load complete: keys=%d\n", uptime, cfg.Keys)
								}
								cancelOnce.Do(cancel)
								break
							}
							counts[j] = workload.FieldCountForIndex(idx)
							keys[j] = workload.KeyForIndex(counts[j], idx)
						} else {
							idx := gen.NextIndex()
							counts[j] = workload.FieldCountForIndex(idx)
							keys[j] = workload.KeyForIndex(counts[j], idx)
						}
						if opTypes[j] == workload.OpWrite {
							args[j] = gen.HSetArgs(counts[j])
						} else {
							if !runMode {
								fields[j] = gen.Fields(counts[j])
							}
						}
						actual++
					}
					if actual == 0 {
						continue
					}
					depth = actual
					delta = uint64(depth)
					pipe := client.Pipeline()
					for j := 0; j < depth; j++ {
						if opTypes[j] == workload.OpWrite {
							pipe.HSet(ctx, keys[j], args[j]...)
						} else {
							if runMode {
								pipe.HGetAll(ctx, keys[j])
							} else {
								pipe.HMGet(ctx, keys[j], fields[j]...)
							}
						}
					}
					startOp := time.Now()
					_, err := pipe.Exec(ctx)
					lat := time.Since(startOp)
					per := lat / time.Duration(depth)
					for j := 0; j < depth; j++ {
						if opTypes[j] == workload.OpWrite {
							m.Record(metrics.OpWrite, per, err)
						} else {
							m.Record(metrics.OpRead, per, err)
						}
					}
				}

				n := atomic.AddUint64(&completed, delta)
				if cfg.Requests > 0 && n >= cfg.Requests {
					cancelOnce.Do(cancel)
				}
			}
		}()
	}

	go func() {
		<-sigCh
		cancelOnce.Do(cancel)
	}()

	wg.Wait()
	elapsed := time.Since(start)
	m.Close()

	summary := m.FinalSummary(elapsed)

	fmt.Printf("\nSummary\n")
	fmt.Printf("elapsed=%s total=%d errors=%d error-rate=%.4f\n", elapsed, summary.Count, summary.Errors, summary.ErrorRate)
	fmt.Printf("throughput	%.0f ops/s			%.0f read/s			%.0f write/s\n", summary.OpsPerSec, summary.ReadOpsSec, summary.WriteOpsSec)
	fmt.Printf("overall		p50=%.3f 	%.3f p90	%.3f p95	%.3f p99	%.3f p99.9\n", summary.Overall.P50, summary.Overall.P90, summary.Overall.P95, summary.Overall.P99, summary.Overall.P999)
	fmt.Printf("read    	p50=%.3f 	%.3f p90	%.3f p95	%.3f p99	%.3f p99.9\n", summary.Read.P50, summary.Read.P90, summary.Read.P95, summary.Read.P99, summary.Read.P999)
	fmt.Printf("write   	p50=%.3f 	%.3f p90	%.3f p95	%.3f p99	%.3f p99.9\n", summary.Write.P50, summary.Write.P90, summary.Write.P95, summary.Write.P99, summary.Write.P999)
	if summary.LastError != "" {
		fmt.Printf("last_error=%s\n", summary.LastError)
	}

	if cfg.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(summary)
	}
}

func parseRunRatio(s string) (float64, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid --run format, expected W:R")
	}
	w, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid --run write ratio: %v", err)
	}
	r, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid --run read ratio: %v", err)
	}
	if w < 0 || r < 0 || (w+r) == 0 {
		return 0, fmt.Errorf("invalid --run ratio values")
	}
	return w / (w + r), nil
}

func hasFlag(name string) bool {
	prefix := "--" + name
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			return true
		}
	}
	return false
}

func splitAddrs(addr string) []string {
	parts := strings.Split(addr, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			addrs = append(addrs, v)
		}
	}
	if len(addrs) == 0 {
		return []string{addr}
	}
	return addrs
}

func pace(ctx context.Context, rate float64, next *int64) bool {
	if rate <= 0 {
		return true
	}
	interval := int64(float64(time.Second) / rate)
	if interval < int64(time.Microsecond) {
		interval = int64(time.Microsecond)
	}
	slot := atomic.AddInt64(next, interval)
	now := time.Now().UnixNano()
	if slot > now {
		timer := time.NewTimer(time.Duration(slot - now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return true
}

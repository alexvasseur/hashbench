package metrics

import (
	"context"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type OpType int

const (
	OpRead OpType = iota
	OpWrite
)

type Percentiles struct {
	P50  float64 `json:"p50_ms"`
	P90  float64 `json:"p90_ms"`
	P95  float64 `json:"p95_ms"`
	P99  float64 `json:"p99_ms"`
	P999 float64 `json:"p99_9_ms"`
}

type Summary struct {
	Count       uint64      `json:"count"`
	Errors      uint64      `json:"errors"`
	ErrorRate   float64     `json:"error_rate"`
	OpsPerSec   float64     `json:"ops_per_sec"`
	ReadOpsSec  float64     `json:"read_ops_per_sec"`
	WriteOpsSec float64     `json:"write_ops_per_sec"`
	Overall     Percentiles `json:"overall"`
	Read        Percentiles `json:"read"`
	Write       Percentiles `json:"write"`
	LastError   string      `json:"last_error,omitempty"`
}

type latencyEvent struct {
	op OpType
	d  time.Duration
}

type latencyCollector struct {
	cap     int
	seen    uint64
	samples []time.Duration
}

func newLatencyCollector(capacity int) *latencyCollector {
	return &latencyCollector{cap: capacity, samples: make([]time.Duration, 0, capacity)}
}

func (c *latencyCollector) add(d time.Duration) {
	c.seen++
	if len(c.samples) < c.cap {
		c.samples = append(c.samples, d)
		return
	}
	// Reservoir sampling
	r := randUint64(c.seen)
	if r < uint64(c.cap) {
		c.samples[r] = d
	}
}

// randUint64 returns a pseudo-random uint64 in [0, n).
func randUint64(n uint64) uint64 {
	// xorshift64* with global state is fine in single goroutine.
	// This avoids pulling in math/rand locks for the aggregator.
	if n == 0 {
		return 0
	}
	x := xorshift64()
	return x % n
}

var rngState uint64 = 88172645463393265

func xorshift64() uint64 {
	x := rngState
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	rngState = x
	return x
}

type Metrics struct {
	latencyCh chan latencyEvent
	wg        sync.WaitGroup

	totalOps uint64
	readOps  uint64
	writeOps uint64
	errors   uint64

	lastErr atomic.Value

	overall *latencyCollector
	read    *latencyCollector
	write   *latencyCollector
}

func New(sampleCap int, chBuffer int) *Metrics {
	if sampleCap <= 0 {
		sampleCap = 100000
	}
	if chBuffer <= 0 {
		chBuffer = 65536
	}
	m := &Metrics{
		latencyCh: make(chan latencyEvent, chBuffer),
		overall:   newLatencyCollector(sampleCap),
		read:      newLatencyCollector(sampleCap),
		write:     newLatencyCollector(sampleCap),
	}
	m.lastErr.Store("")
	m.wg.Add(1)
	go m.runAggregator()
	return m
}

func (m *Metrics) runAggregator() {
	defer m.wg.Done()
	for ev := range m.latencyCh {
		m.overall.add(ev.d)
		switch ev.op {
		case OpRead:
			m.read.add(ev.d)
		case OpWrite:
			m.write.add(ev.d)
		}
	}
}

func (m *Metrics) Close() {
	close(m.latencyCh)
	m.wg.Wait()
}

func (m *Metrics) Record(op OpType, latency time.Duration, err error) {
	atomic.AddUint64(&m.totalOps, 1)
	switch op {
	case OpRead:
		atomic.AddUint64(&m.readOps, 1)
	case OpWrite:
		atomic.AddUint64(&m.writeOps, 1)
	}
	if err != nil {
		atomic.AddUint64(&m.errors, 1)
		m.lastErr.Store(err.Error())
	}
	select {
	case m.latencyCh <- latencyEvent{op: op, d: latency}:
	default:
		// drop sample if channel is full
	}
}

func (m *Metrics) Snapshot() (total, read, write, errors uint64, lastErr string) {
	total = atomic.LoadUint64(&m.totalOps)
	read = atomic.LoadUint64(&m.readOps)
	write = atomic.LoadUint64(&m.writeOps)
	errors = atomic.LoadUint64(&m.errors)
	lastErr, _ = m.lastErr.Load().(string)
	return
}

func (m *Metrics) RunReporter(ctx context.Context, interval time.Duration, report func(total, read, write, errors uint64, opsSec, readOpsSec, writeOpsSec float64)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastTotal, lastRead, lastWrite, lastErrors uint64
	var lastTime = time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			total, read, write, errors, _ := m.Snapshot()
			now := time.Now()
			dt := now.Sub(lastTime).Seconds()
			if dt <= 0 {
				dt = interval.Seconds()
			}
			opsSec := float64(total-lastTotal) / dt
			readOpsSec := float64(read-lastRead) / dt
			writeOpsSec := float64(write-lastWrite) / dt
			report(total, read, write, errors, opsSec, readOpsSec, writeOpsSec)
			lastTotal = total
			lastRead = read
			lastWrite = write
			lastErrors = errors
			_ = lastErrors
			lastTime = now
		}
	}
}

func (m *Metrics) FinalSummary(elapsed time.Duration) Summary {
	total, read, write, errors, lastErr := m.Snapshot()
	opsPerSec := 0.0
	readOpsSec := 0.0
	writeOpsSec := 0.0
	if elapsed > 0 {
		opsPerSec = float64(total) / elapsed.Seconds()
		readOpsSec = float64(read) / elapsed.Seconds()
		writeOpsSec = float64(write) / elapsed.Seconds()
	}
	errRate := 0.0
	if total > 0 {
		errRate = float64(errors) / float64(total)
	}
	return Summary{
		Count:       total,
		Errors:      errors,
		ErrorRate:   errRate,
		OpsPerSec:   opsPerSec,
		ReadOpsSec:  readOpsSec,
		WriteOpsSec: writeOpsSec,
		Overall:     computePercentiles(m.overall.samples),
		Read:        computePercentiles(m.read.samples),
		Write:       computePercentiles(m.write.samples),
		LastError:   lastErr,
	}
}

func computePercentiles(samples []time.Duration) Percentiles {
	if len(samples) == 0 {
		return Percentiles{}
	}
	copySamples := make([]time.Duration, len(samples))
	copy(copySamples, samples)
	sort.Slice(copySamples, func(i, j int) bool { return copySamples[i] < copySamples[j] })
	get := func(p float64) float64 {
		if p <= 0 {
			return ms(copySamples[0])
		}
		if p >= 1 {
			return ms(copySamples[len(copySamples)-1])
		}
		n := float64(len(copySamples))
		idx := int(math.Ceil(p*n)) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(copySamples) {
			idx = len(copySamples) - 1
		}
		return ms(copySamples[idx])
	}
	return Percentiles{
		P50:  get(0.50),
		P90:  get(0.90),
		P95:  get(0.95),
		P99:  get(0.99),
		P999: get(0.999),
	}
}

func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

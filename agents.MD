# Agents Implementation Guide (for Codex / automated dev)

## Overview
Implement a Go CLI benchmark tool that exercises Redis Hash reads/writes with configurable:
- keys, threads (goroutines), connections (pool size)
- read/write ratio
- random field counts per operation (min..max)
- fixed-size values (default 16 bytes)

Use pure Go build with a Makefile that works on macOS and Linux.

## Recommended Stack
- Go >= 1.21
- Redis client: `github.com/redis/go-redis/v9` (preferred)
- CLI parsing: stdlib `flag` (keep simple, avoid cobra unless needed)
- Latency stats: implement lightweight reservoir/TDigest or fixed-bucket histogram + quantile from sorted samples.
  - For simplicity: collect per-op latencies into slices with capped size (reservoir sampling) to compute percentiles at end.
  - Also compute rolling ops/sec via atomic counters.

## Repo Layout
- `cmd/hashbench/main.go` — CLI entry
- `internal/config/config.go` — config struct + validation
- `internal/workload/workload.go` — key selection, op decision, field/value generation
- `internal/metrics/metrics.go` — counters, latency collector, reporting ticker
- `Makefile`
- `go.mod`, `go.sum`
- `README.md` (optional)

## Concurrency Model
- Use a global context with cancel.
- Stop conditions:
  - duration timer -> cancel
  - requests counter reaches limit -> cancel
- Use `sync/atomic` to count completed ops globally.
- Workers:
  - spawn `threads` goroutines
  - each loops until ctx.Done()
  - chooses op type based on RNG and write_ratio
  - measures latency with `time.Now()` / `time.Since()`
  - records success/failure + latency into metrics

## RNG Strategy (important for concurrency)
- Create one `rand.Rand` per worker (local, not shared) to avoid lock contention.
- Seed:
  - If seed==0: use time.Now().UnixNano()
  - Worker seed = baseSeed + int64(workerID)*9973

## Field Count + Names
- For each op:
  - N = rng.Intn(max-min+1) + min
  - Build `[]string` fields length N: `f0...f(N-1)`
- Keep field names deterministic (based on index) for simplicity.
- Value generation:
  - Create `[]byte` of length value_bytes (default 16)
  - Fill with rng.Read(buf) for random bytes
  - For go-redis, pass as `string(buf)` or `[]byte` depending on API; ensure binary-safe.
  - Prefer `[]byte` if supported by the command builder; otherwise convert to string and accept that it may contain non-UTF8 (Redis is binary-safe).

## Key Selection
- keys: `bench:{prefix}:{i}`
- selection:
  - random: i = rng.Intn(keys)
  - sequential: maintain local counter and mod keys
- Consider adding hash tag `{prefix}` to keep keys in same cluster slot (even if cluster not required) — already in spec with `bench:{prefix}:i`.

## Redis Client / Connections
Using go-redis:
- `redis.NewClient(&redis.Options{Addr, Password, DB, PoolSize: conns, MinIdleConns: conns})`
- Also consider:
  - `PoolTimeout`, `DialTimeout`, `ReadTimeout`, `WriteTimeout`
- The pool will multiplex commands; PoolSize correlates with max connections.
- If pipeline depth > 1:
  - Use `Pipelined` or `Pipeline()` with `Exec()` per op.
  - But keep default pipeline=1 as direct commands.

## Command Implementation Details
### Write (HSET)
- Build args as `[]interface{}{field1, value1, field2, value2...}`
- Call: `client.HSet(ctx, key, args...).Err()`
- Important: go-redis HSet signature is `HSet(ctx, key, values ...interface{})`
  - so pass alternating field/value.

### Read (HMGET)
- `client.HMGet(ctx, key, fields...).Result()`
- Validate len(result)==N (it should).
- Do not fail if values are nil; only fail on Redis error.

## Metrics
Maintain:
- atomic counters: totalOps, readOps, writeOps, errors
- latency collectors:
  - overall, read, write
Implementation option A (simple and good enough):
- Each worker records latency into a buffered channel of `time.Duration`.
- A single aggregator goroutine reads and appends to slices with a cap (e.g., 1e6 samples total, or reservoir sampling).
- At end, sort slices and compute percentiles.
Option B:
- Fixed histogram buckets (log scale) + percentile approximation (more complex).

Also implement periodic reporting:
- Every report_interval, snapshot atomic counters and print ops/sec deltas.
- Print running p50/p99 if available (optional); final report must include percentiles.

## Makefile Requirements
Provide:
- `make build` -> `go build -trimpath -ldflags "-s -w" -o bin/hashbench ./cmd/hashbench`
- `make test` -> `go test ./...`
- `make clean` -> remove `bin/`
Optional:
- `make cross` building darwin/linux amd64/arm64 using `GOOS`/`GOARCH` (still pure Go).

Avoid CGO:
- set `CGO_ENABLED=0` in build targets.

## Config Validation (unit tests)
Add tests for:
- min_fields <= max_fields
- write_ratio in [0,1]
- threads, conns, keys > 0
- value_bytes == 16 by default but can be changed
- duration parsing
- requests/duration stop logic sanity

## Error Handling
- Count errors; include last error string in final report (store atomically with a mutex or `atomic.Value`).
- Continue on transient errors unless ctx canceled.

## Output Format
Human summary:
- config header
- totals
- throughput overall/read/write
- latency percentiles overall/read/write
If `--json`:
- emit a JSON object with the above fields to stdout (or to a file if you add `--out`).

## Deliverables Checklist
- Go module builds on macOS + Linux with `make build`
- CLI works: can run `./bin/hashbench --addr ...`
- Implements random field count [min,max], default [5,100]
- Values are length 16 bytes (default)
- Configurable keys/threads/conns
- Reports ops/sec and p50/p90/p95/p99/p99.9

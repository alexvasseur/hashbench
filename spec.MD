# Go Redis Multithread Client — Spec

## Goal
Build a cross-platform (macOS + Linux) Go CLI benchmark client for Redis that:
- Spawns a configurable number of worker threads (goroutines).
- Uses a configurable number of Redis connections (pool size).
- Performs configurable mixes of reads and writes on Redis Hashes.
- Uses weighted hash field counts per operation:
  - 35%: 5 fields
  - 50%: 10 fields
  - 10%: 20 fields
  - 5%: 100 fields
- Uses 16-byte field values.

The project must compile with `make` and produce a single binary.

## Redis Data Model
- Keys are Redis Hash keys.
- Key naming: `h<fieldCount>:{keyIndex}`
- Each hash contains N fields where N is chosen per operation based on the weighted distribution:
  - 35%: 5 fields
  - 50%: 10 fields
  - 10%: 20 fields
  - 5%: 100 fields
- Field names: `f{fieldIndex}` where fieldIndex is 0..N-1 for a given operation.
- Field values: exactly 16 bytes (binary-safe). The generator may produce random bytes.

## Operations
Each worker repeatedly performs operations until:
- `--requests` total operations have completed globally, OR
- `--duration` time limit elapses (if duration > 0), whichever is configured.

### Write Operation
- Choose a key (see Key Selection).
- Choose field count `N` in [min_fields, max_fields].
- Generate N field/value pairs:
  - field = `f<i>`
  - value = 16 bytes
- Execute `HSET key field1 value1 ... fieldN valueN`
- Count latency and success.

### Read Operation
- Choose a key (see Key Selection).
- Choose field count `N` in [min_fields, max_fields].
- Read the same field names you would write for this operation:
  - `HMGET key field1 ... fieldN`
- Validate response length:
  - should return N items; nils are allowed (key/field may not exist depending on workload).
- Count latency and success.

### Read/Write Mix
- A configurable `--write-ratio` in [0.0, 1.0] determines probability of write.
- Read probability is `1 - write_ratio`.

## Key Selection
- Configurable number of keys: `--keys`.
- Key index selection per operation:
  - Default: uniform random in [0, keys-1]
  - Optional: support `--key-pattern=sequential|random` (default sequential)

## Concurrency, Threads, Connections
- `--threads` controls number of worker goroutines per client.
- `--client` controls number of Redis client connections.
  - Total workers = `--threads` * `--client`.
- Workers should share a connection pool.
- The pool must cap concurrent in-flight Redis commands to `--client` (or be strongly correlated with it).

## Configuration / CLI
Command-line flags (minimum):
- `--addr` Redis address, default `127.0.0.1:6379`
- `--password` optional
- `--db` integer DB index, default 0
- `--tls` boolean to enable TLS (optional, if implemented)
- `--threads` default 8
- `--client` default 8
- `--keys` default 10000
- `--load` load data only (writes only)
- If `--load` is set and `--duration` is not provided, the benchmark writes each key once and stops after `--keys` keys (across all threads).
- If `--load` is set and `--duration` is provided, the benchmark first writes each key once, then cycles through the same `--keys` keyspace for the full duration.
- `--run` run mode ratio in `W:R` (uses `HSET` writes and `HGETALL` reads across the `--keys` keyspace)
- `--cluster` enable Redis Cluster mode
- `--value-bytes` default 16 (must allow 16, but keep configurable)
- `--write-ratio` default 0.5
- `--requests` total ops, default 0 (meaning disabled if duration is set)
- `--duration` benchmark duration (e.g. `10s`), default `10s`
- `--pipeline` optional integer pipeline depth, default 1
- `--seed` random seed (0 means time-based)
- `--report-interval` default `5s`
- `--json` output JSON summary in addition to human text

Behavior rules:
- If both `requests` and `duration` are set (>0), stop when either condition is met.
- Validate: value_bytes >= 1.
- Print config at start.

## Metrics / Output
Emit:
- Total ops, ops/sec overall
- Read ops/sec, Write ops/sec
- Error count and error rate
- Latency percentiles overall and per op type:
  - p50, p90, p95, p99, p99.9
- Optional: histogram buckets

Report periodically every `--report-interval` and print final summary at end.

## Build / Run Requirements
- `make build` builds `./bin/hashbench` (or similar)
- `make test` runs unit tests (at least config validation + RNG distribution sanity)
- `make lint` optional (if included)
- Must work on:
  - macOS (darwin/amd64 and darwin/arm64)
  - Linux (linux/amd64 and linux/arm64) if possible
- No CGO required (pure Go build).

## Dependencies
Allowed:
- A mainstream Redis Go client library (prefer go-redis v9).
- Minimal extra deps for CLI parsing (standard library `flag` is fine).
- Avoid heavy frameworks.

## Non-Goals
- Cluster slot-aware routing (optional)
- Lua scripting (not required)
- Keyspace preloading (optional)

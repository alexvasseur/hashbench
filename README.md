# Redis Hash Bench (Go)

A Go CLI benchmark tool for Redis Hash read/write workloads with configurable concurrency, key count, field counts, and read/write mix.

## Build

```bash
make build
```

Binary output:

```
bin/hashbench
```

## Quick Start

```bash
./bin/hashbench --addr 127.0.0.1:6379 --threads 16 --client 16 --keys 10000 --write-ratio 0.5 --duration 10s
```

## Sample Command (with options)

```bash
./bin/hashbench \
  --addr 127.0.0.1:6379 \
  --threads 32 \
  --client 32 \
  --keys 50000 \
  --value-bytes 16 \
  --write-ratio 0.3 \
  --requests 0 \
  --duration 30s \
  --pipeline 1 \
  --seed 0 \
  --report-interval 1s
```

## Load-Only Example

```bash
./bin/hashbench --addr 127.0.0.1:6379 --threads 16 --client 16 --keys 10000 --key-pattern sequential --load
```
Add `--duration 10s` to write the same keys multiple time for 10s.


## Run Mode Example (W:R)

```bash
./bin/hashbench --addr 127.0.0.1:6379 --threads 16 --client 16 --keys 1 --run 3:7 --duration 30s
```

## Field Count Distribution

Each operation uses a weighted field count distribution:

- 35%: 5 fields (about 350 bytes per key)
- 50%: 10 fields
- 10%: 20 fields
- 5%: 100 fields (about 6kB per key)

## Key Format

Keys are prefixed by field count so you can spot hash sizes in Redis:

- `h5:<id>`
- `h10:<id>`
- `h20:<id>`
- `h100:<id>`

## Options

- `--addr` Redis address (default: `127.0.0.1:6379`)
- `--password` Redis password
- `--db` Redis DB index (default: `0`)
- `--tls` Enable TLS
- `--threads` Worker goroutines per client (default: `8`)
- `--client` Redis client connections (default: `8`). Total workers = `--threads` * `--client`.
- `--keys` Number of keys (default: `10000`)
- `--value-bytes` Value size in bytes (default: `16`)
- `--write-ratio` Write probability in `[0,1]` (default: `0.5`)
- `--load` Load data only (writes only)
- If `--load` is set and `--duration` is not provided, the benchmark writes each key once and stops after `--keys` keys (across all threads).
- If `--load` is set and `--duration` is provided, the benchmark first writes each key once, then cycles through the same `--keys` keyspace for the full duration.
- `--run` Run mode ratio in `W:R` (e.g. `3:7`), uses `HSET` writes and `HGETALL` reads across the `--keys` keyspace
- `--requests` Total ops to execute, `0` disables (default: `0`)
- `--duration` Benchmark duration (default: `10s`)
- `--pipeline` Pipeline depth (default: `1`)
- `--seed` RNG seed, `0` uses time-based seed (default: `0`)
- `--report-interval` Periodic report interval (default: `5s`)
- `--key-pattern` `random` or `sequential` (default: `sequential`)
- `--cluster` Enable Redis Cluster mode (comma-separated `--addr` list supported)
- `--json` Emit JSON summary at end

## Output

The tool prints a periodic throughput report and a final summary including:

- Total ops, errors, error rate
- Ops/sec overall, read, write
- Latency percentiles overall, read, write (p50/p90/p95/p99/p99.9)

## Tests

```bash
make test
```

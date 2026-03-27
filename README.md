# Redis Hash Bench (Go)

A Go CLI benchmark tool for Redis Hash read/write workloads with configurable concurrency, key count, field counts, and read/write mix.

## Build

```bash
make build

# or for cross platform build (build on Mac, output a binary for Linux)
make cross
```

Binary output:

```
bin/hashbench
```
(and other builds for other platform)

## Quick Start

```bash
./bin/hashbench --addr 127.0.0.1:6379 --threads 16 --client 16 --keys 10000 --run 1:1
```

## Sample Command (with options)

```bash
./bin/hashbench \
  --addr 127.0.0.1:6379 \
  --threads 32 \
  --client 32 \
  --keys 50000 \
  --value-bytes 16 \
  --run 3:7 \
  --requests 0 \
  --pipeline 1 \
  --seed 0 \
  --report-interval 4s
```

## Load-Only Example

```bash
./bin/hashbench --addr 127.0.0.1:6379 --threads 16 --client 16 --keys 10000 --key-pattern sequential --load
```


## Run Mode Example (W:R)

```bash
./bin/hashbench --addr 127.0.0.1:6379 --threads 16 --client 16 --keys 1 --run 3:7 --cluster
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

The `<id>` is monotonic, so you will have `h5:1` .. `h5:2` but no other `*:1` or `*:2` keys.

## Options

- `--addr` Redis address (default: `127.0.0.1:6379`)
- `--password` Redis password
- `--db` Redis DB index (default: `0`)
- `--tls` Enable TLS
- `--threads` Worker goroutines per client (default: `8`)
- `--client` Redis client connections (default: `8`). Total workers = `--threads` * `--client`.
- `--keys` Number of keys (default: `10000`)
- `--value-bytes` Value size in bytes (default: `16`)
- `--run` Write:read ratio (e.g. `3:7`, default: `1:1`)
- `--load` Load data only (writes only)
- If `--load` is set, the benchmark writes each key once and stops after `--keys` keys (across all threads).
- `--run` Run mode ratio in `W:R` (e.g. `3:7`), uses `HSET` writes and `HGETALL` reads across the `--keys` keyspace
- `--requests` Total ops to execute, `0` disables (default: `0`)
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

## Large scale testing

Connection setup
```
export RH="--addr redis-12000.cluster.avasseur-default.demo.redislabs.com:12000 --password XXX --cluster"
```

Data loading only
```
./bin/hashbench $RH \
--threads 10 --client 15 \
--load \
--pipeline 50 \
--keys 100000000 --key-pattern sequential \
--cluster
```

Read only example (no pipeline)
```
./bin/hashbench $RH \
--threads 10 --client 15 \
--run 0:3 \
--keys 100000000 --key-pattern sequential \
--cluster 

(example output with Redis Flex on C4 with 20% RAM/SSD)
...
elapsed=39m33.608381475s total=537888091 errors=0 error-rate=0.0000
throughput	226612 ops/s			226612 read/s			0 write/s
overall		p50=0.561 	0.929 p90	1.189 p95	3.494 p99	5.601 p99.9
read    	p50=0.560 	0.924 p90	1.174 p95	3.486 p99	5.536 p99.9
write   	p50=0.000 	0.000 p90	0.000 p95	0.000 p99	0.000 p99.9
```
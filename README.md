# Redis Hash Bench (Go & Java Lettuce)

A Go CLI benchmark tool for Redis Hash read/write workloads with configurable concurrency, key count, field size, and read/write mix, cluster mode and rate limiting.

A compatible Java Lettuce benchmark tool using the same CLI options and same data structures.

Each Redis key/value hash uses a weighted field count distribution:
- 35%: 5 fields (about 350 bytes per key)
- 50%: 10 fields
- 10%: 20 fields
- 5%: 100 fields (about 6kB per key)

## Java Lettuce

Please refer to the [lettuce/README.md](lettuce/README.md) for the Java Lettuce CLI client.

Use the Go CLI client for initial fast data loading (the `--load` option is only available in Go CLI client)


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

## Redis Hash field Count Distribution

Each Redis key/value hash uses a weighted field count distribution:

- 35%: 5 fields (about 350 bytes per key)
- 50%: 10 fields
- 10%: 20 fields
- 5%: 100 fields (about 5kB per key if 40 byte per field)

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
- `--qps` Global ops/sec limit (divided evenly per client, `0` disables)
- `--conn` Total extra idle connections to open and ping every 30s
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

## Example 100 million keys testing

The below example is with 100 millions keys.
This is by default ~36GB of primary data (unless using a different `--value-bytes`) so fundamentally not that large but can help have enough data to distribute across many shards or nodes.

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

(example output with Redis Flex on C4 with 20% RAM/SSD -- your number may be different and those numbers don't reflect any valid performance outcome or best result possible and are for illustration purpose only).
...
[005 s] 241100 ops   101 slow   48220 o/s   24109 r/s   24111 w/s  1.25 avg  1.20 p50  1.45 p75  1.90 p95  2.32 p99  3.01 p99.9 19.80 p99.99 21.29 slow
[010 s] 464109 ops     0 slow   44602 o/s   22322 r/s   22280 w/s  1.49 avg  1.49 p50  1.75 p75  2.24 p95  2.55 p99  3.03 p99.9  4.43 p99.99  0.00 slow

Summary
elapsed=11.796970042s total=555597 errors=101 error-rate=0.0002
throughput	47097 ops/s			23564 r/s			23533 w/s
overall		1.220 p50	1.506 p75	1.920 p95	2.236 p99	2.633 p99.9	2.885 p99.99
read    	1.219 p50	1.507 p75	1.921 p95	2.240 p99	2.624 p99.9	2.937 p99.99
write   	1.222 p50	1.506 p75	1.920 p95	2.230 p99	2.651 p99.9	2.874 p99.99```

For a read only example with rate limiting
```
./bin/hashbench $RH \
--threads 20 --client 50 \
--run 0:3 \
--keys 100000000 --key-pattern sequential \
--cluster \
--qps 100000
```

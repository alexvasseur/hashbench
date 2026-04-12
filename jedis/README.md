# Hashbench (Java Jedis)

A single‑file Java benchmark client using Jedis that mirrors the Go `hashbench` `--run` behavior and keyspace.
It targets Redis Hash read/write mixes with deterministic key shapes.

## NOT FINAL 

Careful this version with Jedis is not fully final yet.
The pipeline with cluster connection is not optimized.



## Build

```bash
mvn -q -DskipTests package
```

Output jar:

```
target/hashbench-<version>.jar
```

## Run (Localhost)

```bash
java -jar target/hashbench-1.2.0.jar \
  --addr 127.0.0.1:6379 \
  --threads 10 \
  --client 30 \
  --keys 10000 \
  --run 3:7
```

## Options

- `--addr` Redis address (default: `127.0.0.1:6379`)
- `--password` Redis password
- `--tls` Enable TLS
- `--cluster` Enable Redis Cluster mode (flag only)
- `--threads` Worker threads per client (default: `8`)
- `--client` Redis client connections (default: `8`), total workers = `--threads` * `--client`
- `--keys` Number of keys (default: `10000`)
- `--value-bytes` Value size in bytes (default: `16`)
- `--run` Write:read ratio (e.g. `3:7`, default: `1:1`)
- `--load` Only load all keys (exactly once, flag only)
- `--requests` Total ops to execute, `0` disables (default: `0`)
- `--pipeline` Pipeline depth (default: `1`)
- `--seed` RNG seed, `0` uses time-based seed (default: `0`)
- `--report-interval` Periodic report interval (default: `5s`)
- `--key-pattern` `random` or `sequential` (default: `sequential`)
- `--qps` Global ops/sec limit (divided evenly per client, `0` disables)

## Jedis Pool Note

This client opens **one Jedis connection per client** (no pool) for simplicity and determinism.
If you want a pool, use `JedisPoolConfig` and tune:

- `maxTotal`
- `maxIdle`
- `minIdle`
- `testOnBorrow`
- `blockWhenExhausted`

## Performance (Recommended JVM Flags)

## Performance (Recommended JVM Flags)

apt install openjdk-21-jre

Connection setup
```
export RH="--addr redis-12000.cluster.avasseur-default.demo.redislabs.com:12000 --password XXX --cluster"
```

```bash
java \
  -Xms4g -Xmx4g \
  -XX:+UseG1GC -XX:MaxGCPauseMillis=50 \
  -XX:+AlwaysPreTouch -XX:+ParallelRefProcEnabled -XX:+UseStringDeduplication \
  -XX:+UnlockExperimentalVMOptions -XX:+UseNUMA \
  -XX:InitiatingHeapOccupancyPercent=35 -XX:G1ReservePercent=15 \
  -XX:ConcGCThreads=4 -XX:ParallelGCThreads=8 \
  -XX:+UnlockDiagnosticVMOptions -XX:+LogVMOutput -XX:LogFile=jvm.log \
  -jar hashbench-jedis-1.4.0.jar \
  $RH \
  --client 30 --threads 10 --run 0:3 --key-pattern sequential --keys 1000000000 --value-bytes 40 --pipeline 100
```

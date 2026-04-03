import io.lettuce.core.RedisClient;
import io.lettuce.core.RedisFuture;
import io.lettuce.core.RedisURI;
import io.lettuce.core.SocketOptions;
import io.lettuce.core.codec.ByteArrayCodec;
import io.lettuce.core.cluster.ClusterClientOptions;
import io.lettuce.core.cluster.ClusterTopologyRefreshOptions;
import io.lettuce.core.cluster.RedisClusterClient;
import io.lettuce.core.cluster.api.StatefulRedisClusterConnection;
import io.lettuce.core.cluster.api.async.RedisAdvancedClusterAsyncCommands;
import io.lettuce.core.api.StatefulRedisConnection;
import io.lettuce.core.api.async.RedisAsyncCommands;
import io.lettuce.core.resource.ClientResources;
import io.lettuce.core.resource.DefaultClientResources;

import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.LongAdder;

public class ClusterHashReadBench {

    static final class Config {
        String addr = "127.0.0.1:6379";
        String password = null;
        boolean tls = false;
        boolean cluster = false;

        int threads = 8;     // per client
        int clients = 8;
        int keys = 10000;
        int valueBytes = 16;
        String run = "1:1";  // W:R
        long requests = 0;
        long seed = 0L;
        String reportInterval = "5s";
        String keyPattern = "sequential"; // random|sequential
        int qps = 0;         // global

        static Config parse(String[] args) {
            Config c = new Config();

            for (int i = 0; i < args.length; i++) {
                String arg = args[i];
                if (!arg.startsWith("--")) {
                    continue;
                }

                String k;
                String v = null;
                int eq = arg.indexOf('=');
                if (eq > 0) {
                    k = arg.substring(2, eq);
                    v = arg.substring(eq + 1);
                } else {
                    k = arg.substring(2);
                    if (!k.equals("cluster") && !k.equals("help")) {
                        if (i + 1 >= args.length || args[i + 1].startsWith("--")) {
                            System.err.println("Missing value for --" + k);
                            usageAndExit(1);
                        }
                        v = args[++i];
                    }
                }

                switch (k) {
                    case "addr" -> c.addr = v;
                    case "password" -> c.password = v;
                    case "tls" -> c.tls = Boolean.parseBoolean(v);
                    case "cluster" -> c.cluster = true;
                    case "threads" -> c.threads = Integer.parseInt(v);
                    case "client" -> c.clients = Integer.parseInt(v);
                    case "keys" -> c.keys = Integer.parseInt(v);
                    case "value-bytes" -> c.valueBytes = Integer.parseInt(v);
                    case "run" -> c.run = v;
                    case "requests" -> c.requests = Long.parseLong(v);
                    case "seed" -> c.seed = Long.parseLong(v);
                    case "report-interval" -> c.reportInterval = v;
                    case "key-pattern" -> c.keyPattern = v.toLowerCase(Locale.ROOT);
                    case "qps" -> c.qps = Integer.parseInt(v);
                    case "help" -> usageAndExit(0);
                    default -> {
                        System.err.println("Unknown arg: " + arg);
                        usageAndExit(1);
                    }
                }
            }

            if (c.threads <= 0) die("threads must be > 0");
            if (c.clients <= 0) die("client must be > 0");
            if (c.keys <= 0) die("keys must be > 0");
            if (c.valueBytes <= 0) die("value-bytes must be > 0");
            if (c.qps < 0) die("qps must be >= 0");
            if (!c.keyPattern.equals("random") && !c.keyPattern.equals("sequential")) {
                die("key-pattern must be random or sequential");
            }

            return c;
        }

        static void usageAndExit(int code) {
            System.out.println("""
                Usage:
                  java -jar target/lettuce-cluster-hash-read-bench-jar-with-dependencies.jar [options]

                Options:
                  --addr 127.0.0.1:6379
                  --password secret
                  --tls true|false
                  --cluster
                  --threads 8
                  --client 8
                  --keys 10000
                  --value-bytes 16
                  --run 3:7
                  --requests 0
                  --seed 0
                  --report-interval 5s
                  --key-pattern random|sequential
                  --qps 0
                  --help

                Key format:
                  h<fieldCount>:<keyId>
                """);
            System.exit(code);
        }

        static void die(String msg) {
            System.err.println("Config error: " + msg);
            System.exit(2);
        }
    }

    interface AsyncOps {
        RedisFuture<Long> hset(byte[] key, Map<byte[], byte[]> map);
        RedisFuture<Map<byte[], byte[]>> hgetall(byte[] key);
    }

    static final class ClientHandle {
        final AsyncOps ops;
        final AutoCloseable closeable;

        ClientHandle(AsyncOps ops, AutoCloseable closeable) {
            this.ops = ops;
            this.closeable = closeable;
        }
    }

    static final class Stats {
        final LongAdder totalOps = new LongAdder();
        final LongAdder readOps = new LongAdder();
        final LongAdder writeOps = new LongAdder();
        final LongAdder errOps = new LongAdder();
        final LongAdder intervalErrOps = new LongAdder();

        static final long SLOW_ERR_NANOS = TimeUnit.MILLISECONDS.toNanos(10);

        final Reservoir overall = new Reservoir(200_000);
        final Reservoir reads = new Reservoir(200_000);
        final Reservoir writes = new Reservoir(200_000);

        final ConcurrentLinkedQueue<Long> intervalLatNanos = new ConcurrentLinkedQueue<>();
        final ConcurrentLinkedQueue<String> intervalErrors = new ConcurrentLinkedQueue<>();

        final long startNanos = System.nanoTime();
        final long warmupNanos = TimeUnit.SECONDS.toNanos(10);

        void record(boolean isRead, long latencyNanos, Throwable err) {
            totalOps.increment();
            if (isRead) {
                readOps.increment();
            } else {
                writeOps.increment();
            }

            if (err != null) {
                errOps.increment();
                intervalErrors.add(rootMessage(err));
                return;
            }

            intervalLatNanos.add(latencyNanos);
            if (latencyNanos >= SLOW_ERR_NANOS) {
                errOps.increment();
                intervalErrOps.increment();
            }
            if (System.nanoTime() - startNanos >= warmupNanos) {
                overall.add(latencyNanos);
                if (isRead) {
                    reads.add(latencyNanos);
                } else {
                    writes.add(latencyNanos);
                }
            }
        }
    }

    static final class Reservoir {
        private final long[] samples;
        private long seen = 0;
        private int size = 0;
        private long state = 88172645463393265L;

        Reservoir(int cap) {
            this.samples = new long[cap];
        }

        void add(long value) {
            seen++;
            if (size < samples.length) {
                samples[size++] = value;
                return;
            }
            long r = nextRand() % seen;
            if (r < samples.length) {
                samples[(int) r] = value;
            }
        }

        long[] snapshot() {
            return Arrays.copyOf(samples, size);
        }

        private long nextRand() {
            long x = state;
            x ^= x << 13;
            x ^= x >>> 7;
            x ^= x << 17;
            state = x;
            return x & Long.MAX_VALUE;
        }
    }

    public static void main(String[] args) throws Exception {
        Config cfg = Config.parse(args);

        double writeRatio = parseRun(cfg.run);

        System.out.printf("Redis Hash Bench (Java Lettuce)\n");
        int totalWorkers = cfg.threads * cfg.clients;
        System.out.printf("addr=%s tls=%s cluster=%s threads=%d client=%d total-workers=%d keys=%d\n",
                cfg.addr, cfg.tls, cfg.cluster, cfg.threads, cfg.clients, totalWorkers, cfg.keys);
        System.out.printf("value-bytes=%d run=%s requests=%d qps=%d seed=%d key-pattern=%s\n",
                cfg.valueBytes, cfg.run, cfg.requests, cfg.qps, cfg.seed, cfg.keyPattern);

        ClientResources resources = DefaultClientResources.builder().build();
        List<RedisURI> uris = parseRedisUris(cfg);

        List<ClientHandle> clients = new ArrayList<>(cfg.clients);
        List<AutoCloseable> extraCloseables = new ArrayList<>();
        if (cfg.cluster) {
            RedisClusterClient clusterClient = RedisClusterClient.create(resources, uris);

            SocketOptions.KeepAliveOptions keepAliveOptions = SocketOptions.KeepAliveOptions.builder()
                    .interval(Duration.ofSeconds(5))
                    .idle(Duration.ofSeconds(5))
                    .count(3)
                    .enable()
                    .build();

            ClusterTopologyRefreshOptions topologyRefreshOptions = ClusterTopologyRefreshOptions.builder()
                    .enableAllAdaptiveRefreshTriggers()
                    .adaptiveRefreshTriggersTimeout(Duration.ofSeconds(30))
                    .build();

            ClusterClientOptions options = buildClusterOptions(topologyRefreshOptions, keepAliveOptions, true);
            try {
                clusterClient.setOptions(options);
            } catch (IllegalStateException e) {
                ClusterClientOptions fallback = buildClusterOptions(topologyRefreshOptions, keepAliveOptions, false);
                clusterClient.setOptions(fallback);
                System.err.println("[WARN] TCP user timeout not supported; continuing without it.");
            }
            extraCloseables.add(clusterClient);
            for (int i = 0; i < cfg.clients; i++) {
                StatefulRedisClusterConnection<byte[], byte[]> conn = clusterClient.connect(new ByteArrayCodec());
                RedisAdvancedClusterAsyncCommands<byte[], byte[]> async = conn.async();
                AsyncOps ops = new AsyncOps() {
                    @Override
                    public RedisFuture<Long> hset(byte[] key, Map<byte[], byte[]> map) {
                        return async.hset(key, map);
                    }

                    @Override
                    public RedisFuture<Map<byte[], byte[]>> hgetall(byte[] key) {
                        return async.hgetall(key);
                    }
                };
                clients.add(new ClientHandle(ops, conn));
            }
        } else {
            RedisClient client = RedisClient.create(resources, uris.get(0));
            extraCloseables.add(client);
            for (int i = 0; i < cfg.clients; i++) {
                StatefulRedisConnection<byte[], byte[]> conn = client.connect(new ByteArrayCodec());
                RedisAsyncCommands<byte[], byte[]> async = conn.async();
                AsyncOps ops = new AsyncOps() {
                    @Override
                    public RedisFuture<Long> hset(byte[] key, Map<byte[], byte[]> map) {
                        return async.hset(key, map);
                    }

                    @Override
                    public RedisFuture<Map<byte[], byte[]>> hgetall(byte[] key) {
                        return async.hgetall(key);
                    }
                };
                clients.add(new ClientHandle(ops, conn));
            }
        }

        Stats stats = new Stats();
        AtomicBoolean stop = new AtomicBoolean(false);
        AtomicLong completed = new AtomicLong(0);

        long perClientQps = cfg.qps > 0 ? Math.max(1, Math.round((double) cfg.qps / cfg.clients)) : 0;
        AtomicLong[] nextSlots = new AtomicLong[cfg.clients];
        long now = System.nanoTime();
        for (int i = 0; i < cfg.clients; i++) {
            nextSlots[i] = new AtomicLong(now);
        }

        ScheduledExecutorService reporter = Executors.newSingleThreadScheduledExecutor();
        long reportNanos = parseDurationNanos(cfg.reportInterval);
        reporter.scheduleAtFixedRate(new Reporter(stats), reportNanos, reportNanos, TimeUnit.NANOSECONDS);

        ExecutorService workers = Executors.newFixedThreadPool(totalWorkers);

        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            stop.set(true);
            workers.shutdownNow();
            reporter.shutdownNow();
            closeQuietlyAllClients(clients);
            closeQuietlyAll(extraCloseables);
        }));

        for (int i = 0; i < totalWorkers; i++) {
            int workerId = i;
            int clientIdx = workerId % cfg.clients;
            ClientHandle handle = clients.get(clientIdx);
            workers.submit(() -> runWorker(workerId, clientIdx, cfg, handle, stats, stop, completed, writeRatio, nextSlots[clientIdx], perClientQps));
        }

        while (!stop.get()) {
            if (cfg.requests > 0 && completed.get() >= cfg.requests) {
                stop.set(true);
                workers.shutdown();
                reporter.shutdown();
                break;
            }
            Thread.sleep(200);
        }

        workers.awaitTermination(30, TimeUnit.SECONDS);
        reporter.shutdownNow();
        printSummary(stats);
        closeQuietlyAllClients(clients);
        closeQuietlyAll(extraCloseables);
    }

    static void runWorker(
            int workerId,
            int clientIdx,
            Config cfg,
            ClientHandle handle,
            Stats stats,
            AtomicBoolean stop,
            AtomicLong completed,
            double writeRatio,
            AtomicLong nextSlot,
            long perClientQps
    ) {
        long baseSeed = cfg.seed != 0 ? cfg.seed : System.nanoTime();
        Random rnd = new Random(baseSeed + (workerId * 9973L));
        long seq = workerId;

        while (!stop.get() && !Thread.currentThread().isInterrupted()) {
            if (!pace(stop, nextSlot, perClientQps)) {
                return;
            }
            int keyIdx = nextIndex(cfg, rnd, seq);
            if (cfg.keyPattern.equals("sequential")) {
                seq++;
            }
            int fieldCount = fieldCountForIndex(keyIdx);
            byte[] key = keyFor(fieldCount, keyIdx);
            boolean isWrite = rnd.nextDouble() < writeRatio;

            if (isWrite) {
                Map<byte[], byte[]> map = buildValues(fieldCount, cfg.valueBytes, rnd);
                long start = System.nanoTime();
                try {
                    handle.ops.hset(key, map).get();
                    stats.record(false, System.nanoTime() - start, null);
                } catch (Exception e) {
                    stats.record(false, System.nanoTime() - start, e);
                }
            } else {
                long start = System.nanoTime();
                try {
                    handle.ops.hgetall(key).get();
                    stats.record(true, System.nanoTime() - start, null);
                } catch (Exception e) {
                    stats.record(true, System.nanoTime() - start, e);
                }
            }
            completed.incrementAndGet();
        }
    }

    static int nextIndex(Config cfg, Random rnd, long seq) {
        if (cfg.keyPattern.equals("sequential")) {
            return (int) Math.floorMod(seq, cfg.keys);
        }
        return rnd.nextInt(cfg.keys);
    }

    static int fieldCountForIndex(int idx) {
        int n = idx % 100;
        if (n < 35) return 5;
        if (n < 85) return 10;
        if (n < 95) return 20;
        return 100;
    }

    static byte[] keyFor(int fieldCount, int idx) {
        return ("h" + fieldCount + ":" + idx).getBytes(StandardCharsets.UTF_8);
    }

    static Map<byte[], byte[]> buildValues(int fields, int valueBytes, Random rnd) {
        Map<byte[], byte[]> map = new java.util.HashMap<>(fields * 2);
        for (int i = 0; i < fields; i++) {
            byte[] f = ("f" + i).getBytes(StandardCharsets.UTF_8);
            byte[] v = new byte[valueBytes];
            rnd.nextBytes(v);
            map.put(f, v);
        }
        return map;
    }

    static boolean pace(AtomicBoolean stop, AtomicLong nextSlot, long perClientQps) {
        if (perClientQps <= 0) return true;
        long interval = (long) (1_000_000_000.0 / perClientQps);
        if (interval < 1_000) interval = 1_000;
        long slot = nextSlot.addAndGet(interval);
        long now = System.nanoTime();
        if (slot > now) {
            long sleepNanos = slot - now;
            try {
                TimeUnit.NANOSECONDS.sleep(sleepNanos);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return false;
            }
        }
        return !stop.get();
    }

    static double parseRun(String s) {
        String[] parts = s.split(":");
        if (parts.length != 2) {
            throw new IllegalArgumentException("invalid --run format, expected W:R");
        }
        double w = Double.parseDouble(parts[0].trim());
        double r = Double.parseDouble(parts[1].trim());
        if (w < 0 || r < 0 || (w + r) == 0) {
            throw new IllegalArgumentException("invalid --run ratio values");
        }
        return w / (w + r);
    }

    static long parseDurationNanos(String s) {
        String v = s.trim().toLowerCase(Locale.ROOT);
        if (v.endsWith("ms")) {
            return TimeUnit.MILLISECONDS.toNanos(Long.parseLong(v.substring(0, v.length() - 2)));
        }
        if (v.endsWith("s")) {
            return TimeUnit.SECONDS.toNanos(Long.parseLong(v.substring(0, v.length() - 1)));
        }
        return TimeUnit.SECONDS.toNanos(Long.parseLong(v));
    }

    static List<RedisURI> parseRedisUris(Config cfg) {
        List<RedisURI> out = new ArrayList<>();
        for (String node : cfg.addr.split(",")) {
            String trimmed = node.trim();
            if (trimmed.isEmpty()) continue;
            String[] parts = trimmed.split(":");
            if (parts.length != 2) {
                throw new IllegalArgumentException("Bad addr: " + trimmed);
            }
            RedisURI.Builder b = RedisURI.builder()
                    .withHost(parts[0])
                    .withPort(Integer.parseInt(parts[1]))
                    .withTimeout(Duration.ofSeconds(30));
            if (cfg.password != null && !cfg.password.isEmpty()) {
                b.withPassword(cfg.password.toCharArray());
            }
            if (cfg.tls) b.withSsl(true);
            out.add(b.build());
        }
        if (out.isEmpty()) throw new IllegalArgumentException("No valid addr supplied");
        return out;
    }

    static final class Reporter implements Runnable {
        private final Stats stats;
        private long prevOps = 0;
        private long prevRead = 0;
        private long prevWrite = 0;
        private long prevErr = 0;
        private long prevTime = System.nanoTime();
        private final long start = System.nanoTime();

        Reporter(Stats stats) {
            this.stats = stats;
        }

        @Override
        public void run() {
            long now = System.nanoTime();
            long total = stats.totalOps.sum();
            long reads = stats.readOps.sum();
            long writes = stats.writeOps.sum();
            long errs = stats.errOps.sum();
            long intervalErrs = stats.intervalErrOps.sumThenReset();

            long dOps = total - prevOps;
            long dRead = reads - prevRead;
            long dWrite = writes - prevWrite;
            long dErr = errs - prevErr;

            double secs = (now - prevTime) / 1_000_000_000.0;
            double opsSec = secs > 0 ? dOps / secs : 0.0;
            double readSec = secs > 0 ? dRead / secs : 0.0;
            double writeSec = secs > 0 ? dWrite / secs : 0.0;
            double errSec = secs > 0 ? dErr / secs : 0.0;

            long[] interval = drainLatencies(stats.intervalLatNanos);
            Arrays.sort(interval);
            double avgMs = interval.length == 0 ? 0.0 : avgMs(interval);
            double p50 = percentileSortedMs(interval, 0.50);
            double p75 = percentileSortedMs(interval, 0.75);
            double p95 = percentileSortedMs(interval, 0.95);
            double p99 = percentileSortedMs(interval, 0.99);
            double p999 = percentileSortedMs(interval, 0.999);
            double p9999 = percentileSortedMs(interval, 0.9999);
            double maxErr = maxAtOrOverMs(interval, Stats.SLOW_ERR_NANOS);

            int uptime = (int) ((now - start) / 1_000_000_000L);
            System.out.printf(Locale.ROOT,
                    "[%03d s]%7d ops %5d slow %7d o/s %7d r/s %7d w/s %5.2f avg %5.2f p50 %5.2f p75 %5.2f p95 %5.2f p99 %5.2f p99.9 %5.2f p99.99 %5.2f slow\n",
                    uptime,
                    total,
                    intervalErrs,
                    Math.round(opsSec),
                    Math.round(readSec),
                    Math.round(writeSec),
                    avgMs,
                    p50,
                    p75,
                    p95,
                    p99,
                    p999,
                    p9999,
                    maxErr
            );

            List<String> errsMsg = drainErrors(stats.intervalErrors);
            for (String msg : errsMsg) {
                System.err.printf(Locale.ROOT, "[%03d s] %s%n", uptime, msg);
            }

            prevOps = total;
            prevRead = reads;
            prevWrite = writes;
            prevErr = errs;
            prevTime = now;
        }
    }

    static void printSummary(Stats stats) {
        long total = stats.totalOps.sum();
        long read = stats.readOps.sum();
        long write = stats.writeOps.sum();
        long err = stats.errOps.sum();

        long[] all = stats.overall.snapshot();
        long[] r = stats.reads.snapshot();
        long[] w = stats.writes.snapshot();

        System.out.println("\nSummary");
        System.out.printf(Locale.ROOT, "total=%d errors=%d error-rate=%.4f%n",
                total, err, total > 0 ? (double) err / total : 0.0);
        System.out.printf(Locale.ROOT, "latency overall %.3f p50 %.3f p75 %.3f p95 %.3f p99 %.3f p99.9 %.3f p99.99%n",
                percentileMs(all, 0.50), percentileMs(all, 0.75), percentileMs(all, 0.95), percentileMs(all, 0.99), percentileMs(all, 0.999), percentileMs(all, 0.9999));
        System.out.printf(Locale.ROOT, "latency read    %.3f p50 %.3f p75 %.3f p95 %.3f p99 %.3f p99.9 %.3f p99.99%n",
                percentileMs(r, 0.50), percentileMs(r, 0.75), percentileMs(r, 0.95), percentileMs(r, 0.99), percentileMs(r, 0.999), percentileMs(r, 0.9999));
        System.out.printf(Locale.ROOT, "latency write   %.3f p50 %.3f p75 %.3f p95 %.3f p99 %.3f p99.9 %.3f p99.99%n",
                percentileMs(w, 0.50), percentileMs(w, 0.75), percentileMs(w, 0.95), percentileMs(w, 0.99), percentileMs(w, 0.999), percentileMs(w, 0.9999));
    }

    static long[] drainLatencies(ConcurrentLinkedQueue<Long> q) {
        List<Long> out = new ArrayList<>();
        Long v;
        while ((v = q.poll()) != null) {
            out.add(v);
        }
        long[] arr = new long[out.size()];
        for (int i = 0; i < out.size(); i++) {
            arr[i] = out.get(i);
        }
        return arr;
    }

    static List<String> drainErrors(ConcurrentLinkedQueue<String> q) {
        List<String> out = new ArrayList<>();
        String v;
        while ((v = q.poll()) != null) {
            if (v != null && !v.isEmpty()) out.add(v);
        }
        return out;
    }

    static double avgMs(long[] nanos) {
        if (nanos.length == 0) return 0.0;
        double sum = 0.0;
        for (long n : nanos) sum += n / 1_000_000.0;
        return sum / nanos.length;
    }

    static double percentileMs(long[] nanos, double p) {
        if (nanos.length == 0) return 0.0;
        Arrays.sort(nanos);
        return percentileSortedMs(nanos, p);
    }

    static double percentileSortedMs(long[] sorted, double p) {
        if (sorted.length == 0) return 0.0;
        int idx = (int) Math.ceil(p * sorted.length) - 1;
        if (idx < 0) idx = 0;
        if (idx >= sorted.length) idx = sorted.length - 1;
        return sorted[idx] / 1_000_000.0;
    }

    static double maxAtOrOverMs(long[] sorted, long thresholdNanos) {
        double max = 0.0;
        for (int i = sorted.length - 1; i >= 0; i--) {
            if (sorted[i] >= thresholdNanos) {
                max = sorted[i] / 1_000_000.0;
                break;
            }
        }
        return max;
    }

    static String rootMessage(Throwable t) {
        Throwable cur = t;
        while (cur.getCause() != null) cur = cur.getCause();
        return cur.getMessage() == null ? cur.toString() : cur.getMessage();
    }

    static void closeQuietlyAllClients(List<ClientHandle> clients) {
        for (ClientHandle h : clients) {
            closeQuietly(h.closeable);
        }
    }

    static void closeQuietlyAll(List<? extends AutoCloseable> closeables) {
        for (AutoCloseable c : closeables) {
            closeQuietly(c);
        }
    }

    static void closeQuietly(AutoCloseable c) {
        if (c == null) return;
        try {
            c.close();
        } catch (Exception ignored) {
        }
    }

    static ClusterClientOptions buildClusterOptions(
            ClusterTopologyRefreshOptions topologyRefreshOptions,
            SocketOptions.KeepAliveOptions keepAliveOptions,
            boolean withTcpUserTimeout
    ) {
        SocketOptions.Builder socketBuilder = SocketOptions.builder()
                .keepAlive(keepAliveOptions);
        if (withTcpUserTimeout) {
            SocketOptions.TcpUserTimeoutOptions tcpUserTimeout = SocketOptions.TcpUserTimeoutOptions.builder()
                    .tcpUserTimeout(Duration.ofSeconds(20))
                    .enable()
                    .build();
            socketBuilder.tcpUserTimeout(tcpUserTimeout);
        }
        return ClusterClientOptions.builder()
                .topologyRefreshOptions(topologyRefreshOptions)
                .socketOptions(socketBuilder.build())
                .build();
    }
}

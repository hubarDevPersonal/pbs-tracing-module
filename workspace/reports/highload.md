# High-load run 2026-09-20 21:52 UTC

Docker: 4 CPUs for Prebid Server. Stub bidder delay 10ms. 3 runs of 15s per cell; medians, with min–max where it matters. Open-loop steps hold an arrival rate; a step is *sustained* when the achieved rate is ≥ 95 % of the target with no errors and no dropped arrivals. The closed loop keeps 128 requests in flight and measures throughput. CPU is Prebid Server's cgroup time; the generator and the stub run outside the VM.

## 200 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 200 | yes | 12.7ms | 14.2ms | 16.7ms (16.5ms–17.7ms) | 23.5ms | 2.292ms | 0.46 | 0s | 0 | 0 | 0 | 0 | 0 | 4 | 450 MiB |
| hooks on, untraced | 200 | yes | 12.8ms | 14.2ms | 17.2ms (16.9ms–17.6ms) | 26.7ms | 2.52ms | 0.50 | 4µs | 0 | 0 | 0 | 0 | 0 | 4 | 449 MiB |
| active tracing | 200 | yes | 12.8ms | 14.1ms | 17.4ms (17.3ms–41.5ms) | 36.0ms | 2.782ms | 0.56 | 21µs | 0 | 0 | 0 | 2999 | 0 | 5 | 578 MiB |

## 400 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 400 | yes | 11.8ms | 12.6ms | 15.7ms (14.7ms–16.1ms) | 24.8ms | 1.787ms | 0.71 | 0s | 0 | 0 | 0 | 0 | 0 | 7 | 452 MiB |
| hooks on, untraced | 400 | yes | 12.0ms | 12.7ms | 15.7ms (15.3ms–16.1ms) | 33.3ms | 2.003ms | 0.80 | 3µs | 0 | 0 | 0 | 0 | 0 | 8 | 454 MiB |
| active tracing | 400 | yes | 12.2ms | 13.0ms | 16.3ms (15.6ms–16.4ms) | 25.5ms | 2.365ms | 0.95 | 18µs | 0 | 0 | 0 | 6000 | 0 | 9 | 587 MiB |

## 800 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 799 | yes | 11.9ms | 12.6ms | 15.5ms (15.0ms–15.7ms) | 27.4ms | 1.787ms | 1.43 | 0s | 0 | 0 | 0 | 0 | 0 | 14 | 466 MiB |
| hooks on, untraced | 799 | yes | 12.2ms | 13.2ms | 18.3ms (17.3ms–146.3ms) | 48.6ms | 1.944ms | 1.55 | 4µs | 0 | 0 | 0 | 0 | 0 | 16 | 468 MiB |
| active tracing | 799 | yes | 12.6ms | 13.9ms | 20.0ms (19.6ms–20.7ms) | 33.6ms | 2.199ms | 1.76 | 21µs | 0 | 0 | 0 | 12000 | 0 | 18 | 616 MiB |

## 1600 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 1599 | yes | 12.9ms | 19.7ms | 39.2ms (31.7ms–43.0ms) | 75.6ms | 1.411ms | 2.26 | 0s | 0 | 0 | 0 | 0 | 0 | 26 | 532 MiB |
| hooks on, untraced | 1599 | yes | 13.5ms | 29.1ms | 54.7ms (50.5ms–59.6ms) | 98.9ms | 1.49ms | 2.38 | 5µs | 0 | 0 | 0 | 0 | 0 | 31 | 538 MiB |
| active tracing | 1308 | no | 319.3ms | 501.8ms | 709.3ms (654.3ms–875.9ms) | 1171.5ms | 2.195ms | 2.88 | 240µs | 604 | 0 | 4092 | 12983 | 7800 | 26 | 763 MiB |

## closed loop, 128 in flight

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 2389 | yes | 50.0ms | 80.2ms | 121.3ms (111.9ms–124.3ms) | 200.7ms | 1.077ms | 2.58 | 0s | 0 | 0 | 0 | 0 | 0 | 39 | 556 MiB |
| hooks on, untraced | 2230 | yes | 54.1ms | 86.1ms | 120.8ms (117.1ms–121.8ms) | 184.8ms | 1.217ms | 2.71 | 20µs | 1 | 0 | 0 | 0 | 0 | 43 | 549 MiB |
| active tracing | 1244 | yes | 99.2ms | 137.8ms | 189.4ms (188.0ms–197.1ms) | 308.8ms | 2.277ms | 2.83 | 80µs | 76 | 0 | 0 | 18571 | 200 | 25 | 693 MiB |

## Module cost against hooks off

| step | configuration | Δ CPU/auction | Δ p50 | Δ p99 | Δ throughput |
|---|---|---:|---:|---:|---:|
| 200 rps | hooks on, untraced | +0.2ms | +0.1ms | +0.5ms | +0 rps |
| 200 rps | active tracing | +0.5ms | +0.1ms | +0.8ms | +0 rps |
| 400 rps | hooks on, untraced | +0.2ms | +0.1ms | -0.0ms | +0 rps |
| 400 rps | active tracing | +0.6ms | +0.3ms | +0.5ms | +0 rps |
| 800 rps | hooks on, untraced | +0.2ms | +0.3ms | +2.9ms | -0 rps |
| 800 rps | active tracing | +0.4ms | +0.7ms | +4.5ms | -0 rps |
| 1600 rps | hooks on, untraced | +0.1ms | +0.6ms | +15.5ms | -0 rps |
| 1600 rps | active tracing | +0.8ms | +306.4ms | +670.1ms | -290 rps |
| closed loop, 128 in flight | hooks on, untraced | +0.1ms | +4.0ms | -0.5ms | -159 rps |
| closed loop, 128 in flight | active tracing | +1.2ms | +49.2ms | +68.1ms | -1145 rps |

## CPU profile: hooks off @ closed loop, 128 in flight

Paths through the module: Showing nodes accounting for 0, 0% of 25.58s total

```
File: prebid-server
Build ID: 1e6eadab6ad2488034c62172d86e2956cdf39cc4
Type: cpu
Time: 2026-09-20 22:34:23 WEST
Duration: 10.02s, Total samples = 25580ms (255.21%)
Showing nodes accounting for 9670ms, 37.80% of 25580ms total
Dropped 984 nodes (cum <= 127.90ms)
Showing top 12 nodes out of 343
      flat  flat%   sum%        cum   cum%
    3150ms 12.31% 12.31%     3150ms 12.31%  internal/runtime/syscall/linux.Syscall6
     880ms  3.44% 15.75%      910ms  3.56%  encoding/json.stateInString
     840ms  3.28% 19.04%     2080ms  8.13%  encoding/json.appendCompact
     700ms  2.74% 21.77%      700ms  2.74%  compress/flate.(*compressor).reset
     650ms  2.54% 24.32%      650ms  2.54%  aeshashbody
     620ms  2.42% 26.74%     2280ms  8.91%  compress/flate.(*compressor).deflate
     600ms  2.35% 29.09%      600ms  2.35%  runtime.memclrNoHeapPointers
     600ms  2.35% 31.43%      810ms  3.17%  runtime.tryDeferToSpanScan
     540ms  2.11% 33.54%      800ms  3.13%  runtime.scanObjectsSmall
     380ms  1.49% 35.03%      380ms  1.49%  runtime.memmove
     360ms  1.41% 36.43%      420ms  1.64%  compress/flate.(*huffmanEncoder).bitCounts
     350ms  1.37% 37.80%     2100ms  8.21%  runtime.mallocgcSmallScanNoHeader
```

## CPU profile: hooks on, untraced @ closed loop, 128 in flight

Paths through the module: Showing nodes accounting for 0.01s, 0.037% of 26.78s total

```
File: prebid-server
Build ID: 1e6eadab6ad2488034c62172d86e2956cdf39cc4
Type: cpu
Time: 2026-09-20 22:39:45 WEST
Duration: 10.01s, Total samples = 26780ms (267.54%)
Showing nodes accounting for 9670ms, 36.11% of 26780ms total
Dropped 1055 nodes (cum <= 133.90ms)
Showing top 12 nodes out of 375
      flat  flat%   sum%        cum   cum%
    2940ms 10.98% 10.98%     2940ms 10.98%  internal/runtime/syscall/linux.Syscall6
     970ms  3.62% 14.60%     1010ms  3.77%  encoding/json.stateInString
     880ms  3.29% 17.89%     1960ms  7.32%  encoding/json.appendCompact
     750ms  2.80% 20.69%      750ms  2.80%  runtime.memclrNoHeapPointers
     690ms  2.58% 23.26%      960ms  3.58%  runtime.tryDeferToSpanScan
     670ms  2.50% 25.77%      670ms  2.50%  compress/flate.(*compressor).reset
     600ms  2.24% 28.01%      600ms  2.24%  aeshashbody
     580ms  2.17% 30.17%     1920ms  7.17%  compress/flate.(*compressor).deflate
     480ms  1.79% 31.96%      850ms  3.17%  runtime.scanObjectsSmall
     400ms  1.49% 33.46%      440ms  1.64%  runtime.(*mspan).writeHeapBitsSmall
     360ms  1.34% 34.80%     3060ms 11.43%  runtime.mallocgcSmallScanNoHeader
     350ms  1.31% 36.11%      370ms  1.38%  compress/flate.(*huffmanEncoder).bitCounts
```

## CPU profile: active tracing @ closed loop, 128 in flight

Paths through the module: Showing nodes accounting for 800ms, 2.84% of 28140ms total

```
File: prebid-server
Build ID: 1e6eadab6ad2488034c62172d86e2956cdf39cc4
Type: cpu
Time: 2026-09-20 22:48:26 WEST
Duration: 10.02s, Total samples = 28140ms (280.75%)
Showing nodes accounting for 10070ms, 35.79% of 28140ms total
Dropped 1114 nodes (cum <= 140.70ms)
Showing top 12 nodes out of 350
      flat  flat%   sum%        cum   cum%
    2700ms  9.59%  9.59%     2700ms  9.59%  internal/runtime/syscall/linux.Syscall6
    1450ms  5.15% 14.75%     3050ms 10.84%  encoding/json.appendCompact
    1220ms  4.34% 19.08%     1240ms  4.41%  encoding/json.stateInString
     760ms  2.70% 21.78%      760ms  2.70%  runtime.memclrNoHeapPointers
     650ms  2.31% 24.09%      650ms  2.31%  compress/flate.(*compressor).reset
     580ms  2.06% 26.15%     1830ms  6.50%  compress/flate.(*compressor).deflate
     560ms  1.99% 28.14%      770ms  2.74%  runtime.tryDeferToSpanScan
     470ms  1.67% 29.82%      470ms  1.67%  runtime.memmove
     440ms  1.56% 31.38%     3880ms 13.79%  encoding/json.structEncoder.encode
     430ms  1.53% 32.91%     3380ms 12.01%  runtime.mallocgcSmallScanNoHeader
     410ms  1.46% 34.36%      730ms  2.59%  runtime.scanObjectsSmall
     400ms  1.42% 35.79%      400ms  1.42%  aeshashbody
```


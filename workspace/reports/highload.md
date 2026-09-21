# High-load run 2026-09-21 10:00 UTC

Docker: 4 CPUs for Prebid Server. Stub bidder delay 10ms. 3 runs of 15s per cell; medians, with min–max where it matters. Open-loop steps hold an arrival rate; a step is *sustained* when the achieved rate is ≥ 95 % of the target with no errors and no dropped arrivals. The closed loop keeps 128 requests in flight and measures throughput. CPU is Prebid Server's cgroup time; the generator and the stub run outside the VM.

## 200 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 200 | yes | 12.9ms | 14.9ms | 18.2ms (17.5ms–18.4ms) | 27.7ms | 2.502ms | 0.50 | 0s | 0 | 0 | 0 | 0 | 0 | 3 | 451 MiB |
| hooks on, untraced | 200 | yes | 13.0ms | 14.9ms | 18.3ms (18.0ms–18.5ms) | 24.2ms | 2.657ms | 0.53 | 4µs | 0 | 0 | 0 | 0 | 0 | 4 | 449 MiB |
| active tracing | 200 | yes | 13.3ms | 15.2ms | 18.8ms (18.6ms–19.0ms) | 29.6ms | 3.085ms | 0.62 | 25µs | 0 | 0 | 0 | 3000 | 0 | 5 | 450 MiB |

## 400 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 400 | yes | 12.1ms | 13.8ms | 18.1ms (17.3ms–19.7ms) | 86.6ms | 1.927ms | 0.77 | 0s | 0 | 0 | 0 | 0 | 0 | 7 | 454 MiB |
| hooks on, untraced | 400 | yes | 12.2ms | 14.5ms | 17.7ms (17.7ms–18.5ms) | 27.6ms | 2.088ms | 0.83 | 4µs | 0 | 0 | 0 | 0 | 0 | 8 | 456 MiB |
| active tracing | 400 | yes | 12.4ms | 14.9ms | 18.5ms (18.4ms–18.7ms) | 30.4ms | 2.404ms | 0.96 | 20µs | 0 | 0 | 0 | 5999 | 0 | 9 | 452 MiB |

## 800 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 799 | yes | 11.9ms | 12.5ms | 14.7ms (14.5ms–14.9ms) | 28.4ms | 1.91ms | 1.53 | 0s | 0 | 0 | 0 | 0 | 0 | 14 | 466 MiB |
| hooks on, untraced | 799 | yes | 12.1ms | 13.2ms | 17.8ms (16.7ms–18.1ms) | 34.1ms | 2.03ms | 1.62 | 4µs | 0 | 0 | 0 | 0 | 0 | 17 | 466 MiB |
| active tracing | 799 | yes | 12.7ms | 14.5ms | 24.1ms (21.8ms–24.7ms) | 48.6ms | 2.363ms | 1.89 | 23µs | 0 | 0 | 0 | 11999 | 0 | 17 | 496 MiB |

## 1600 rps

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 1598 | yes | 13.4ms | 25.2ms | 49.0ms (44.5ms–52.9ms) | 100.5ms | 1.422ms | 2.27 | 0s | 0 | 0 | 0 | 0 | 0 | 26 | 535 MiB |
| hooks on, untraced | 1599 | yes | 13.7ms | 31.9ms | 64.3ms (54.1ms–66.3ms) | 109.6ms | 1.485ms | 2.37 | 6µs | 0 | 0 | 0 | 0 | 0 | 30 | 544 MiB |
| active tracing | 1260 | no | 348.3ms | 519.4ms | 771.6ms (743.8ms–816.6ms) | 1197.9ms | 2.284ms | 2.88 | 247µs | 565 | 0 | 5318 | 11619 | 9000 | 25 | 623 MiB |

## closed loop, 128 in flight

| configuration | achieved rps | sustained | p50 | p90 | p99 (min–max) | max | CPU/auction | CPU cores | hook mean | hook bad | errors | arrivals dropped | traces | packets dropped | GC | memory |
|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 2323 | yes | 51.2ms | 83.8ms | 126.7ms (121.5ms–131.7ms) | 213.9ms | 1.112ms | 2.58 | 0s | 0 | 0 | 0 | 0 | 0 | 37 | 558 MiB |
| hooks on, untraced | 2097 | yes | 56.7ms | 92.2ms | 135.7ms (124.5ms–136.2ms) | 198.7ms | 1.285ms | 2.69 | 23µs | 2 | 0 | 0 | 0 | 0 | 40 | 558 MiB |
| active tracing | 1189 | yes | 103.2ms | 146.3ms | 209.5ms (209.1ms–227.6ms) | 346.5ms | 2.377ms | 2.83 | 85µs | 26 | 0 | 0 | 17726 | 200 | 25 | 559 MiB |

## Module cost against hooks off

| step | configuration | Δ CPU/auction | Δ p50 | Δ p99 | Δ throughput |
|---|---|---:|---:|---:|---:|
| 200 rps | hooks on, untraced | +0.2ms | +0.1ms | +0.1ms | -0 rps |
| 200 rps | active tracing | +0.6ms | +0.4ms | +0.6ms | -0 rps |
| 400 rps | hooks on, untraced | +0.2ms | +0.1ms | -0.3ms | -0 rps |
| 400 rps | active tracing | +0.5ms | +0.3ms | +0.4ms | +0 rps |
| 800 rps | hooks on, untraced | +0.1ms | +0.2ms | +3.1ms | -0 rps |
| 800 rps | active tracing | +0.5ms | +0.7ms | +9.4ms | -0 rps |
| 1600 rps | hooks on, untraced | +0.1ms | +0.4ms | +15.2ms | +0 rps |
| 1600 rps | active tracing | +0.9ms | +334.9ms | +722.6ms | -338 rps |
| closed loop, 128 in flight | hooks on, untraced | +0.2ms | +5.5ms | +9.0ms | -225 rps |
| closed loop, 128 in flight | active tracing | +1.3ms | +52.0ms | +82.8ms | -1134 rps |

## CPU profile: hooks off @ closed loop, 128 in flight

Paths through the module: Showing nodes accounting for 0, 0% of 25.76s total

```
File: prebid-server
Build ID: 5dd8674c3a2a6852376c1e56db978963084d0487
Type: cpu
Time: 2026-09-21 10:31:18 WEST
Duration: 10s, Total samples = 25760ms (257.53%)
Showing nodes accounting for 9640ms, 37.42% of 25760ms total
Dropped 966 nodes (cum <= 128.80ms)
Showing top 12 nodes out of 348
      flat  flat%   sum%        cum   cum%
    2860ms 11.10% 11.10%     2860ms 11.10%  internal/runtime/syscall/linux.Syscall6
     890ms  3.45% 14.56%      940ms  3.65%  encoding/json.stateInString
     860ms  3.34% 17.90%     2050ms  7.96%  encoding/json.appendCompact
     840ms  3.26% 21.16%      840ms  3.26%  compress/flate.(*compressor).reset
     670ms  2.60% 23.76%     2580ms 10.02%  compress/flate.(*compressor).deflate
     650ms  2.52% 26.28%      650ms  2.52%  aeshashbody
     610ms  2.37% 28.65%      610ms  2.37%  runtime.memclrNoHeapPointers
     590ms  2.29% 30.94%      810ms  3.14%  runtime.tryDeferToSpanScan
     490ms  1.90% 32.84%      870ms  3.38%  runtime.scanObjectsSmall
     460ms  1.79% 34.63%      510ms  1.98%  compress/flate.(*huffmanEncoder).bitCounts
     360ms  1.40% 36.02%     3790ms 14.71%  runtime.mallocgc
     360ms  1.40% 37.42%      360ms  1.40%  runtime.memmove
```

## CPU profile: hooks on, untraced @ closed loop, 128 in flight

Paths through the module: Showing nodes accounting for 0.02s, 0.076% of 26.46s total

```
File: prebid-server
Build ID: 5dd8674c3a2a6852376c1e56db978963084d0487
Type: cpu
Time: 2026-09-21 10:37:47 WEST
Duration: 10.01s, Total samples = 26460ms (264.35%)
Showing nodes accounting for 9440ms, 35.68% of 26460ms total
Dropped 1051 nodes (cum <= 132.30ms)
Showing top 12 nodes out of 356
      flat  flat%   sum%        cum   cum%
    2970ms 11.22% 11.22%     2970ms 11.22%  internal/runtime/syscall/linux.Syscall6
    1000ms  3.78% 15.00%     1790ms  6.76%  encoding/json.appendCompact
     760ms  2.87% 17.88%      780ms  2.95%  encoding/json.stateInString
     710ms  2.68% 20.56%      710ms  2.68%  compress/flate.(*compressor).reset
     640ms  2.42% 22.98%      860ms  3.25%  runtime.tryDeferToSpanScan
     610ms  2.31% 25.28%     2080ms  7.86%  compress/flate.(*compressor).deflate
     510ms  1.93% 27.21%      510ms  1.93%  runtime.memclrNoHeapPointers
     500ms  1.89% 29.10%      500ms  1.89%  aeshashbody
     500ms  1.89% 30.99%      800ms  3.02%  runtime.scanObjectsSmall
     460ms  1.74% 32.73%     2830ms 10.70%  runtime.mallocgcSmallScanNoHeader
     420ms  1.59% 34.32%      460ms  1.74%  compress/flate.(*huffmanEncoder).bitCounts
     360ms  1.36% 35.68%     4580ms 17.31%  runtime.mallocgc
```

## CPU profile: active tracing @ closed loop, 128 in flight

Paths through the module: Showing nodes accounting for 670ms, 2.41% of 27850ms total

```
File: prebid-server
Build ID: 5dd8674c3a2a6852376c1e56db978963084d0487
Type: cpu
Time: 2026-09-21 10:52:55 WEST
Duration: 10.03s, Total samples = 27850ms (277.56%)
Showing nodes accounting for 9460ms, 33.97% of 27850ms total
Dropped 1121 nodes (cum <= 139.25ms)
Showing top 12 nodes out of 361
      flat  flat%   sum%        cum   cum%
    2600ms  9.34%  9.34%     2600ms  9.34%  internal/runtime/syscall/linux.Syscall6
    1240ms  4.45% 13.79%     2640ms  9.48%  encoding/json.appendCompact
    1090ms  3.91% 17.70%     1110ms  3.99%  encoding/json.stateInString
     870ms  3.12% 20.83%      870ms  3.12%  runtime.memclrNoHeapPointers
     640ms  2.30% 23.12%      640ms  2.30%  runtime.memmove
     630ms  2.26% 25.39%     1940ms  6.97%  compress/flate.(*compressor).deflate
     480ms  1.72% 27.11%      480ms  1.72%  compress/flate.(*compressor).reset
     410ms  1.47% 28.58%     3180ms 11.42%  runtime.mallocgcSmallScanNoHeader
     400ms  1.44% 30.02%      400ms  1.44%  aeshashbody
     380ms  1.36% 31.38%      400ms  1.44%  compress/flate.(*huffmanEncoder).bitCounts
     380ms  1.36% 32.75%      600ms  2.15%  runtime.tryDeferToSpanScan
     340ms  1.22% 33.97%     3410ms 12.24%  encoding/json.structEncoder.encode
```


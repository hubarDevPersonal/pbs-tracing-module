# 06 — High-load report

What the module costs on a Prebid Server under load, measured, not inferred. Method and scenarios: [test-specs/load.md](test-specs/load.md)
L-10 to L-12; how to repeat: [05-runbook.md](05-runbook.md) §8. The raw output of the run this document reads is
[reports/highload.md](reports/highload.md).

## 1. Stand

| Part | Where | Notes |
|------|-------|-------|
| Prebid Server @ `f660bedc` with the module | Docker VM, 4 CPUs, 8 GiB | image built with `-tags loadbench`: three partners whose limits outlast the run |
| Configuration | [deploy/pbs.load.yaml](../deploy/pbs.load.yaml) | the perf configuration; `hooks.enabled` toggled per configuration through `PBS_HOOKS_ENABLED` |
| Bidders | stub on the host, `:18081` | one bid per impression after a 10 ms delay; the sample's four bidders point at it |
| Load generator | on the host | open loop at a fixed arrival rate, or closed loop at fixed concurrency (`test/load`) |
| Request | the assessment's sample, `ext.prebid.debug` and `trace` removed | 3.7 KB, four bidders |

Three configurations, each on its own fresh server: **hooks off** (`hooks.enabled: false`), **hooks on, untraced** (the module
runs on every stage, the account matches no rule), **active tracing** (every auction traced and written to stdout).

Steps: 200, 400, 800 and 1600 auctions/s held for 15 s each, then a closed loop with 128 requests in flight. Every cell three
times; the tables show medians and, for p99, the spread. A step is *sustained* when the achieved rate is at least 95 % of the
target with no errors and no arrival dropped by the generator.

CPU is Prebid Server's cgroup time inside the VM, so it excludes the generator and the stub. Hook metrics come from Prebid
Server's Prometheus endpoint; packets on stdout and dropped packets from the container log and the module's own counter.

## 2. Results

Run of 2026-09-21 (`make highload`, defaults). Medians of three runs; p99 spread in brackets. Full tables with p90, max, GC
and memory: [reports/highload.md](reports/highload.md).

### 2.1 Rate ladder

| step | configuration | sustained | p50 | p99 | CPU / auction | CPU cores | hook mean | hook timeouts | packets written | packets dropped |
|---|---|:---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 200/s | hooks off | yes | 12.9 ms | 18.2 ms (17.5–18.4) | 2.50 ms | 0.50 | | | | |
| 200/s | module on, untraced | yes | 13.0 ms | 18.3 ms (18.0–18.5) | 2.66 ms | 0.53 | 4 µs | 0 | 0 | 0 |
| 200/s | every auction traced | yes | 13.3 ms | 18.8 ms (18.6–19.0) | 3.09 ms | 0.62 | 25 µs | 0 | 3000 | 0 |
| 400/s | hooks off | yes | 12.1 ms | 18.1 ms (17.3–19.7) | 1.93 ms | 0.77 | | | | |
| 400/s | module on, untraced | yes | 12.2 ms | 17.7 ms (17.7–18.5) | 2.09 ms | 0.83 | 4 µs | 0 | 0 | 0 |
| 400/s | every auction traced | yes | 12.4 ms | 18.5 ms (18.4–18.7) | 2.40 ms | 0.96 | 20 µs | 0 | 5999 | 0 |
| 800/s | hooks off | yes | 11.9 ms | 14.7 ms (14.5–14.9) | 1.91 ms | 1.53 | | | | |
| 800/s | module on, untraced | yes | 12.1 ms | 17.8 ms (16.7–18.1) | 2.03 ms | 1.62 | 4 µs | 0 | 0 | 0 |
| 800/s | every auction traced | yes | 12.7 ms | 24.1 ms (21.8–24.7) | 2.36 ms | 1.89 | 23 µs | 0 | 11999 | 0 |
| 1600/s | hooks off | yes | 13.4 ms | 49.0 ms (44.5–52.9) | 1.42 ms | 2.27 | | | | |
| 1600/s | module on, untraced | yes | 13.7 ms | 64.3 ms (54.1–66.3) | 1.49 ms | 2.37 | 6 µs | 0 | 0 | 0 |
| 1600/s | every auction traced | **no**, 1260/s reached | 348 ms | 772 ms (744–817) | 2.28 ms | 2.88 | 247 µs | 565 | 11619 | 9000 |

### 2.2 Closed loop, 128 requests in flight

| configuration | throughput | p50 | p99 | CPU / auction | CPU cores | hook mean | hook timeouts | packets written | packets dropped | PBS CPU on paths through the module |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 2323/s | 51.2 ms | 127 ms (122–132) | 1.11 ms | 2.58 | | | | | 0 |
| module on, untraced | 2097/s | 56.7 ms | 136 ms (125–136) | 1.29 ms | 2.69 | 23 µs | 2 | 0 | 0 | 0.08 % |
| every auction traced | 1189/s | 103 ms | 210 ms (209–228) | 2.38 ms | 2.83 | 85 µs | 26 | 17726 | 200 | 2.4 % |

### 2.3 Module cost, difference to hooks off

| step | module on, untraced | | every auction traced | |
|---|---:|---:|---:|---:|
| | Δ CPU / auction | Δ p99 | Δ CPU / auction | Δ p99 |
| 200/s | +0.2 ms | +0.1 ms | +0.6 ms | +0.6 ms |
| 400/s | +0.2 ms | −0.3 ms | +0.5 ms | +0.4 ms |
| 800/s | +0.1 ms | +3.1 ms | +0.5 ms | +9.4 ms |
| 1600/s | +0.1 ms | +15.2 ms | +0.9 ms | +723 ms (not sustained) |
| closed loop | +0.2 ms, −10 % throughput | +9 ms | +1.3 ms, −49 % throughput | +83 ms |

The CPU profile of the closed loop (top frames in the raw report) is the same for all three configurations: syscalls, JSON
compaction and validation, gzip. No module frame appears among the top twelve.

## 3. Reading

**The module's own work is small and flat.** Hook mean is 4–6 µs when nothing is traced and 20–25 µs when every auction is,
independent of the rate up to 800/s. The CPU profile puts 0.08 % of Prebid Server's CPU on paths through the module with
nothing traced and 2.4 % with everything traced.

**The price of having the module on is PBS's hook execution, not the module.** With nothing traced the server spends
0.1–0.2 ms more CPU per auction and loses 10 % of closed-loop throughput. The profile attributes almost none of it to the module:
it is the executor's two goroutines and one timer per invocation, thirteen invocations per auction with the provided plan, plus
the copy of the request body at `entrypoint`. Any hook module pays this; the only lever is the number of stages in the plan.

**Every auction traced costs 0.5–0.6 ms of CPU per auction and about 9 ms of p99 at 800/s**, with every packet written. That is
the cost of marshalling the four bidder requests and the response and of the stdout write, and it holds up to the point where
stdout, not the server, becomes the limit.

**The limit of full tracing is the stdout path, at about 1200 packets/s on this VM.** At 1600/s the container's log driver, which
runs in the same 4-CPU VM as Prebid Server, cannot absorb 16 MB/s of packets: the queue fills, the module drops what does not
fit (9000 of 20600 in 15 s), and the server itself slows to a p50 of 348 ms because the log driver takes the CPU. The closed loop
shows the same: 1189/s against 2323/s with hooks off. None of this reaches a hook: the auction path stays non-blocking and the
drops are counted and logged. The rules bound how many auctions are traced, so this is a ceiling on the tracing rate, not on the
server; a production rule set traces a few packets per partner, not 1200 per second.

**Hook timeouts appear only at saturation.** With the 50 ms group timeout of the bench configuration there are none at any
sustained open-loop step. At 1600/s with full tracing 565 invocations timed out, in the closed loop 26 (2 with nothing traced):
under CPU starvation the executor's goroutine for a hook is not scheduled in time, whatever the hook does. The Prometheus
counter `modules_test_provider_test_tracer_timeouts` shows it happening.

What a timeout costs depends on the stage, because PBS stops waiting but does not stop the hook (analysis §3.3): the goroutine
runs to the end and its work still lands in the shared trace. A timed-out `bidder_request` or `raw_bidder_response` therefore
loses its entry only when the packet is built before it finishes, and a timed-out `exitpoint` still enqueues its packet. The
expensive stage is `entrypoint`: on a timeout the executor stores a nil module context for the module, and `moduleContexts.put`
merges later contexts into that nil entry instead of replacing it, so every later stage of that request sees no context. The
trace is then started at `processed_auction_request`, takes a slot and is never written; the slot comes back with its lease
(FR-10 AC4). With the provided plan's 120 000 ms group timeout this is unreachable; it needs a configuration as tight as the
bench's 50 ms and a saturated server.

**Memory** grows by 30 MiB with full tracing at 800/s and by 90 MiB at 1600/s, where the queue is full and packets are being
dropped: the queue of up to 64 packets, the packets being built, and the buffers of the log path. In the closed loop, 1189 traced
auctions per second with a queue that keeps up, it does not grow at all. The run of 2026-09-20, taken before the tracer stopped
holding references to finished traces (M-38), showed 130–150 MiB at 800/s. GC cycles follow the auction rate and are the same
with and without the module.

**What to take from this for deployment.** Keep `TracePacketsAmount` small enough that the traced rate stays well under the
stdout path's capacity of the host; send stdout to a file or a log driver with known throughput; watch the timeouts counter and
the drop warnings; treat 0.2 ms of CPU per auction as the standing price of the hook framework and 0.4–0.6 ms as the price of a
traced auction.

## 4. Limits of the measurement

- One host, one Docker VM: absolute numbers are this machine's. Compare configurations within the run, not across machines.
- The generator, the stub bidder and the VM share the host's cores. Above the VM's saturation the generator itself is under
  pressure; the closed loop is the reliable ceiling.
- 15 s per run and three runs per cell separate configurations whose difference is larger than the spread of p99; smaller
  differences are noise here.
- Stub bidders answer in 10 ms with a bid. Live bidders answer in 100 ms or more and mostly with 204, which moves the auction's
  cost from CPU to waiting; the module's share of CPU is then smaller than measured here.

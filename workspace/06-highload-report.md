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

Run of 2026-09-20 (`make highload`, defaults). Medians of three runs; p99 spread in brackets. Full tables with p90, max, GC
and memory: [reports/highload.md](reports/highload.md).

### 2.1 Rate ladder

| step | configuration | sustained | p50 | p99 | CPU / auction | CPU cores | hook mean | hook timeouts | packets written | packets dropped |
|---|---|:---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 200/s | hooks off | yes | 12.7 ms | 16.7 ms (16.5–17.7) | 2.29 ms | 0.46 | | | | |
| 200/s | module on, untraced | yes | 12.8 ms | 17.2 ms (16.9–17.6) | 2.52 ms | 0.50 | 4 µs | 0 | 0 | 0 |
| 200/s | every auction traced | yes | 12.8 ms | 17.4 ms (17.3–41.5) | 2.78 ms | 0.56 | 21 µs | 0 | 2999 | 0 |
| 400/s | hooks off | yes | 11.8 ms | 15.7 ms (14.7–16.1) | 1.79 ms | 0.71 | | | | |
| 400/s | module on, untraced | yes | 12.0 ms | 15.7 ms (15.3–16.1) | 2.00 ms | 0.80 | 3 µs | 0 | 0 | 0 |
| 400/s | every auction traced | yes | 12.2 ms | 16.3 ms (15.6–16.4) | 2.37 ms | 0.95 | 18 µs | 0 | 6000 | 0 |
| 800/s | hooks off | yes | 11.9 ms | 15.5 ms (15.0–15.7) | 1.79 ms | 1.43 | | | | |
| 800/s | module on, untraced | yes | 12.2 ms | 18.3 ms (17.3–146.3) | 1.94 ms | 1.55 | 4 µs | 0 | 0 | 0 |
| 800/s | every auction traced | yes | 12.6 ms | 20.0 ms (19.6–20.7) | 2.20 ms | 1.76 | 21 µs | 0 | 12000 | 0 |
| 1600/s | hooks off | yes | 12.9 ms | 39.2 ms (31.7–43.0) | 1.41 ms | 2.26 | | | | |
| 1600/s | module on, untraced | yes | 13.5 ms | 54.7 ms (50.5–59.6) | 1.49 ms | 2.38 | 5 µs | 0 | 0 | 0 |
| 1600/s | every auction traced | **no**, 1308/s reached | 319 ms | 709 ms (654–876) | 2.20 ms | 2.88 | 240 µs | 604 | 12983 | 7800 |

### 2.2 Closed loop, 128 requests in flight

| configuration | throughput | p50 | p99 | CPU / auction | CPU cores | hook mean | hook timeouts | packets written | packets dropped | PBS CPU on paths through the module |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| hooks off | 2389/s | 50.0 ms | 121 ms (112–124) | 1.08 ms | 2.58 | | | | | 0 |
| module on, untraced | 2230/s | 54.1 ms | 121 ms (117–122) | 1.22 ms | 2.71 | 20 µs | 1 | 0 | 0 | 0.04 % |
| every auction traced | 1244/s | 99.2 ms | 189 ms (188–197) | 2.28 ms | 2.83 | 80 µs | 76 | 18571 | 200 | 2.8 % |

### 2.3 Module cost, difference to hooks off

| step | module on, untraced | | every auction traced | |
|---|---:|---:|---:|---:|
| | Δ CPU / auction | Δ p99 | Δ CPU / auction | Δ p99 |
| 200/s | +0.2 ms | +0.5 ms | +0.5 ms | +0.8 ms |
| 400/s | +0.2 ms | 0 | +0.6 ms | +0.5 ms |
| 800/s | +0.2 ms | +2.9 ms | +0.4 ms | +4.5 ms |
| 1600/s | +0.1 ms | +15.5 ms | +0.8 ms | +670 ms (not sustained) |
| closed loop | +0.1 ms, −7 % throughput | −0.5 ms | +1.2 ms, −48 % throughput | +68 ms |

The CPU profile of the closed loop (top frames in the raw report) is the same for all three configurations: syscalls, JSON
compaction and validation, gzip. No module frame appears among the top twelve.

## 3. Reading

**The module's own work is small and flat.** Hook mean is 3–5 µs when nothing is traced and 18–21 µs when every auction is,
independent of the rate up to 800/s. The CPU profile puts 0.04 % of Prebid Server's CPU on paths through the module with
nothing traced and 2.8 % with everything traced.

**The price of having the module on is PBS's hook execution, not the module.** With nothing traced the server spends
0.1–0.2 ms more CPU per auction and loses 7 % of closed-loop throughput. The profile attributes almost none of it to the module:
it is the executor's two goroutines and one timer per invocation, thirteen invocations per auction with the provided plan, plus
the copy of the request body at `entrypoint`. Any hook module pays this; the only lever is the number of stages in the plan.

**Every auction traced costs 0.4–0.6 ms of CPU per auction and about 4 ms of p99 at 800/s**, with every packet written. That is
the cost of marshalling the four bidder requests and the response and of the stdout write, and it holds up to the point where
stdout, not the server, becomes the limit.

**The limit of full tracing is the stdout path, at about 1300 packets/s on this VM.** At 1600/s the container's log driver, which
runs in the same 4-CPU VM as Prebid Server, cannot absorb 16 MB/s of packets: the queue fills, the module drops what does not
fit (7800 of 21000 in 15 s), and the server itself slows to a p50 of 319 ms because the log driver takes the CPU. The closed loop
shows the same: 1244/s against 2389/s with hooks off. None of this reaches a hook: the auction path stays non-blocking and the
drops are counted and logged. The rules bound how many auctions are traced, so this is a ceiling on the tracing rate, not on the
server; a production rule set traces a few packets per partner, not 1300 per second.

**Hook timeouts appear only at saturation.** With the 50 ms group timeout of the bench configuration there are none at any
sustained open-loop step. At 1600/s with full tracing 604 invocations timed out, in the closed loop 76 (1 with nothing traced):
under CPU starvation the executor's goroutine for a hook is not scheduled in time, whatever the hook does. A timed-out
`bidder_request` or `raw_bidder_response` loses that entry of the packet; a timed-out `exitpoint` loses the packet. The
Prometheus counter `modules_test_provider_test_tracer_timeouts` shows it happening.

**Memory** grows by 130–150 MiB with full tracing at 800/s: the queue of up to 64 packets, the packets being built, and the
buffers of the log path. It does not grow with the rate beyond that. GC cycles follow the auction rate and are the same with and
without the module.

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

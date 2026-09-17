# 06 — Performance and load

Scope: the Prebid Server (PBS) process that hosts `test_provider.test_tracer`. Three concerns raised for this phase:
DNS caching, HTTP client management, and tracking CPU-heavy operations. Facts reference PBS master `f660bedc` (the pinned build).

## 1. Where a request spends its time

From the live Docker run (`docs/05-runbook.md`, trace timestamps of one traced auction):

| Segment | Observed | Notes |
|---------|----------|-------|
| entrypoint → processed_auction_request | ~3 ms | request read, stored-request lookup, account resolution, enrichment |
| processed_auction_request → bidder_request (×4) | ~4 ms | request split per bidder, privacy scrubbing |
| bidder HTTP calls (parallel) | **~140–350 ms** | network round trip to `ib.adnxs.com`, `pbs.amxrtb.com`, `bl-us.aceex.io`, `hb-ss.omnitagjs.com`; each answered 204 |
| auction_response → exitpoint → encode | ~1 ms | response assembly, debug enrichment, JSON encode |
| module hooks (all seven, traced) | ~0.14 ms CPU | see §5 |

The process is I/O-bound on bidder calls; CPU per request is dominated by JSON (request parse, per-bidder marshal, response
encode) and, with `compression.*.enable_gzip: true`, by gzip. Everything below targets (a) not paying for new connections
and DNS on the hot path, (b) not letting slow bidders hold goroutines and memory, and (c) seeing where CPU actually goes.

## 2. HTTP client management

PBS builds one `http.Transport` for bidder traffic in `router/router.go` from the `http_client` section (`config.HTTPClient`),
using Go's default `net.Dialer` (no DNS cache, see §3). Knobs, defaults from `config/config.go`, and the values in
[deploy/pbs.perf.yaml](../deploy/pbs.perf.yaml):

| Key | Default | Tuned | Why |
|-----|---------|-------|-----|
| `http_client.max_idle_connections` | 400 | 1024 | pool across all bidder hosts; below the concurrency × bidders product the pool thrashes |
| `http_client.max_idle_connections_per_host` | 10 | 64 | the important one: a burst above 10 concurrent calls to one bidder opens new connections → new TCP + TLS handshake **+ DNS lookup** each |
| `http_client.max_connections_per_host` | 0 (unlimited) | 0 | cap only to protect a specific bidder; otherwise it turns into queueing inside the transport |
| `http_client.idle_connection_timeout_seconds` | 60 | 90 | keep warm connections through traffic gaps |
| `http_client.dialer.timeout_seconds` | 30 | 5 | a bidder that cannot be dialed in 5 s should fail fast, not hold a goroutine for 30 s |
| `http_client.dialer.keep_alive_seconds` | 15 | 30 | TCP keep-alive probe interval on pooled connections |
| `http_client.tls_handshake_timeout_seconds` | 10 | 5 | same reasoning as the dial timeout |
| `http_client.throttle.enable_throttling` | false | true | adaptive per-bidder throttling on transport queue wait (`exchange/bidder.go`); a bidder whose connections queue longer than `long_queue_wait_threshold_ms` gets a share of requests skipped with `BidderThrottled` |
| `http_client.throttle.simulate_throttling_only` | false | **true** | observe first: PBS logs what it *would* throttle; flip to `false` after reading the logs under real traffic |
| `auction_timeouts_ms.default` / `max` | 0 / 0 (request `tmax` as-is) | 1000 / 3000 | the assessment config allows 10 minutes; under load a slow bidder must not pin a request; `tmax` in the request is clamped to `max` |
| `tmax_adjustments.*` | disabled | disabled | enable when PBS-side overhead is measurable relative to `tmax` (it subtracts PBS processing time from the bidder deadline) |

Verification signals: Prometheus counters `adapter_connection_created` vs `adapter_connection_reused` per bidder — the reuse
ratio is the direct measure of pool health — plus the dial histograms `dns_lookup_time` and `tls_handshake_time` (enabled by
`metrics.disabled_metrics.adapter_connections_dial_metrics: false`) and `adapter_request_time_seconds`.

Also relevant: `compression.request/response.enable_gzip` (CPU for every request/response; keep it on only if the client sends
gzip and bandwidth matters), `max_request_size` (default 256 KiB, bounds parse cost), and the separate `http_client_cache`
transport for Prebid Cache which follows the same keys.

## 3. DNS caching

Go's resolver (`net.Resolver`, pure-Go mode inside containers) performs a lookup on **every new connection** and caches
nothing; the only cache in the path is whatever `/etc/resolv.conf` points at. PBS has no DNS-cache setting and uses the
stock dialer (`router.go: defaultTransportDialContext(&net.Dialer{...})`). Two complementary measures:

1. **Reduce lookups**: the pool settings in §2 (`max_idle_connections_per_host`, idle timeout) make new dials rare under
   steady traffic, so DNS is paid only on cold start, pool exhaustion and idle expiry.
2. **Cache what remains**: run a caching resolver next to PBS. The `perf` profile of `docker-compose.yml` adds a CoreDNS
   sidecar ([deploy/coredns/Corefile](../deploy/coredns/Corefile)) with `cache 300` (success TTL cap 300 s, negative 60 s,
   `prefetch` of popular names before expiry) forwarding to 1.1.1.1/8.8.8.8, and points the PBS container's `dns:` at it.
   Cache effectiveness is visible in `coredns_cache_hits_total` / `coredns_cache_misses_total` on `:9153`. The same pattern
   applies in Kubernetes with NodeLocal DNSCache and on bare hosts with `systemd-resolved` (`Cache=yes`) or `dnsmasq`.

An in-process alternative is a caching `DialContext` (e.g. `rs/dnscache` wrapped around the dialer in `router.go`). It is a
PBS-core patch, not something a hook module can do, and it duplicates what the OS-level cache gives; documented here as the
option if a sidecar is not acceptable in the target platform.

## 4. Tracking CPU-heavy operations

- **pprof** is already wired: `router/admin.go` registers `/debug/pprof/{profile,trace,cmdline,symbol}` on the admin
  listener (`admin_port`, default 6060, `admin.enabled: true`). `scripts/profile.sh 30` captures a 30 s CPU profile and prints
  the top frames; `go tool pprof -http=:8081 cpu.pprof` for the flame graph. The perf profile publishes `:6060`.
- **Prometheus** (`metrics.prometheus.port: 9100` in the tuned config): request/adapter histograms
  (`request_time_seconds`, `adapter_request_time_seconds`), connection counters (§2), and per-module series
  `modules_test_provider_test_tracer_{duration,called,failed,success_noops,…}` per stage — the module's own CPU cost is
  observable in production without instrumentation changes.
- **Go runtime**: `garbage_collector_threshold` (bytes of ballast, default 0) lowers GC frequency at steady RSS cost; set
  to 128 MiB in the tuned config. `GOMAXPROCS` should match the container CPU limit (Go ≥ 1.25 reads cgroup limits itself).
- **Module-level**: `make bench` (§5). Non-traced requests must stay near zero cost; the numbers below are the regression baseline.

## 5. Module overhead (benchmarks)

`go test ./internal/testtracer -bench . -benchmem`, Apple M-series, Go 1.26, sample request (3.7 KB), four bidders with responses, emitter → `io.Discard`:

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| `TracedAuction_4Bidders` (all seven hooks, packet emitted) | ~134 000 | ~44 000 | 134 |
| `UntracedAuction_4Bidders` (account matches no rule) | ~3 000 | ~9 700 | 16 |
| `EmitPacket` (marshal + write) | ~2 450 | ~1 470 | 12 |

A traced auction costs ~0.13 ms of CPU, i.e. < 0.1 % of a 150 ms auction; the untraced path is ~3 µs, of which most is the
`entrypoint` body copy (3.7 KB) that has to happen before the account is known. The trace budget is bounded by the rules
(`TracePacketsAmount`), so the steady-state cost of the module on a production instance is the untraced path.

Optimisations kept in reserve (not needed at current numbers): skip the entrypoint copy when no rule can match the endpoint,
marshal bidder requests lazily at `exitpoint` from a retained pointer (rejected in D10 because the exchange mutates the objects),
and a `sync.Pool` for packet buffers.

## 6. Measured load run

Produced by `make perf` (`scripts/perf-docker.sh`): tuned config, CoreDNS sidecar, `cmd/loadgen` at a modest rate against
live bidders, 30 s CPU profile captured concurrently, Prometheus/CoreDNS counters diffed before/after.

Why 5 rps × 30 s (defaults in `cmd/loadgen/config.go`): the harness drives live third-party bidders, so the rate is a courtesy
limit (5 × 4 bidders ≈ 20 outbound req/s), not a capacity probe; 150 samples make p50/p90 stable while p99 is indicative only.
Capacity testing of PBS itself belongs on stored bid responses or a mock bidder, where `-rps` can be raised freely;
`-concurrency` must stay ≥ rps × worst-case latency to hold the rate, and is unrelated to `GOMAXPROCS` (workers park on I/O).

Run of 2026-09-16 (`make perf`, `NO_BUILD=1`, Docker Desktop on Apple M-series, egress Portugal): 5 rps × 30 s, concurrency 16,
150 requests, 4 live bidders each, 30 s CPU profile captured concurrently.

| Metric | Value |
|--------|-------|
| HTTP | 150 × 200, 0 errors, 0 responses with bids (all bidders 204) |
| Latency | p50 163 ms · p90 272 ms · p95 429 ms · p99 795 ms · max 865 ms — bidder network time |
| Achieved rate | 5.0 rps (no ticks dropped) |
| Adapter connections during the run | created **7** (2 aceex, 2 amx, 2 appnexus, 1 adyoulike), reused **605** → 98.9 % reuse |
| Transport queue wait (`adapter_connection_wait`) | aceex 3.5 ms avg, adyoulike 1.3 ms, amx 0.8 ms, appnexus 0.8 ms per call |
| CoreDNS during the run | 10 queries, **10 cache hits, 0 forwarded** — every lookup after warm-up served locally |
| CPU | 2.70 s of samples over 30 s ≈ 9 % of one core ≈ 18 ms CPU per auction |
| Module hooks (Prometheus `modules_test_provider_test_tracer_duration`) | entrypoint 61 µs · processed_auction_request 69 µs · bidder_request 29 µs (×4) · auction_response 87 µs · exitpoint 59 µs — ≈ 0.4 ms per auction incl. the three traced packets |
| Trace packets emitted | 3 (rule `TracePacketsAmount: 3`), then zero cost beyond the untraced path |

CPU profile, top cumulative frames: syscalls/futex (network I/O, goroutine parking) ≈ 15 %, `encoding/json` compaction and
validation ≈ 15 % (debug `httpcalls` payloads and the module's snapshots), `json-iterator` request decode/encode ≈ 5 %,
`compress/flate` ≈ 6 % (response gzip; `compression.response.enable_gzip: true` in the assessment config). No single
hotspot: at this load PBS is idle waiting for bidders, and the remaining CPU is JSON plumbing.

Reading of the run: the pool and dialer settings keep connection creation at cold-start levels only (7 new connections for
605 calls), so DNS is effectively taken off the hot path and what remains is fully served by the sidecar cache. With
`debug: true` on every request the response payload (~15 KB with `ext.debug.httpcalls`) and its gzip dominate PBS-side CPU;
production traffic without the debug flag will sit well below this. (`deploy/pbs.perf.yaml` has since set
`account_defaults.debug_allow: false`, so later runs of `make perf` measure the non-debug path; the assessment `pbs.yaml` is unchanged.)

Re-run of 2026-09-17 after the review fixes (debug off, dial metrics with their real names): 149 × 200, p50 137 ms, p99 765 ms,
response bytes 431 KB total vs 2.3 MB with debug; adapter connections 595 reused / 1 created; `dns_lookup_time_count` 3,
`tls_handshake_time_count` 0 (all four sample bidders are dialed over plain HTTP or reuse TLS sessions); CoreDNS 6/6 cache hits.

## 7. Recommendations (in order)

1. Ship the `http_client` pool/dialer values from `deploy/pbs.perf.yaml`; watch `adapter_connection_reused / created` per bidder.
2. Clamp auction timeouts (`auction_timeouts_ms.max`) to what the integration can tolerate; the assessment values are debug-only.
3. Put a caching resolver in the pod/host; confirm with CoreDNS cache metrics that lookups are served locally.
4. Keep bidder throttling in `simulate_throttling_only: true` for a few days of real traffic, then enable it.
5. Scrape `:9100` and keep `:6060` reachable from the ops network only (the compose files publish both on `127.0.0.1`; pprof is
   unauthenticated and the admin server has no write timeout, so an open port lets anyone run unbounded profiles); profile on
   demand with `scripts/profile.sh`.
6. Track `make bench` in CI as the module's regression baseline (numbers above ±20 %).

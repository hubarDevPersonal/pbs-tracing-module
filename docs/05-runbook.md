# 05 — Runbook

## 1. Prerequisites

| Item | Version used | Notes |
|------|--------------|-------|
| Go | 1.26.2 (PBS declares `go 1.25`) | `go version` |
| Prebid Server source | master `f660bedc` or any v4 tag ≥ the hooks framework with `exitpoint` | `git clone --depth 1 https://github.com/prebid/prebid-server.git` |
| `curl`, `python3` or `jq` | any | for sending and inspecting |
| Free ports | 8080 (PBS), 6060 (PBS admin) | |

## 1a. Quickest path: Docker

```bash
make docker-build                       # PBS @ pinned commit + module; module tests run inside the build
docker run --rm -p 8080:8080 pbs-tracer:local 2>pbs.log | tee trace.ndjson
sh 02-send-bid-request.sh               # in another terminal; repeat > TracePacketsAmount times
```

Each traced auction appears as one JSON line on the container's stdout. `make e2e` does all of this and verifies the result (§5).

## 2. Install the module into a PBS checkout

```bash
export PBS_DIR=$HOME/Dev/prebid-server        # your checkout
scripts/install-module.sh                     # copies modules/test_provider/test_tracer to the same path in PBS_DIR, go generate
cd "$PBS_DIR" && go vet ./modules/test_provider/... && go build -o prebid-server .
```

`go generate` rewrites `modules/builder.go`; verify it now contains `"test_provider": {"test_tracer": ...}`.

## 3. Run with the provided configuration

PBS reads `pbs.yaml` from the working directory (viper: `SetConfigName("pbs")`, paths `.` and `/etc/config`) and bidder info from `./static/bidder-info`,
so run from the PBS root with the assessment's file copied next to the binary:

```bash
cp /path/to/code-PBS-specific/pbs.yaml "$PBS_DIR/pbs.yaml"
cd "$PBS_DIR" && ./prebid-server -stderrthreshold=INFO 2>pbs.log 1>trace.ndjson
```

stdout carries **only** the trace lines; PBS's own logging goes to stderr.

For Docker see §1a; the image already contains `pbs.yaml`.

## 4. Send the sample request

```bash
sh 02-send-bid-request.sh
```

Expected after the first request: one line in `trace.ndjson`, e.g. `python3 -c 'import json;[print(json.loads(l)["partner_id"], json.loads(l)["packet_index"], [b["bidder"] for b in json.loads(l)["bidder_requests"]]) for l in open("trace.ndjson")]'`
prints `664-025-677-881 1 ['aceex', 'adyoulike', 'amx', 'appnexus']` (order may vary).

Repeat the request; lines stop appearing once `TracePacketsAmount` for `664-025-677-881` is reached or `Duration` has elapsed since the first traced request.

## 5. What to expect from live bidders

On 2026-09-16 all four bidders answered HTTP 204 for the sample (egress: Portugal), also when called directly with request variants.
PBS then does not invoke `raw_bidder_response`, so `bidder_responses` is `[]` and `final_response.body.seatbid` is absent. This is PBS
behaviour, not a module defect; items 1, 2 and 4 are still produced. Item 3 is proven by the unit and integration tests and shows up
in e2e as soon as any bidder actually bids (e.g., from a network where appnexus test mode returns a creative).

To see item 3 populated live, send [testdata/bid-request-live-bid.json](../testdata/bid-request-live-bid.json): the sample plus
onetag's documented test publisher (`pubId 386276e072`, returns a $2.00 test creative), resolved to the second rule
(`33415-10498`, one packet):

```bash
curl -s -H 'Content-Type: application/json' --data @testdata/bid-request-live-bid.json http://localhost:8080/openrtb2/auction >/dev/null
tail -1 trace.ndjson | python3 -c 'import json,sys; p=json.load(sys.stdin); print([(b["bidder"], len(b["response"]["bids"])) for b in p["bidder_responses"]])'
# → [('onetag', 1)]
```

Automated live run: `make e2e` builds the image and runs the end-to-end suite. Phase A is the assessment request verbatim, phase B
is the live-bid request with item 3 asserted strictly. Scenarios: [test-specs/e2e.md](test-specs/e2e.md).

## 6. Verifying hook execution from the HTTP response

The sample has `ext.prebid.debug: true` and `trace: "verbose"`, so the response contains `ext.prebid.modules.trace.stages[*]` with entries
for `test_provider.test_tracer`; each invocation should show `"status": "success"` and `"action": "no_action"`.

## 7. Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `pbs.log`: `Not found hook while building hook execution plan: test_provider.test_tracer …` on every request | module not compiled in (`builder.go` not regenerated) or `hooks.modules.test_provider.test_tracer.enabled` false | rerun `go generate ./modules/...`, rebuild, check config |
| PBS exits with `failed to init "test_provider.test_tracer" module: …` | hardcoded rules failed validation | fix `modules/test_provider/test_tracer/rules.go` (empty PartnerID, non-positive Duration/amount, duplicate PartnerID) |
| No trace line although account matches | partner already stopped (amount/duration) or process restarted mid-window | restart PBS to reset state; check `Duration` in `rules.go` |
| Trace printed but `bidder_responses: []` | bidders returned 204/error → PBS skipped `raw_bidder_response` | expected for the sample's bidders; use the live-bid request (§5) |
| `ext.errors.prebid`: `Error sending the request to Prebid Cache: Post "///cache"` | the sample asks for bid caching (`ext.prebid.cache`) and `pbs.yaml` configures no cache host | harmless for tracing; set `cache.host` or drop `ext.prebid.cache` |
| stdout mixed with logs | logs not redirected | run with `2>pbs.log` |
| Port 8080 busy | another PBS/service | `lsof -iTCP:8080 -sTCP:LISTEN` |
| Partner stopped with fewer packets than `TracePacketsAmount` | an auction failed with 4xx/5xx after the trace started; PBS skips `exitpoint` on that path, the slot is consumed (analysis §5.6) | restart PBS to reset; check `pbs.log` for `Critical error while running the auction` |
| PBS exits after a traced auction when stdout is a pipe | reader of the pipe exited → `EPIPE` on fd 1 terminates the process (analysis §5.8) | redirect stdout to a file or use a log driver |
| `:6060` / `:9100` not reachable from another host | published on `127.0.0.1` only on purpose: pprof and metrics are unauthenticated | use an SSH tunnel or an authenticated reverse proxy |

## 8. Load and the perf profile

The load suite holds a constant auction rate against a running PBS and fails on any transport error, non-2xx status or dropped
arrival (scenario L-04 in [test-specs/load.md](test-specs/load.md)). It calls live bidders, so keep the rate modest.

```bash
docker run --rm -d --name pbs -p 8080:8080 pbs-tracer:local
make load                                                   # 5 auctions/s, 30 s per scenario, against PBS_URL
go test -tags load -count=1 -v ./test/load -args -pbs-url http://localhost:8080 -rps 10 -duration 60s -concurrency 32
```

`-concurrency` must stay at or above rate × worst-case latency, otherwise arrivals are dropped and the run fails.

`make perf` runs the same suite against the `perf` profile of `docker-compose.yml`: [deploy/pbs.perf.yaml](../deploy/pbs.perf.yaml)
(HTTP client pools and dial timeouts, clamped auction timeouts, simulated bidder throttling, Prometheus on `:9100`) and a caching
CoreDNS sidecar ([deploy/coredns/Corefile](../deploy/coredns/Corefile), metrics on `:9153`). While the load runs it captures a CPU
profile from the admin port and diffs the Prometheus and CoreDNS counters. The report lands in `$WORK/perf-report.md`.

| Port | What | Exposure |
|------|------|----------|
| 6060 | PBS admin: `/debug/pprof`, `/version` | loopback only; unauthenticated |
| 9100 | PBS Prometheus metrics, perf profile | loopback only |
| 9153 | CoreDNS metrics, perf profile | loopback only |

`scripts/profile.sh 30` takes a 30 s CPU profile from any running PBS and prints the top frames; `go tool pprof -http=:8081 <file>`
opens the flame graph.

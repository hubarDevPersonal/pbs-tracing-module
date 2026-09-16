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
make docker-build                       # builds PBS @ pinned commit + module; runs the module tests inside the build
docker run --rm -p 8080:8080 pbs-tracer:local 2>pbs.log | tee trace.ndjson
sh 02-send-bid-request.sh               # in another terminal; repeat > TracePacketsAmount times
```

Each traced auction appears as one JSON line on the container's stdout. `make docker-e2e` does all of this and asserts the result.

## 2. Install the module into a PBS checkout

```bash
export PBS_DIR=$HOME/Dev/prebid-server        # your checkout
cp -R modules/test_provider "$PBS_DIR/modules/"
cd "$PBS_DIR" && go generate ./modules/... && go vet ./modules/test_provider/... && go build -o prebid-server .
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

Automated live run: `PBS_DIR=... e2e/run.sh` (add `STRICT_BIDS=1` to require at least one live bidder response).

## 6. Verifying hook execution from the HTTP response

The sample has `ext.prebid.debug: true` and `trace: "verbose"`, so the response contains `ext.prebid.modules.trace.stages[*]` with entries
for `test_provider.test_tracer`; each invocation should show `"status": "success"` and `"action": "no_action"`.

## 7. Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `pbs.log`: `Not found hook while building hook execution plan: test_provider.test_tracer …` on every request | module not compiled in (`builder.go` not regenerated) or `hooks.modules.test_provider.test_tracer.enabled` false | rerun `go generate ./modules/...`, rebuild, check config |
| PBS exits with `failed to init "test_provider.test_tracer" module: …` | hardcoded rules failed validation | fix `rules.go` (empty PartnerID, non-positive Duration/amount, duplicate PartnerID) |
| No trace line although account matches | partner already stopped (amount/duration) or process restarted mid-window | restart PBS to reset state; check `Duration` in `rules.go` |
| Trace printed but `bidder_responses: []` | bidders returned 204/error → PBS skipped `raw_bidder_response` | expected with live no-bid; see §5 |
| stdout mixed with logs | logs not redirected | run with `2>pbs.log` |
| Port 8080 busy | another PBS/service | `lsof -iTCP:8080 -sTCP:LISTEN` |

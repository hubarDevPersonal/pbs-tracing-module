#!/usr/bin/env bash
# Live end-to-end check against a local Prebid Server checkout (no Docker, no mocks).
# Uses the assessment's pbs.yaml and 02-send-bid-request.sh unchanged; assertions via cmd/tracecheck.
#
#   PBS_DIR=~/Dev/prebid-server scripts/e2e-live.sh
#   PBS_DIR=... REQUESTS=6 EXPECT_PACKETS=3 STRICT_BIDS=1 scripts/e2e-live.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PBS_DIR="${PBS_DIR:?set PBS_DIR to a prebid-server checkout}"
REQUESTS="${REQUESTS:-5}"
EXPECT_PACKETS="${EXPECT_PACKETS:-3}"   # TracePacketsAmount of the sample partner in internal/testtracer/rules.go
STRICT_BIDS="${STRICT_BIDS:-0}"
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/pbs-e2e.XXXXXX")}"; mkdir -p "$WORK"

log()  { printf '\033[1;34m[e2e-live]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[e2e-live] FAIL:\033[0m %s\n' "$*"; exit 1; }
cleanup() { [[ -n "${PBS_PID:-}" ]] && kill "$PBS_PID" 2>/dev/null || true; }
trap cleanup EXIT

log "1/5 install module into $PBS_DIR, vet, test, build"
PBS_DIR="$PBS_DIR" "$ROOT/scripts/install-module.sh"
( cd "$PBS_DIR" && go vet ./modules/test_provider/... && go test -count=1 ./modules/test_provider/... && go build -o "$WORK/prebid-server" . ) || fail "build/test failed"
( cd "$ROOT" && go build -o "$WORK/tracecheck" ./cmd/tracecheck ) || fail "tracecheck build failed"

log "2/5 start Prebid Server with the provided pbs.yaml (stdout → trace, stderr → log)"
cp "$ROOT/pbs.yaml" "$PBS_DIR/pbs.yaml"
( cd "$PBS_DIR" && "$WORK/prebid-server" -stderrthreshold=INFO >"$WORK/trace.ndjson" 2>"$WORK/pbs.log" & echo $! >"$WORK/pbs.pid" )
PBS_PID="$(cat "$WORK/pbs.pid")"
for _ in $(seq 1 60); do curl -sf -o /dev/null http://localhost:8080/status && break; sleep 0.5; done
curl -sf -o /dev/null http://localhost:8080/status || fail "PBS did not come up (see $WORK/pbs.log)"

log "3/5 run 02-send-bid-request.sh $REQUESTS times + one unknown-account request"
for i in $(seq 1 "$REQUESTS"); do
  bash "$ROOT/02-send-bid-request.sh" -s >"$WORK/resp-$i.json" 2>/dev/null
done
sed 's/"664-025-677-881"/"not-a-partner"/; s/"33415-10498"/"not-a-partner"/' "$ROOT/01-bid-request-example.json" >"$WORK/unknown.json"
curl -s -o "$WORK/resp-unknown.json" -H 'Content-Type: application/json' --data @"$WORK/unknown.json" http://localhost:8080/openrtb2/auction
sleep 1

log "4/5 stop PBS"
kill "$PBS_PID"; wait "$PBS_PID" 2>/dev/null || true; PBS_PID=""

log "5/5 verify"
strict=(); [[ "$STRICT_BIDS" == "1" ]] && strict=(-strict-bids)
"$WORK/tracecheck" -expect "$EXPECT_PACKETS" ${strict[@]+"${strict[@]}"} -responses "$WORK/resp-[0-9]*.json" -pbs-log "$WORK/pbs.log" "$WORK/trace.ndjson" || fail "see $WORK"
log "done — artefacts in $WORK"

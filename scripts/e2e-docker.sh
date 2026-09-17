#!/usr/bin/env bash
# Dockerised end-to-end check: build the image (PBS @ pinned commit + module), run it, fire the
# assessment's own curl, verify the trace on the container's stdout with cmd/tracecheck. Live bidders, no mocks.
#
# Phase A — the assessment request verbatim (partner 664-025-677-881, 3 packets; item 3 soft because the
#           sample's bidders answer 204 from most networks).
# Phase B — testdata/bid-request-live-bid.json: the same request plus onetag's documented test publisher,
#           resolved to partner 33415-10498 (rule: 1 packet). onetag returns a real test bid, so item 3
#           (raw_bidder_response) is asserted strictly here.
#
#   scripts/e2e-docker.sh                       # builds pbs-tracer:local
#   IMAGE=... REQUESTS=6 EXPECT_PACKETS=3 STRICT_BIDS=0 NO_BUILD=1 scripts/e2e-docker.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${IMAGE:-pbs-tracer:local}"
REQUESTS="${REQUESTS:-5}"
EXPECT_PACKETS="${EXPECT_PACKETS:-3}"   # TracePacketsAmount of the sample partner in internal/testtracer/rules.go
STRICT_BIDS="${STRICT_BIDS:-0}"
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/pbs-e2e-docker.XXXXXX")}"; mkdir -p "$WORK"
NAME="pbs-tracer-e2e-$$"

log()  { printf '\033[1;34m[e2e-docker]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[e2e-docker] FAIL:\033[0m %s\n' "$*"; exit 1; }
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

if [[ "${NO_BUILD:-0}" != "1" ]]; then
  log "1/7 docker build $IMAGE (module tests run inside the build)"
  docker build -t "$IMAGE" "$ROOT" || fail "docker build failed"
else
  log "1/7 skipping build (NO_BUILD=1)"
fi
( cd "$ROOT" && go build -o "$WORK/tracecheck" ./cmd/tracecheck ) || fail "tracecheck build failed"

log "2/7 start container"
docker run -d --name "$NAME" -p 8080:8080 "$IMAGE" >/dev/null
for _ in $(seq 1 60); do [[ "$(docker inspect -f '{{.State.Health.Status}}' "$NAME" 2>/dev/null)" == "healthy" ]] && break; sleep 1; done
[[ "$(docker inspect -f '{{.State.Health.Status}}' "$NAME")" == "healthy" ]] || { docker logs "$NAME" 2>&1 | tail -30; fail "container did not become healthy"; }

log "3/7 phase A: run 02-send-bid-request.sh $REQUESTS times + one unknown-account request"
for i in $(seq 1 "$REQUESTS"); do
  bash "$ROOT/02-send-bid-request.sh" -s >"$WORK/resp-$i.json" 2>/dev/null   # the assessment's own script, unchanged
done
sed 's/"664-025-677-881"/"not-a-partner"/; s/"33415-10498"/"not-a-partner"/' "$ROOT/01-bid-request-example.json" >"$WORK/unknown.json"
curl -s -o "$WORK/resp-unknown.json" -H 'Content-Type: application/json' --data @"$WORK/unknown.json" http://localhost:8080/openrtb2/auction
sleep 1

log "4/7 collect container stdout (trace) and stderr (PBS log)"
docker logs "$NAME" >"$WORK/trace.ndjson" 2>"$WORK/pbs.log"

log "5/7 verify phase A"
strict=(); [[ "$STRICT_BIDS" == "1" ]] && strict=(-strict-bids)
"$WORK/tracecheck" -expect "$EXPECT_PACKETS" ${strict[@]+"${strict[@]}"} -responses "$WORK/resp-[0-9]*.json" -pbs-log "$WORK/pbs.log" "$WORK/trace.ndjson" || fail "phase A: see $WORK"

log "6/7 phase B: live-bid request (onetag test publisher, partner 33415-10498) ×2 — second one must not be traced"
phase_a_lines=$(wc -l <"$WORK/trace.ndjson" | tr -d ' ')
for i in 1 2; do
  curl -s -o "$WORK/live-$i.json" -H 'Content-Type: application/json' --data @"$ROOT/testdata/bid-request-live-bid.json" http://localhost:8080/openrtb2/auction
done
sleep 1
docker logs "$NAME" 2>"$WORK/pbs.log" | tail -n +"$((phase_a_lines + 1))" >"$WORK/trace-live.ndjson"

log "7/7 verify phase B (item 3 strict)"
"$WORK/tracecheck" -expect 1 -partner 33415-10498 -auction-id live-bid-1 -bidders aceex,appnexus,amx,adyoulike,onetag -strict-bids \
  -responses "$WORK/live-[0-9]*.json" -pbs-log "$WORK/pbs.log" "$WORK/trace-live.ndjson" || fail "phase B: see $WORK"
log "done — artefacts in $WORK"

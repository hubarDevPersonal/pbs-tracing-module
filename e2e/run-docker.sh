#!/usr/bin/env bash
# Dockerised end-to-end check: build the image, run it, fire the assessment's own curl, assert the trace.
#
#   e2e/run-docker.sh                      # builds pbs-tracer:local, 5 requests, expects 3 packets
#   IMAGE=... REQUESTS=6 EXPECT_PACKETS=3 STRICT_BIDS=0 NO_BUILD=1 e2e/run-docker.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
IMAGE="${IMAGE:-pbs-tracer:local}"
REQUESTS="${REQUESTS:-5}"
EXPECT_PACKETS="${EXPECT_PACKETS:-3}"   # TracePacketsAmount of the sample partner in rules.go
STRICT_BIDS="${STRICT_BIDS:-0}"
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/pbs-e2e-docker.XXXXXX")}"; mkdir -p "$WORK"
NAME="pbs-tracer-e2e-$$"

log()  { printf '\033[1;34m[e2e-docker]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[e2e-docker] FAIL:\033[0m %s\n' "$*"; exit 1; }
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

if [[ "${NO_BUILD:-0}" != "1" ]]; then
  log "1/5 docker build $IMAGE (module tests run inside the build)"
  docker build -t "$IMAGE" "$ROOT" || fail "docker build failed"
else
  log "1/5 skipping build (NO_BUILD=1)"
fi

log "2/5 start container"
docker run -d --name "$NAME" -p 8080:8080 "$IMAGE" >/dev/null
i=0
until [[ "$(docker inspect -f '{{.State.Health.Status}}' "$NAME" 2>/dev/null)" == "healthy" || $i -ge 60 ]]; do i=$((i+1)); sleep 1; done
[[ "$(docker inspect -f '{{.State.Health.Status}}' "$NAME")" == "healthy" ]] || { docker logs "$NAME" | tail -30; fail "container did not become healthy"; }

log "3/5 run 02-send-bid-request.sh $REQUESTS times + one unknown-account request"
for i in $(seq 1 "$REQUESTS"); do
  bash "$ROOT/02-send-bid-request.sh" -s >"$WORK/resp-$i.json" 2>/dev/null   # the assessment's own script, unchanged
  python3 -c "import json,sys; json.load(open(sys.argv[1]))" "$WORK/resp-$i.json" || fail "response $i is not JSON"
done
python3 - "$ROOT/01-bid-request-example.json" "$WORK/unknown.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); d["site"]["publisher"]["ext"]["prebid"]["parentAccount"]="not-a-partner"; d["site"]["publisher"]["id"]="not-a-partner"
json.dump(d,open(sys.argv[2],"w"))
PY
curl -s -o "$WORK/resp-unknown.json" -H 'Content-Type: application/json' --data @"$WORK/unknown.json" http://localhost:8080/openrtb2/auction
sleep 1

log "4/5 collect stdout (trace) and stderr (PBS log) from the container"
docker logs "$NAME" >"$WORK/trace.ndjson" 2>"$WORK/pbs.log"
grep -q "Not found hook while building hook execution plan: test_provider.test_tracer" "$WORK/pbs.log" \
  && fail "module hooks not found by PBS — registration broken"

log "5/5 assert"
python3 - "$WORK/trace.ndjson" "$EXPECT_PACKETS" "$WORK" "$REQUESTS" "$STRICT_BIDS" <<'PY'
import json, sys, glob
trace, expect, work, requests, strict = sys.argv[1], int(sys.argv[2]), sys.argv[3], int(sys.argv[4]), sys.argv[5] == "1"
lines = [l for l in open(trace).read().splitlines() if l.strip()]
bad = [l for l in lines if not l.startswith("{")]
assert not bad, f"non-JSON lines on stdout: {bad[:3]}"
assert len(lines) == expect, f"expected {expect} trace packets (TracePacketsAmount), got {len(lines)}"
sample_bidders = {"aceex", "appnexus", "amx", "adyoulike"}
responses_seen = 0
for i, l in enumerate(lines, 1):
    p = json.loads(l)
    assert p["module"] == "test_provider.test_tracer"
    assert p["partner_id"] == "664-025-677-881", p["partner_id"]
    assert p["packet_index"] == i
    assert p["incoming_request"]["body"]["id"] == "5d394bed0104ca857c702982fe8d95e408820eb2-3"
    assert {b["bidder"] for b in p["bidder_requests"]} == sample_bidders
    for b in p["bidder_requests"]:
        assert b["request"]["id"] == p["incoming_request"]["body"]["id"]
    for b in p["bidder_responses"]:
        assert b["bidder"] in sample_bidders and isinstance(b["response"]["bids"], list)
    responses_seen += len(p["bidder_responses"])
    fr = p["final_response"]["body"]
    assert fr["id"] == p["incoming_request"]["body"]["id"]
    assert "debug" in fr.get("ext", {}), "final_response must be the enriched response the client received"
    assert p["completed_at"] >= p["started_at"] and p["started_at"].endswith("Z")
for f in sorted(glob.glob(f"{work}/resp-[0-9]*.json"))[:requests]:
    r = json.load(open(f))
    dump = json.dumps(r.get("ext", {}).get("prebid", {}).get("modules", {})).replace(" ", "")
    assert "test_provider.test_tracer" in dump, f"{f}: module missing from ext.prebid.modules"
    assert '"status":"success"' in dump and '"status":"failure"' not in dump and '"status":"timeout"' not in dump, f"{f}: bad hook status"
msg = f"OK: {len(lines)} packets on stdout; live bidder responses recorded: {responses_seen}"
if responses_seen == 0:
    msg += " (all live bidders answered no-bid; item 3 not exercised from this network)"
    if strict: raise SystemExit("FAIL: STRICT_BIDS=1 and no live bidder responded")
print(msg)
PY
log "done — artefacts in $WORK"

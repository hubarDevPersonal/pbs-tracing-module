#!/usr/bin/env bash
# End-to-end check for the test_provider.test_tracer module against LIVE bidders (docs/04-test-plan.md §5).
# Uses the assessment's pbs.yaml and 01-bid-request-example.json unchanged. No mocks.
#
# Requires: Go >= 1.25, curl, python3, a Prebid Server checkout in $PBS_DIR (module path .../v4),
#           outbound internet access (bidders are called for real). Ports 8080 and 6060 free.
#
#   PBS_DIR=~/Dev/prebid-server e2e/run.sh                       # 5 requests, expect 3 traced packets
#   PBS_DIR=... REQUESTS=6 EXPECT_PACKETS=3 STRICT_BIDS=1 e2e/run.sh   # also require >=1 live bidder response
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
PBS_DIR="${PBS_DIR:?set PBS_DIR to a prebid-server checkout}"
REQUESTS="${REQUESTS:-5}"
EXPECT_PACKETS="${EXPECT_PACKETS:-3}"   # TracePacketsAmount of the sample partner in rules.go
STRICT_BIDS="${STRICT_BIDS:-0}"         # 1 → fail when no live bidder returned a response (item 3)
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/pbs-e2e.XXXXXX")}"
TRACE="$WORK/trace.ndjson"; PBS_LOG="$WORK/pbs.log"

log()  { printf '\033[1;34m[e2e]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[e2e] WARN:\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[e2e] FAIL:\033[0m %s\n' "$*"; exit 1; }

cleanup() { [[ -n "${PBS_PID:-}" ]] && kill "$PBS_PID" 2>/dev/null || true; }
trap cleanup EXIT

wait_for() { # url attempts
  local i=0
  until curl -sf -o /dev/null "$1" || [[ $i -ge ${2:-60} ]]; do i=$((i+1)); sleep 0.5; done
  [[ $i -lt ${2:-60} ]] || fail "$1 did not come up"
}

log "1/5 install module into $PBS_DIR, verify, build"
rm -rf "$PBS_DIR/modules/test_provider"
cp -R "$ROOT/modules/test_provider" "$PBS_DIR/modules/"
( cd "$PBS_DIR" \
  && go generate ./modules/... \
  && gofmt -l ./modules/test_provider | { ! grep . ; } \
  && go vet ./modules/test_provider/... \
  && go test -count=1 ./modules/test_provider/... \
  && go build -o "$WORK/prebid-server" . ) || fail "build/test step failed"
grep -q '"test_provider"' "$PBS_DIR/modules/builder.go" || fail "builder.go does not register test_provider"

log "2/5 start Prebid Server with the provided pbs.yaml (stdout → $TRACE, stderr → $PBS_LOG)"
cp "$ROOT/pbs.yaml" "$PBS_DIR/pbs.yaml"
( cd "$PBS_DIR" && "$WORK/prebid-server" -stderrthreshold=INFO >"$TRACE" 2>"$PBS_LOG" & echo $! > "$WORK/pbs.pid" )
PBS_PID="$(cat "$WORK/pbs.pid")"
wait_for "http://localhost:8080/status"

log "3/5 send $REQUESTS sample requests (live bidders)"
for i in $(seq 1 "$REQUESTS"); do
  code=$(curl -s -o "$WORK/resp-$i.json" -w '%{http_code}' \
    -H 'Content-Type: application/json' --data @"$ROOT/01-bid-request-example.json" \
    http://localhost:8080/openrtb2/auction)
  [[ "$code" == "200" ]] || fail "request $i returned HTTP $code"
done
# negative: an account that matches no rule must not be traced
python3 - "$ROOT/01-bid-request-example.json" "$WORK/unknown.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); d["site"]["publisher"]["ext"]["prebid"]["parentAccount"]="not-a-partner"; d["site"]["publisher"]["id"]="not-a-partner"
json.dump(d,open(sys.argv[2],"w"))
PY
curl -s -o "$WORK/resp-unknown.json" -H 'Content-Type: application/json' --data @"$WORK/unknown.json" http://localhost:8080/openrtb2/auction
sleep 1

log "4/5 assert"
grep -q "Not found hook while building hook execution plan: test_provider.test_tracer" "$PBS_LOG" \
  && fail "PBS could not find the module hooks — registration broken (see $PBS_LOG)"

python3 - "$TRACE" "$EXPECT_PACKETS" "$WORK" "$REQUESTS" "$STRICT_BIDS" <<'PY'
import json, sys, glob
trace, expect, work, requests, strict = sys.argv[1], int(sys.argv[2]), sys.argv[3], int(sys.argv[4]), sys.argv[5] == "1"
lines = [l for l in open(trace).read().splitlines() if l.strip()]
assert len(lines) == expect, f"expected {expect} trace packets (TracePacketsAmount), got {len(lines)}"
sample_bidders = {"aceex", "appnexus", "amx", "adyoulike"}
responses_seen = 0
for i, l in enumerate(lines, 1):
    p = json.loads(l)
    assert p["module"] == "test_provider.test_tracer", p["module"]
    assert p["partner_id"] == "664-025-677-881", p["partner_id"]
    assert p["packet_index"] == i, (p["packet_index"], i)
    # item 1: incoming request
    assert p["incoming_request"]["body"]["id"] == "5d394bed0104ca857c702982fe8d95e408820eb2-3"
    # item 2: one outgoing request per bidder in the sample
    bidders = {b["bidder"] for b in p["bidder_requests"]}
    assert bidders == sample_bidders, f"bidder_requests {bidders} != {sample_bidders}"
    for b in p["bidder_requests"]:
        assert b["request"]["id"] == p["incoming_request"]["body"]["id"]
        assert b["timestamp"] >= p["incoming_request"]["timestamp"]
    # item 3: live bidders may answer 204 → PBS does not invoke raw_bidder_response; assert shape only
    for b in p["bidder_responses"]:
        assert b["bidder"] in sample_bidders, b["bidder"]
        assert "currency" in b["response"] and isinstance(b["response"]["bids"], list)
    responses_seen += len(p["bidder_responses"])
    # item 4: final response is the one the client got (same id, debug ext present because debug:true)
    fr = p["final_response"]["body"]
    assert fr["id"] == p["incoming_request"]["body"]["id"]
    assert "debug" in fr.get("ext", {}), "final_response must be the enriched response sent to the client"
    for k in ("started_at", "completed_at"):
        assert p[k].endswith("Z"), p[k]
    assert p["completed_at"] >= p["started_at"]
# hook outcomes are visible in the HTTP responses (debug:true + trace:verbose)
for f in sorted(glob.glob(f"{work}/resp-[0-9]*.json"))[:requests]:
    r = json.load(open(f))
    mods = r.get("ext", {}).get("prebid", {}).get("modules", {})
    assert mods, f"{f}: ext.prebid.modules missing — hooks did not run"
    dump = json.dumps(mods).replace(" ", "")
    assert "test_provider.test_tracer" in dump, f"{f}: module not present in hook trace"
    assert '"status":"success"' in dump and '"status":"failure"' not in dump, f"{f}: non-success hook status"
    assert '"status":"timeout"' not in dump, f"{f}: hook timeout"
msg = f"OK: {len(lines)} packets; live bidder responses recorded: {responses_seen}"
if responses_seen == 0:
    msg += " (all bidders answered 204/no-bid — item 3 could not be exercised from this network)"
    if strict:
        raise SystemExit("FAIL: STRICT_BIDS=1 and no live bidder responded")
print(msg)
PY

log "5/5 done — artefacts in $WORK"

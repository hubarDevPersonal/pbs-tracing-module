#!/usr/bin/env bash
# Load + profiling run against the Docker image with the perf profile (tuned config, caching DNS sidecar).
# Live bidders are called for real — keep RPS modest. Produces $WORK/perf-report.md.
#
#   scripts/perf-docker.sh                       # 5 rps for 30 s, 30 s CPU profile, assessment request
#   RPS=10 DURATION=60 NO_BUILD=1 scripts/perf-docker.sh
#   BODY=testdata/bid-request-live-bid.json scripts/perf-docker.sh   # onetag bids → raw_bidder_response + bid processing on the profile
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RPS="${RPS:-5}"
DURATION="${DURATION:-30}"            # seconds; also the pprof CPU profile length
CONCURRENCY="${CONCURRENCY:-16}"
BODY="${BODY:-$ROOT/01-bid-request-example.json}"   # request body; testdata/bid-request-live-bid.json exercises a bidding path
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/pbs-perf.XXXXXX")}"; mkdir -p "$WORK"
COMPOSE=(docker compose --profile perf -f "$ROOT/docker-compose.yml")

log()  { printf '\033[1;34m[perf]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[perf] FAIL:\033[0m %s\n' "$*"; exit 1; }
cleanup() { "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

metric() { # file name → sum of values for a metric name (all label sets)
  awk -v m="$2" '$1 ~ "^"m"({|$)" { s += $NF } END { printf "%d", s }' "$1"
}

log "1/6 build tools and start the perf profile (pbs-perf + dns)"
( cd "$ROOT" && go build -o "$WORK/loadgen" ./cmd/loadgen ) || fail "loadgen build failed"
if [[ "${NO_BUILD:-0}" == "1" ]]; then "${COMPOSE[@]}" up -d --no-build dns pbs-perf; else "${COMPOSE[@]}" up -d --build dns pbs-perf; fi
for _ in $(seq 1 90); do curl -sf -o /dev/null http://localhost:8080/status && break; sleep 1; done
curl -sf -o /dev/null http://localhost:8080/status || { "${COMPOSE[@]}" logs pbs-perf | tail -30; fail "pbs-perf did not come up"; }
curl -sf -o /dev/null http://localhost:9100/metrics || fail "prometheus metrics not exposed on :9100"
curl -sf -o /dev/null http://localhost:6060/debug/pprof/ || fail "pprof not exposed on :6060"

log "2/6 warm-up (3 requests) and metric snapshots"
for _ in 1 2 3; do bash "$ROOT/02-send-bid-request.sh" -s >/dev/null 2>&1; done
sleep 1
curl -s http://localhost:9100/metrics >"$WORK/metrics-before.txt"
curl -s http://localhost:9153/metrics >"$WORK/coredns-before.txt"

log "3/6 CPU profile ($DURATION s) in the background + load: $RPS rps for $DURATION s"
curl -s -o "$WORK/cpu.pprof" "http://localhost:6060/debug/pprof/profile?seconds=$DURATION" &
PPROF_PID=$!
"$WORK/loadgen" -url http://localhost:8080/openrtb2/auction -body "$BODY" \
  -rps "$RPS" -duration "${DURATION}s" -concurrency "$CONCURRENCY" -out "$WORK/loadgen.json" | tee "$WORK/loadgen.txt" || true
wait "$PPROF_PID" || true

log "4/6 metric snapshots after load"
sleep 1
curl -s http://localhost:9100/metrics >"$WORK/metrics-after.txt"
curl -s http://localhost:9153/metrics >"$WORK/coredns-after.txt"
"${COMPOSE[@]}" logs --no-log-prefix pbs-perf >"$WORK/trace.ndjson" 2>"$WORK/pbs.log" || true

log "5/6 pprof top"
( cd "$ROOT" && go tool pprof -top -nodecount=30 "$WORK/cpu.pprof" >"$WORK/pprof-top.txt" 2>/dev/null ) || echo "(pprof top unavailable)" >"$WORK/pprof-top.txt"

log "6/6 report → $WORK/perf-report.md"
{
  echo "# Perf run $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo "Config: deploy/pbs.perf.yaml; load: ${RPS} rps × ${DURATION}s, concurrency ${CONCURRENCY}; live bidders; body: $(basename "$BODY")."
  echo
  echo '## Load generator'; echo '```'; cat "$WORK/loadgen.txt"; echo '```'
  echo
  echo '## Connection reuse (prometheus, delta during the run)'
  for m in prebid_server_adapter_connection_created prebid_server_adapter_connection_reused prebid_server_adapter_dns_lookup_time_count prebid_server_adapter_tls_handshake_time_count prebid_server_requests; do
    b=$(metric "$WORK/metrics-before.txt" "$m"); a=$(metric "$WORK/metrics-after.txt" "$m")
    echo "- \`$m\`: $((a - b))"
  done
  echo
  echo '## DNS sidecar (coredns, delta during the run)'
  for m in coredns_dns_requests_total coredns_cache_hits_total coredns_cache_misses_total coredns_forward_requests_total; do
    b=$(metric "$WORK/coredns-before.txt" "$m"); a=$(metric "$WORK/coredns-after.txt" "$m")
    echo "- \`$m\`: $((a - b))"
  done
  echo
  echo '## Module hook durations (prometheus, cumulative)'
  grep -E '^prebid_server_modules_test_provider_test_tracer_duration_(sum|count)' "$WORK/metrics-after.txt" | sed 's/^/    /' || true
  echo
  echo '## Trace packets emitted during the run'
  echo "- lines on stdout: $(grep -c '^{' "$WORK/trace.ndjson" || true)"
  echo
  echo '## CPU profile (top 30, pprof)'; echo '```'; cat "$WORK/pprof-top.txt"; echo '```'
} >"$WORK/perf-report.md"
cat "$WORK/perf-report.md" | head -60
log "done — artefacts in $WORK"

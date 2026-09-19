#!/usr/bin/env bash
# Load test (test/load) against the Docker image with the perf profile (tuned config, caching DNS sidecar),
# with a CPU profile and metric deltas captured during the run. Live bidders are called for real, keep RPS
# modest. Writes $WORK/perf-report.md.
#
#   scripts/perf-docker.sh                       # 5 rps, 30 s per scenario (sample and live-bid request)
#   RPS=10 DURATION=60 NO_BUILD=1 scripts/perf-docker.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RPS="${RPS:-5}"
DURATION="${DURATION:-30}"            # seconds per load scenario; the CPU profile covers both scenarios
CONCURRENCY="${CONCURRENCY:-16}"
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/pbs-perf.XXXXXX")}"; mkdir -p "$WORK"
COMPOSE=(docker compose --profile perf -f "$ROOT/docker-compose.yml")

log()  { printf '\033[1;34m[perf]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[perf] FAIL:\033[0m %s\n' "$*"; exit 1; }
cleanup() { "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

metric() { # file name → sum of values for a metric name (all label sets)
  awk -v m="$2" '$1 ~ "^"m"({|$)" { s += $NF } END { printf "%d", s }' "$1"
}

log "1/6 start the perf profile (pbs-perf + dns)"
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

log "3/6 CPU profile in the background + load test: $RPS rps, $DURATION s per scenario"
curl -s -o "$WORK/cpu.pprof" "http://localhost:6060/debug/pprof/profile?seconds=$((2 * DURATION))" &
PPROF_PID=$!
LOAD_STATUS=0
( cd "$ROOT" && go test -tags load -count=1 -v -timeout 0 ./test/load \
    -args -pbs-url http://localhost:8080 -rps "$RPS" -duration "${DURATION}s" -concurrency "$CONCURRENCY" ) | tee "$WORK/load.txt" || LOAD_STATUS=$?
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
  echo "Config: deploy/pbs.perf.yaml; load: ${RPS} rps × ${DURATION}s per scenario, concurrency ${CONCURRENCY}; live bidders."
  echo
  echo '## Load test (test/load)'; echo '```'; cat "$WORK/load.txt"; echo '```'
  echo
  echo '## Connection reuse (prometheus, delta during the run)'
  for m in prebid_server_adapter_connection_created prebid_server_adapter_connection_reused prebid_server_dns_lookup_time_count prebid_server_tls_handshake_time_count prebid_server_requests; do
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
head -60 "$WORK/perf-report.md"
[[ "$LOAD_STATUS" == "0" ]] || fail "load test failed, see $WORK/load.txt"
log "done — artefacts in $WORK"

#!/usr/bin/env bash
# Capture a CPU profile (default 30 s) from a running Prebid Server admin port and print the top frames.
#   scripts/profile.sh [seconds] [admin-host:port]
set -euo pipefail
SECONDS_="${1:-30}"; ADMIN="${2:-localhost:6060}"
OUT="${OUT:-cpu-$(date +%s).pprof}"
curl -sf -o "$OUT" "http://$ADMIN/debug/pprof/profile?seconds=$SECONDS_"
echo "profile: $OUT"
go tool pprof -top -nodecount=40 "$OUT"

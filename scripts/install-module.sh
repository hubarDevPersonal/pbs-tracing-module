#!/usr/bin/env bash
# Copy the module package into a Prebid Server checkout and regenerate modules/builder.go.
#   PBS_DIR=~/Dev/prebid-server scripts/install-module.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PBS_DIR="${PBS_DIR:?set PBS_DIR to a prebid-server checkout}"
DEST="$PBS_DIR/modules/test_provider/test_tracer"

rm -rf "$DEST" && mkdir -p "$(dirname "$DEST")"
cp -R "$ROOT/modules/test_provider/test_tracer" "$DEST"
( cd "$PBS_DIR" && go generate ./modules/... )
grep -q '"test_provider"' "$PBS_DIR/modules/builder.go" || { echo "builder.go does not register test_provider" >&2; exit 1; }
echo "installed into $DEST and registered in modules/builder.go"

#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for the awesome-lists lens (see issue #354).
# Runs 6 independent gates in order; fails loud (non-zero exit) on the
# first gate that fails. Re-run after every change to lenses/awesome-lists.json
# or its supporting code/tests.
#
# Usage: bash scripts/harness-354.sh

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

OUT_DIR="$REPO_ROOT/.harness-out"
mkdir -p "$OUT_DIR"

GATE=0
FAILED=0

run_gate() {
  local name="$1"
  local logfile="$2"
  shift 2
  GATE=$((GATE + 1))
  echo "==> Gate ${GATE}: ${name}"
  if "$@" >"$logfile" 2>&1; then
    echo "    PASS (log: ${logfile#"$REPO_ROOT"/})"
  else
    local status=$?
    echo "    FAIL (exit ${status}) — see ${logfile#"$REPO_ROOT"/}"
    tail -n 30 "$logfile" | sed 's/^/    | /'
    FAILED=1
  fi
}

run_gate "Lint (golangci-lint)" \
  "$OUT_DIR/01-lint.log" \
  make lint

run_gate "SAST (gosec)" \
  "$OUT_DIR/02-sast.log" \
  make sec

run_gate "Multi-instance isolation test" \
  "$OUT_DIR/03-isolation.log" \
  go test ./internal/search/... -run 'TestMultiInstanceLensRegistryIsolation' -v -count=1

run_gate "Dead-code scan (go vet + unused linter)" \
  "$OUT_DIR/04-deadcode.log" \
  go vet ./...

run_gate "Unit/integration tests (Phase-1 rules)" \
  "$OUT_DIR/05-unit.log" \
  go test -race ./internal/search/... ./internal/tools/... -count=1

run_gate "Live E2E (real queries, recorded proof)" \
  "$OUT_DIR/06-live-e2e.log" \
  go test -tags=live -run 'TestAwesomeListsLensLive' ./internal/search/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

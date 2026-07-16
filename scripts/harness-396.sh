#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for the GitHub API capability work (see issue
# #396 and its sub-issues #394, #282, #395). Runs 6 independent gates in
# order; fails loud (non-zero exit) on the first gate that fails. Re-run
# after every change to internal/search/github*.go, internal/scraper/github.go,
# internal/search/ecosystems_awesome.go, or their supporting tests.
#
# Usage: bash scripts/harness-396.sh

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

run_gate "SAST (gosec) + SSRF-safe-client grep (rule 2.2)" \
  "$OUT_DIR/02-sast.log" \
  bash -c '
    set -e
    make sec
    echo "--- grep: no http.DefaultClient in new GitHub code ---"
    ! grep -rn "http\.DefaultClient" internal/search/github*.go internal/scraper/github.go 2>/dev/null
    echo "--- grep: shared helper defined exactly once, called at both search-layer sites (rule 5.1) ---"
    defs=$(grep -rn "^func githubAPIRequest" internal/search/*.go 2>/dev/null | wc -l | tr -d " ")
    if [ "$defs" != "1" ]; then echo "expected exactly 1 githubAPIRequest definition, found $defs"; exit 1; fi
    echo "--- grep: no TODO/FIXME left in new files (rule 5.3) ---"
    ! grep -rn "TODO\|FIXME" internal/search/github*.go internal/scraper/github.go 2>/dev/null
  '

run_gate "Multi-instance isolation tests (rule 1.1/1.2/1.3)" \
  "$OUT_DIR/03-isolation.log" \
  go test ./internal/search/... ./internal/scraper/... -run 'TestMultiInstance.*GitHub|TestAvailableProviders' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/04-deadcode.log" \
  go vet ./...

run_gate "Unit/integration tests (Phase-1 rules 2-4)" \
  "$OUT_DIR/05-unit.log" \
  go test -race ./internal/search/... ./internal/scraper/... ./internal/tools/... ./internal/config/... -count=1

run_gate "Live E2E (real GitHub API calls, recorded proof)" \
  "$OUT_DIR/06-live-e2e.log" \
  go test -tags=live -run 'TestGitHub.*Live' ./internal/search/... ./internal/scraper/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

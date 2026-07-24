#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for v1.38.0 "Engagement Signals & Platform
# Scrapers" (see issue #407 and its tracked issues #275, #276, #277, #278,
# #279, #280, #281, #285, #393). Runs 6 independent gates in order; fails
# loud (non-zero exit) on the first gate that fails. Re-run after every
# change to internal/content/quality.go, internal/circuit/breaker.go,
# internal/search/{router,reddit,bluesky,hackernews,exa,provider}.go,
# internal/scraper/{twitter,bluesky,browser}.go, or their supporting tests.
#
# Usage: bash scripts/harness-407.sh

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

# go test with -run exits 0 and prints "no tests to run" when the pattern
# matches nothing — a silent no-op that would let this gate rubber-stamp
# tests that were never written. Treat that output as an explicit failure.
require_tests_ran() {
  if grep -q "no tests to run" "$1"; then
    echo "no tests matched the -run pattern (target tests not written yet)" >>"$1"
    return 1
  fi
  return 0
}

run_gate_requiring_tests() {
  local name="$1"
  local logfile="$2"
  shift 2
  GATE=$((GATE + 1))
  echo "==> Gate ${GATE}: ${name}"
  if "$@" >"$logfile" 2>&1 && require_tests_ran "$logfile"; then
    echo "    PASS (log: ${logfile#"$REPO_ROOT"/})"
  else
    echo "    FAIL — see ${logfile#"$REPO_ROOT"/}"
    tail -n 30 "$logfile" | sed 's/^/    | /'
    FAILED=1
  fi
}

run_gate "Lint (golangci-lint)" \
  "$OUT_DIR/01-lint.log" \
  make lint

run_gate "SAST (gosec) + pattern checks (rules 2.1/2.2/2.3/2.4, 4.1)" \
  "$OUT_DIR/02-sast.log" \
  bash -c '
    set -e
    make sec
    echo "--- grep: no http.DefaultClient in new provider/scraper files (rule 2.1) ---"
    ! grep -rln "http\.DefaultClient" internal/search/reddit.go internal/search/bluesky.go internal/scraper/bluesky.go 2>/dev/null
    echo "--- grep: LimitReader present in reddit.go/bluesky.go, not bare io.ReadAll (rule 4.1) ---"
    for f in internal/search/reddit.go internal/search/bluesky.go; do
      if [ -f "$f" ]; then
        grep -q "io.LimitReader(resp.Body" "$f" || { echo "missing io.LimitReader cap in $f"; exit 1; }
      fi
    done
    echo "--- grep: no in-provider Breaker.Execute() calls in reddit.go/bluesky.go (rule 3.3) ---"
    ! grep -rln "Breaker\.Execute" internal/search/reddit.go internal/search/bluesky.go 2>/dev/null
    echo "--- grep: no TODO/FIXME left in new v1.38.0 files (rule 5.2) ---"
    ! grep -rn "TODO\|FIXME" internal/search/reddit.go internal/search/bluesky.go internal/scraper/bluesky.go 2>/dev/null
    echo "--- grep: no new secret-like env vars introduced (.env.example, rule 2.3) ---"
    added=$(git diff origin/main...HEAD -- .env.example 2>/dev/null | grep "^+" | grep -iE "_KEY=|_TOKEN=|_SECRET=" | grep -v "^+++" || true)
    if [ -n "$added" ]; then echo "unexpected new secret-shaped env var in .env.example:"; echo "$added"; exit 1; fi
  '

run_gate_requiring_tests "Multi-instance isolation tests (rule 1.1/1.2/1.3)" \
  "$OUT_DIR/03-isolation.log" \
  go test ./internal/search/... ./internal/scraper/... ./internal/circuit/... -run 'TestMultiInstance.*|TestAvailableProviders|TestConcurrentAccess' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/04-deadcode.log" \
  go vet ./...

run_gate "Unit/integration tests (Phase-1 rules 2-6)" \
  "$OUT_DIR/05-unit.log" \
  go test -race ./internal/content/... ./internal/circuit/... ./internal/search/... ./internal/scraper/... ./internal/tools/... ./internal/config/... -count=1

run_gate_requiring_tests "Live E2E (real provider/scraper calls, recorded proof)" \
  "$OUT_DIR/06-live-e2e.log" \
  go test -tags=live -run 'TestRedditProviderLive|TestBlueskyProviderLive|TestScrapeBskyPostLive|TestScrapeBskyProfileLive' ./internal/search/... ./internal/scraper/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

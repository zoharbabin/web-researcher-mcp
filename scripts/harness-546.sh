#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for "GitHub trust-surface signals for scrape_page"
# (see issue #546). Runs all 6 gates in order, regardless of earlier
# failures, then exits non-zero if any gate failed — so a single run's
# output always shows the full pass/fail picture, not just the first
# failure. Re-run after every change to internal/scraper/github.go,
# internal/content/quality.go, internal/content/classify.go, or their
# supporting tests.
#
# Usage: bash scripts/harness-546.sh

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
  "$OUT_DIR/546-01-lint.log" \
  make lint

run_gate "SAST (gosec) + pattern checks (rules 2.2/2.3/5.2/6.3/6.4)" \
  "$OUT_DIR/546-02-sast.log" \
  bash -c '
    set -e
    make sec
    echo "--- grep: no http.DefaultClient in github.go (rule 2.2) ---"
    ! grep -n "http\.DefaultClient" internal/scraper/github.go
    echo "--- grep: GitHubToken never co-occurs with log./Errorf/audit. (rule 2.3) ---"
    ! grep -n "GitHubToken" internal/scraper/github.go | grep -iE "log\.|Errorf|audit\."
    echo "--- grep: no TODO/FIXME left in github.go (rule 5.2) ---"
    ! grep -n "TODO\|FIXME" internal/scraper/github.go
    echo "--- grep: no new consent.Purpose/HasConsent call site added for this feature (rule 6.3) ---"
    ! grep -n "consent\.Purpose\|HasConsent" internal/scraper/github.go internal/content/quality.go internal/content/classify.go 2>/dev/null
    echo "--- grep: no new datasubject registry entry or persist.Store write added (rule 6.4) ---"
    ! grep -n "datasubject\.\|persist\.Store" internal/scraper/github.go internal/content/quality.go internal/content/classify.go 2>/dev/null
  '

run_gate_requiring_tests "Multi-instance isolation tests (rule 1.1)" \
  "$OUT_DIR/546-03-isolation.log" \
  go test ./internal/scraper/... -run 'TestMultiInstanceGitHubTrustSignalsIsolation|TestMultiInstance.*' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/546-04-deadcode.log" \
  go vet ./...

# internal/content is deliberately excluded: issue #546's Rules section
# (1.1-6.4) never mandates changing content.scoreAuthority/classify.go — the
# Problem section's mention of authorityTier/sourceType was motivating
# context, not a testable rule. See the Phase 4 closing comment for this
# documented deviation from the Phase 2 harness's original assumption.
run_gate_requiring_tests "Unit/integration tests (rules 2.1/2.4/3.1/3.2/3.3/4.1/4.2/4.3)" \
  "$OUT_DIR/546-05-unit.log" \
  go test -race ./internal/scraper/... ./internal/tools/... -run 'TestGitHub.*TrustSignal.*|TestFetchOrgMetadata.*|TestFetchContributorCount.*|TestFetchCommunityProfile.*|TestFetchReleaseCount.*|TestScrapeGitHubReadme.*Degrad.*|TestScrapePage.*GitHubTrust.*' -v -count=1

run_gate_requiring_tests "E2E (mocked GitHub API surface, recorded proof)" \
  "$OUT_DIR/546-06-e2e.log" \
  go test -tags=e2e -run 'TestGitHubTrustSignals546_E2E' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

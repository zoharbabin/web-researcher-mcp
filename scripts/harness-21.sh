#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for the v1.48.0 "Operational Hardening II"
# milestone (issue #21 and its sub-issues #463-#548; build constitution at
# #555). Runs 6 independent gates in order; fails loud (non-zero exit) on the
# first gate that fails. Re-run after every change touching any file listed
# in #21's sub-issues.
#
# Usage: bash scripts/harness-21.sh

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
  "$OUT_DIR/21-01-lint.log" \
  make lint

run_gate "SAST (gosec) + vuln scan + SSRF fuzz sweep (#499)" \
  "$OUT_DIR/21-02-sast.log" \
  bash -c '
    set -e
    make sec
    make vuln
    echo "--- grep: no TODO/FIXME left in any #21 sub-issue touched file ---"
    ! grep -rn "TODO\|FIXME" internal/scraper/pipeline.go internal/scraper/browser.go internal/scraper/ssrf.go internal/audit/logger.go internal/tools/metadata_test.go internal/resources/resources.go 2>/dev/null
    echo "--- fuzz: SSRF hostname/IP validator survives a short adversarial sweep (#499) ---"
    go test ./internal/scraper/... -run=^\$ -fuzz=FuzzIsBlockedHostname -fuzztime=5s
    go test ./internal/scraper/... -run=^\$ -fuzz=FuzzIsPrivateIP -fuzztime=5s
  '

# internal/scraper's per-tenant limiter (#463) and internal/audit's hash-chain
# (#466) are the two new pieces of per-instance state this milestone adds —
# this gate proves neither leaked into package-level shared state.
run_gate_requiring_tests "Multi-instance isolation tests (#463/#466)" \
  "$OUT_DIR/21-03-isolation.log" \
  go test ./internal/scraper/... ./internal/audit/... -run 'TestPerTenantLimiter_.*Isolation|TestMultiInstance.*|TestHashChain_TwoInstancesIndependent' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/21-04-deadcode.log" \
  go vet ./...

# Named for the specific new tests each Phase-3 fix adds (per #555's Proof:
# lines) rather than existing Test<Pkg>* prefixes — naming them explicitly is
# what makes this gate fail now and pass only once Phase 3 lands each one.
run_gate_requiring_tests "Unit/integration tests proving #463-#548 rules" \
  "$OUT_DIR/21-05-unit.log" \
  go test -race ./internal/scraper/... ./internal/audit/... ./internal/redisbackend/... ./internal/tools/... ./internal/resources/... -run 'TestPerTenantLimiter_GlobalCeilingRespected|TestPerTenantLimiter_SingleTenantUsesFullCapacity|TestScrapeFairness_TenantIDFromContextOnly|TestScrapeBrowserRecoversFromCrash|TestHashChain_.*|TestVerifyChain_.*|TestEnumErrorMessageParity|TestPromptsDocMatchesRegistry|TestPromptGolden|TestSharedCache.*|TestSessionManager.*' -v -count=1

run_gate_requiring_tests "E2E (real MCP flow, recorded proof)" \
  "$OUT_DIR/21-06-e2e.log" \
  go test -tags=e2e -run 'TestSecurity_STDIO_Templates|TestHTTP_Templates' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

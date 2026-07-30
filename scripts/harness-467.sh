#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for the v1.47.0 "Operational Hardening" milestone
# (issue #467 and its 22 sub-issues #468-#489). Runs 6 independent gates in
# order; fails loud (non-zero exit) on the first gate that fails. Re-run
# after every change touching any file listed in #467's sub-issues.
#
# Usage: bash scripts/harness-467.sh

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

run_gate "Lint (golangci-lint, incl. bodyclose/noctx per #477)" \
  "$OUT_DIR/467-01-lint.log" \
  make lint

run_gate "SAST (gosec) + vuln scan + pattern checks (#484/#486/#488)" \
  "$OUT_DIR/467-02-sast.log" \
  bash -c '
    set -e
    make sec
    make vuln
    echo "--- grep: CACHE_ISOLATION startup guard exists (#484) ---"
    grep -rq "CACHE_ISOLATION" internal/server/*.go internal/config/*.go 2>/dev/null
    echo "--- grep: ADMIN_API_KEY_PREV dual-key rotation exists (#488) ---"
    grep -rq "ADMIN_API_KEY_PREV" internal/config/config.go 2>/dev/null
    echo "--- grep: no TODO/FIXME left in any #467 sub-issue touched file ---"
    ! grep -rn "TODO\|FIXME" internal/circuit/breaker.go internal/search/router.go internal/scraper/pipeline.go internal/metrics/collector.go internal/ratelimit/limiter.go internal/audit/logger.go internal/config/config.go 2>/dev/null
  '

# internal/tools holds zero per-instance state (Design Rule 1). This gate
# targets the provider/breaker/cache-level state the Isolation rows concern:
# per-tenant cache reads (#484) and bounded per-tenant metrics (#475).
run_gate_requiring_tests "Multi-instance isolation tests (#475/#484)" \
  "$OUT_DIR/467-03-isolation.log" \
  go test ./internal/cache/... ./internal/metrics/... -run 'TestCacheIsolationCrossTenant|TestTenantStatsBounded|TestMultiInstance.*' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/467-04-deadcode.log" \
  go vet ./...

# Named for the specific new tests each Phase-3 fix adds (per each sub-issue's
# Proof: line) rather than existing Test<Pkg>* prefixes — naming them
# explicitly is what makes this gate fail now and pass only once Phase 3 lands.
run_gate_requiring_tests "Unit/integration tests proving #468-#489 rules" \
  "$OUT_DIR/467-05-unit.log" \
  go test -race ./internal/circuit/... ./internal/search/... ./internal/scraper/... ./internal/cache/... ./internal/metrics/... ./internal/ratelimit/... ./internal/audit/... ./internal/config/... ./internal/session/... -run 'TestCircuitConfigFromEnv|TestBreakerJitterOnResetTimeout|TestRouterChaosFailoverAndRecovery|TestScrapePipelineTierFairness|TestSingleflightCoalescesCacheMiss|TestTenantStatsBounded|TestRateLimiterRedisFallbackLogsAndCounts|TestCacheIsolationCrossTenant|TestAdminKeyDualKeyGracePeriod|TestAuditRequestBodyErasureOrRedaction|TestSessionEncryptionKeyRotationEdgeCases|TestSessionDiskVsRedisSwitch' -v -count=1

run_gate_requiring_tests "E2E (real MCP flow, recorded proof)" \
  "$OUT_DIR/467-06-e2e.log" \
  go test -tags=e2e -run 'TestOperationalHardening467_E2E' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for milestone #16 / spec issue #441 (v1.46.0
# Research Intelligence): research_panel (#302) + answer/structured_search
# removal (#301), research monitoring (#273), ScholarAPI provider (#266),
# Wikidata corporate-ownership fallback (#248). Runs 6 independent gates in
# order; fails loud (non-zero exit) on the first gate that fails. Re-run
# after every change to internal/tools/{research_panel*,monitor,verify_recommendation}.go,
# internal/search/{scholarapi,wikidata,synthesis}.go, or internal/tools/research_panel_provider*.go.
#
# Usage: bash scripts/harness-16.sh

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
# matches nothing in a given package — a silent no-op that would let this
# gate rubber-stamp tests that were never written. A single invocation here
# spans multiple packages, and some legitimately have zero matches (e.g. a
# pattern targeting only internal/tools also gets pointed at internal/config
# for other rules in the same gate) — so only fail if NOT EVEN ONE test
# actually ran anywhere in the invocation.
require_tests_ran() {
  if ! grep -q -- '--- PASS:\|--- FAIL:' "$1"; then
    echo "no tests matched the -run pattern in any package (target tests not written yet)" >>"$1"
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
  "$OUT_DIR/16-01-lint.log" \
  make lint

run_gate "SAST (gosec) + vuln scan + pattern checks (spec #441 rules 2.1-2.6, 4.2)" \
  "$OUT_DIR/16-02-sast.log" \
  bash -c '
    set -e
    make sec
    make vuln
    echo "--- grep: no http.DefaultClient introduced by new provider/tool files ---"
    ! grep -rn "http\.DefaultClient" internal/search/scholarapi.go internal/search/wikidata.go internal/tools/research_panel*.go internal/tools/monitor.go 2>/dev/null
    echo "--- grep: answer/structured_search interfaces fully removed (rule 5.2, only after #301 lands) ---"
    if [ -f internal/search/synthesis.go ]; then
      if grep -q "AnswerProvider\|StructuredProvider" internal/search/synthesis.go 2>/dev/null; then
        echo "pre-#301 baseline: AnswerProvider/StructuredProvider still present (expected before removal)"
      fi
    fi
    echo "--- grep: no TODO/FIXME left in new milestone files ---"
    ! grep -rln "TODO\|FIXME" internal/search/scholarapi.go internal/search/wikidata.go internal/tools/research_panel*.go internal/tools/monitor.go internal/tools/verify_recommendation.go 2>/dev/null
    echo "--- grep: QID validated before SPARQL substitution (rule 2.2) ---"
    if [ -f internal/search/wikidata.go ]; then
      grep -q "\^Q\[0-9\]" internal/search/wikidata.go || { echo "wikidata.go must validate QID with ^Q[0-9]+\$ before SPARQL substitution"; exit 1; }
    fi
  '

# internal/tools and internal/search hold zero per-instance state (Design
# Rule 1). Proof: construct 2+ instances of each new provider/tool in one
# process and confirm no shared state, reusing the established
# TestRegisterAllDoesNotPanic pattern (harness-432's precedent) plus new
# provider-constructor tests named in each sub-issue's spec.
run_gate_requiring_tests "Multi-instance isolation (spec #441 rule 1.1-1.3)" \
  "$OUT_DIR/16-03-isolation.log" \
  go test ./internal/tools/... ./internal/search/... -run 'TestRegisterAllDoesNotPanic|TestScholarAPIWithKey|TestScholarAPIRequiresKey|TestWikidataOwnershipResolver_BlankToken|TestMonitorNotRegisteredWhenMonitorNil|TestResearchPanel.*Isolation|TestAvailableModelProviders' -v -race -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/16-04-deadcode.log" \
  go vet ./...

run_gate_requiring_tests "Unit/integration tests proving spec #441 rules 2-6" \
  "$OUT_DIR/16-05-unit.log" \
  go test -race ./internal/search/... ./internal/tools/... ./internal/config/... ./internal/consent/... ./internal/content/... -run 'TestScholarAPI|TestWikidataOwnershipResolver|TestBrandTokenFromHost|TestCorporateOwnership|TestSelfPromotionSignalCorporateOwnerFields|TestMonitor|TestResearchPanel|TestDivergence|TestAllToolsHaveAnnotations|TestToolsDocMatchesRegistry|TestAllToolsHaveOutputSchema|TestOutputSchemaMatchesResponse|TestExternalContentToolsCarryTrustMarker' -v -count=1

run_gate_requiring_tests "E2E (research_panel, monitor_query_save/check, verify_recommendation ownership branch)" \
  "$OUT_DIR/16-06-e2e.log" \
  go test -tags=e2e -run 'TestResearchPanel_E2E|TestMonitorQuery_E2E|TestVerifyRecommendation_E2E' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

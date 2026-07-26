#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for issue #434: citation_graph DOI-seed fallback
# flakiness, filing_search form_type inference gap, econ_search FRED
# popularity ranking, verify_recommendation lens-selection bias. Runs 6
# independent gates in order; fails loud (non-zero exit) on the first gate
# that fails. Re-run after every change to internal/tools/citationgraph.go,
# internal/search/{openalex,semanticscholar,edgar,fred}.go,
# internal/tools/verify_recommendation.go, or their supporting tests.
#
# Usage: bash scripts/harness-434.sh

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

run_gate "SAST (gosec) + pattern checks (Finding A/B/C/D security rows)" \
  "$OUT_DIR/02-sast.log" \
  bash -c '
    set -e
    make sec
    make vuln
    echo "--- grep: no bare fmt.Sprintf URL interpolation of unescaped DOI/form-type input ---"
    ! grep -rnE "fmt\.Sprintf\(\"[^\"]*%s[^\"]*\", *(doi|seedID|params\.FormType)\)" internal/tools/citationgraph.go internal/search/edgar.go internal/search/openalex.go 2>/dev/null
    echo "--- grep: FRED order_by/sort_order are fixed literals, never built from params.Query ---"
    ! grep -rnE "order_by.*params\.Query|sort_order.*params\.Query" internal/search/fred.go 2>/dev/null
    echo "--- grep: no TODO/FIXME left in touched #434 files ---"
    ! grep -rn "TODO\|FIXME" internal/tools/citationgraph.go internal/search/openalex.go internal/search/edgar.go internal/search/fred.go internal/tools/verify_recommendation.go internal/search/structured_domains.go 2>/dev/null
  '

# internal/tools is deliberately excluded: tool handlers hold zero
# per-instance state (Design Rule 1). This gate targets the provider-level
# state (FREDProvider, EDGARProvider, OpenAlexProvider, SemanticScholarProvider)
# that Finding A/B/C's Isolation rows concern.
run_gate_requiring_tests "Multi-instance isolation tests (Isolation rows)" \
  "$OUT_DIR/03-isolation.log" \
  go test ./internal/search/... -run 'TestMultiInstance.*|TestEDGARInterface|TestFREDInterface|TestConcurrentAccess' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/04-deadcode.log" \
  go vet ./...

# Named for the specific new tests each Phase-3 fix adds (per issue #434's
# Proof: lines) rather than the existing Test<Tool>* prefixes — the existing
# tests already pass today, which would let this gate rubber-stamp a baseline
# where none of the 4 fixes exist yet. Naming the new tests explicitly is what
# makes the gate fail now and pass only once Phase 3 lands.
run_gate_requiring_tests "Unit/integration tests proving Finding A/B/C/D rules" \
  "$OUT_DIR/05-unit.log" \
  go test -race ./internal/search/... ./internal/tools/... -run 'TestCitationGraphOpenAlexDOIToTitleFallback|TestCitationGraphBothProvidersFailErrorNamesBoth|TestEDGARFormTypeInferredFromQuery|TestEDGARExplicitFormTypeWinsOverInferred|TestEDGARAmbiguousFormTypeTakesFirst|TestFREDSeriesSearchOrdersByPopularity|TestFREDSeriesSearchDecodesPopularity|TestVerifyRecommendationLensSelectionTechClaim|TestVerifyRecommendationLensSelectionLegalClaim' -v -count=1

run_gate_requiring_tests "E2E (citation_graph/filing_search/econ_search/verify_recommendation reachable end-to-end)" \
  "$OUT_DIR/06-e2e.log" \
  go test -tags=e2e -run 'TestFindings434_E2E' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

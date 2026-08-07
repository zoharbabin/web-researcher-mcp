#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for "v1.47.2 Trust & Consistency Fixes"
# (see issue #549, consolidating #522-#539). Runs all 6 gates in order,
# regardless of earlier failures, then exits non-zero if any gate failed —
# so a single run's output always shows the full pass/fail picture, not
# just the first failure. Re-run after every change to any of the files
# touched by this milestone's 18 fixes.
#
# Usage: bash scripts/harness-549.sh

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

# Files touched across the 18 fixes (#522-#539), used by the SAST pattern
# checks and the Clean-code/Compliance grep gates (rules 2.1-2.3, 5.1, 5.4,
# 6.2-6.3).
TOUCHED_FILES=(
  internal/content/claim.go
  internal/tools/verify_citation.go
  internal/tools/audit_bibliography.go
  internal/tools/newssearch.go
  internal/tools/sequential.go
  internal/tools/patent.go
  internal/search/uspto.go
  internal/search/epo.go
  internal/search/lens.go
  internal/search/searchapi.go
  internal/content/bibliography_interchange.go
  internal/content/bibliography.go
  internal/tools/sourcetracker.go
  internal/tools/paper_fulltext.go
  internal/tools/get_research_session.go
  internal/tools/search.go
  internal/search/eurostat.go
  internal/tools/monarchsearch.go
  internal/tools/errors.go
  internal/tools/research_panel.go
  internal/session/types.go
  internal/tools/bibliography.go
  internal/tools/paper_fulltext_test.go
  internal/tools/depth_test.go
  internal/tools/verify_recommendation.go
  internal/tools/verify_recommendation_test.go
  internal/search/geo_eval_live_test.go
  internal/search/structured_domains.go
  internal/search/eurostat_test.go
  internal/tools/fred.go
  internal/tools/econ_render_test.go
  internal/tools/schemas.go
  internal/tools/monarchsearch_test.go
  internal/tools/zerohints_test.go
  internal/tools/research_panel_test.go
  internal/tools/research_panel_eval_test.go
  internal/tools/metadata_test.go
)

run_gate "Lint (golangci-lint)" \
  "$OUT_DIR/549-01-lint.log" \
  make lint

run_gate "SAST (gosec) + pattern checks (rules 2.1/2.2/2.3/5.1/5.3/5.4/6.2/6.3)" \
  "$OUT_DIR/549-02-sast.log" \
  bash -c '
    set -e
    make sec
    echo "--- grep: no exec/shell patterns in touched files (rule 2.1) ---"
    ! grep -n "os/exec\|exec\.Command" '"${TOUCHED_FILES[*]}"' 2>/dev/null
    echo "--- grep: no http.DefaultClient in paper_fulltext.go (rule 2.2) ---"
    ! grep -n "http\.DefaultClient" internal/tools/paper_fulltext.go
    echo "--- grep: no API key co-occurring with log/Errorf/audit in provider files (rule 2.3) ---"
    ! grep -n "APIKey\|apiKey" internal/search/uspto.go internal/search/epo.go internal/search/lens.go internal/search/searchapi.go 2>/dev/null | grep -iE "log\.|Errorf|audit\."
    echo "--- grep: no TODO/FIXME left in touched files (rule 5.1) ---"
    ! grep -rn "TODO\|FIXME" '"${TOUCHED_FILES[*]}"' 2>/dev/null
    echo "--- grep: old json tags fully retired, not aliased (rule 5.3) ---"
    ! grep -n "\"question\"" internal/tools/research_panel.go
    ! grep -n "\"numResults\"" internal/tools/monarchsearch.go
    echo "--- grep: no orphaned journalism lens-name reference anywhere (rule 5.4, #535) ---"
    ! grep -rln "journalism" --include="*.go" --include="*.json" internal/ lenses/
    echo "--- grep: investigative_records lens name propagated (rule 5.4, #535) ---"
    grep -q "investigative_records" internal/tools/verify_recommendation.go
    test -f lenses/investigative_records.json
    test -f internal/search/lenses_embed/investigative_records.json
    echo "--- grep: no new datasubject/persist.Store call sites in touched files (rule 6.2) ---"
    ! grep -n "persist\.Store\|datasubject\." '"${TOUCHED_FILES[*]}"' 2>/dev/null
    echo "--- grep: no new consent.Purpose/HasConsent call sites in touched files (rule 6.3) ---"
    ! grep -n "consent\.Purpose\|HasConsent" '"${TOUCHED_FILES[*]}"' 2>/dev/null
  '

run_gate_requiring_tests "Multi-instance isolation tests (rules 1.1/1.2)" \
  "$OUT_DIR/549-03-isolation.log" \
  go test ./internal/session/... ./internal/search/... -run 'TestMultiInstance.*|TestTenantIsolation|TestUserIsolationWithinTenant|TestSessionIndexTotalStepsEstimateIsolatedPerSession' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/549-04-deadcode.log" \
  go vet ./...

run_gate_requiring_tests "Unit/integration tests — one per issue (#522-#539)" \
  "$OUT_DIR/549-05-unit.log" \
  go test -race ./internal/content/... ./internal/tools/... ./internal/search/... -run 'TestExtractClaimEvidence.*|TestClaimTermCoverage.*|TestClaimTerms.*|TestNewsResultsSourceType.*|TestNewsSearch.*SourceType.*|TestSequentialSearch.*TotalStepsEstimate.*|TestBuildSequentialResponse.*TotalStepsEstimate.*|TestDetectDOI.*|TestDOI.*Full.*|TestAuditBibliography.*Frontiers.*|TestPatentSearch.*Provider.*Google.*|TestResolvePatentSearcher.*|TestUSPTOPatentsYearFilter.*|TestPatentSearch.*Landscape.*|TestPatentSearch.*CPCCode.*|TestFormatCSLJSON.*Collision.*|TestAcademicResultsToSources.*|TestSourceTracker.*Author.*|TestPaperFulltext.*Unpaywall.*|TestSourceTracker.*FoundInStep.*|TestSequentialSearch.*FoundInStep.*|TestSelectCorroborationLenses.*|TestGeoEval.*InvestigativeRecords.*|TestEurostat.*Warning.*|TestEconSearch.*Warning.*|TestMonarchSearch.*Hints.*|TestBuildZeroResultHints.*Required.*|TestResearchPanel.*Query.*|TestMonarchSearch.*NumResults.*' -v -count=1

run_gate_requiring_tests "E2E (real binary, real MCP transport, recorded proof)" \
  "$OUT_DIR/549-06-e2e.log" \
  go test -tags=e2e -run 'TestTrustConsistency549_E2E' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

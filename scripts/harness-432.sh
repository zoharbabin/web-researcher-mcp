#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for issue #432 (company_recon "web" phase dead
# code, scraper browser-tier 404 misclassification, audit_bibliography false
# not_found on arXiv-family DOIs). Runs 6 independent gates in order; fails
# loud (non-zero exit) on the first gate that fails. Re-run after every change
# to internal/tools/{company_recon,audit_bibliography,paper_fulltext}.go or
# internal/scraper/{browser,errors}.go.
#
# Usage: bash scripts/harness-432.sh

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
  "$OUT_DIR/432-01-lint.log" \
  make lint

run_gate "SAST (gosec) + vuln scan + pattern checks (rules 2.1/2.2)" \
  "$OUT_DIR/432-02-sast.log" \
  bash -c '
    set -e
    make sec
    make vuln
    echo "--- grep: browser-tier fix reuses classifyHTTPStatus, no bespoke status check (rule 5.2) ---"
    grep -q "classifyHTTPStatus" internal/scraper/browser.go || { echo "browser.go must call classifyHTTPStatus"; exit 1; }
    echo "--- grep: audit_bibliography DOI fallback consults deps.DOIRegistry, not a new resolver (rule 5.3) ---"
    grep -q "DOIRegistry" internal/tools/audit_bibliography.go || { echo "audit_bibliography.go must consult deps.DOIRegistry"; exit 1; }
    echo "--- grep: no TODO/FIXME left in the three fixed files ---"
    ! grep -rn "TODO\|FIXME" internal/tools/company_recon.go internal/tools/audit_bibliography.go internal/scraper/browser.go 2>/dev/null
  '

# internal/tools holds zero per-instance state (Design Rule 1); none of the
# three fixes add module-level mutable state (spec rule 1.1, N/A row). No
# dedicated new isolation test is required — the existing suite's concurrent
# registration test is reused as proof no regression was introduced.
run_gate_requiring_tests "Multi-instance isolation (rule 1.1, reuse)" \
  "$OUT_DIR/432-03-isolation.log" \
  go test ./internal/tools/... -run 'TestRegisterAllDoesNotPanic' -v -count=1

run_gate "Dead-code scan (go vet)" \
  "$OUT_DIR/432-04-deadcode.log" \
  go vet ./...

run_gate_requiring_tests "Unit/integration tests proving rules 3.1/3.2/3.3" \
  "$OUT_DIR/432-05-unit.log" \
  go test -race ./internal/tools/... ./internal/scraper/... -run 'TestCompanyRecon.*Web|TestCompanyReconPhaseSelection|TestScrapeBrowser.*NotFound|TestAuditBibliography.*DOIRegistry|TestAuditBibliography.*ArXiv|TestAuditBibliography.*Registry' -v -count=1

run_gate_requiring_tests "E2E (real MCP flow per finding, recorded proof)" \
  "$OUT_DIR/432-06-e2e.log" \
  go test -tags=e2e -run 'TestCompanyRecon_E2E' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

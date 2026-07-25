#!/usr/bin/env bash
set -uo pipefail

# Permanent regression gate for v1.45.0 "Academic Research Depth" (see issue
# #413 and its tracked issues #267, #268, #269, #270, #283, #284, #286, #318,
# #323, #352). Runs 6 independent gates in order; fails loud (non-zero exit)
# on the first gate that fails. Re-run after every change to
# internal/search/{core,pubmed,monarch,ct_logs,wayback_cdx,router}.go,
# internal/scraper/{jina,youtube,pipeline}.go,
# internal/tools/{paper_fulltext,monarchsearch,syllabus_search,gag_order_search,company_recon}.go,
# lenses/{biomed,curriculum}.json, or their supporting tests.
#
# Usage: bash scripts/harness-413.sh

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

run_gate "SAST (gosec) + pattern checks (rules 2.1/2.2/2.3/2.4/2.5, 4.1/4.4)" \
  "$OUT_DIR/02-sast.log" \
  bash -c '
    set -e
    make sec
    make vuln
    echo "--- grep: no http.DefaultClient in new provider/scraper files (rule 2.4) ---"
    ! grep -rln "http\.DefaultClient" internal/search/core.go internal/search/monarch.go internal/search/ct_logs.go internal/search/wayback_cdx.go internal/scraper/jina.go 2>/dev/null
    echo "--- grep: io.LimitReader present on every new response body (rule 4.1) ---"
    for f in internal/search/core.go internal/search/monarch.go internal/scraper/jina.go; do
      if [ -f "$f" ]; then
        grep -q "io.LimitReader(resp.Body" "$f" || { echo "missing io.LimitReader cap in $f"; exit 1; }
      fi
    done
    echo "--- grep: CURIE/domain input validated before URL interpolation (rule 2.2) ---"
    for f in internal/search/monarch.go internal/search/ct_logs.go internal/search/wayback_cdx.go; do
      if [ -f "$f" ]; then
        grep -qE "regexp\.MustCompile|url\.PathEscape|url\.Values\{\}" "$f" || { echo "missing input-validation/escaping pattern in $f"; exit 1; }
      fi
    done
    echo "--- grep: no bearer/API key literals or key values in log/error strings (rule 2.3) ---"
    ! grep -rnE "(Sprintf|Errorf|log\.).*\b(APIKey|apiKey|Bearer)\b.*%s" internal/search/core.go internal/search/monarch.go internal/scraper/jina.go internal/tools/syllabus_search.go internal/tools/gag_order_search.go 2>/dev/null
    echo "--- grep: no TODO/FIXME left in new v1.45.0 files ---"
    ! grep -rn "TODO\|FIXME" internal/search/core.go internal/search/monarch.go internal/search/ct_logs.go internal/search/wayback_cdx.go internal/scraper/jina.go internal/tools/paper_fulltext.go internal/tools/monarchsearch.go internal/tools/syllabus_search.go internal/tools/gag_order_search.go internal/tools/company_recon.go 2>/dev/null
  '

# internal/tools is deliberately excluded here: tool handlers hold zero
# per-instance state (Design Rule 1 — deps flow through Dependencies,
# constructed once in main.go), so no sub-issue's isolation rule requires a
# dedicated multi-instance test there; the pattern below targets the
# provider/router state that rule 1.1-1.3 actually concerns.
run_gate_requiring_tests "Multi-instance isolation tests (rule 1.1/1.2/1.3)" \
  "$OUT_DIR/03-isolation.log" \
  go test ./internal/search/... ./internal/scraper/... -run 'TestMultiInstance.*|TestAvailableAcademicProviders|TestAvailableProviders|TestConcurrentAccess|TestRouter_WebPerProviderCap_DoesNotMutateOriginalParams' -v -count=1

run_gate "Dead-code scan (go vet + staticcheck via lint)" \
  "$OUT_DIR/04-deadcode.log" \
  go vet ./...

run_gate "Unit/integration tests (Phase-1 rules 2-6)" \
  "$OUT_DIR/05-unit.log" \
  go test -race ./internal/search/... ./internal/scraper/... ./internal/tools/... ./internal/config/... ./internal/resources/... -count=1

run_gate_requiring_tests "E2E (new tools reachable end-to-end, recorded proof)" \
  "$OUT_DIR/06-e2e.log" \
  go test -tags=e2e -run 'TestPaperFulltext|TestMonarchSearch|TestCompanyRecon|TestSyllabusSearch|TestGagOrderSearch' ./tests/e2e/... -v -count=1

echo
if [ "$FAILED" -ne 0 ]; then
  echo "HARNESS FAILED — see logs under ${OUT_DIR#"$REPO_ROOT"/}/"
  exit 1
fi

echo "HARNESS PASSED — all 6 gates green. Logs under ${OUT_DIR#"$REPO_ROOT"/}/"
exit 0

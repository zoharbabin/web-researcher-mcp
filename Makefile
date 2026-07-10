.PHONY: build build-fips sync-lenses test test-race test-cover test-e2e test-live test-eval test-geo-eval test-concurrency test-bench test-python test-python-live \
        lint fmt fmt-check vet vuln sec tools hooks precommit verify clean run dev docker docker-smoke release version-sync rebuild-local help all \
        gen-python-client check-python-drift

BINARY = web-researcher-mcp
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -ldflags="-s -w -X main.version=$(VERSION)"

# Lint and vuln tools are pinned in go.mod via `tool` directives and invoked
# through `go tool`, so every contributor and CI run uses byte-identical
# versions with zero manual install. See `make tools`.
GOLANGCI = go tool golangci-lint
GOVULN   = go tool govulncheck
GOSEC    = go tool gosec

# gosec tuning (kept signal-only): G104 is excluded repo-wide because
# golangci-lint's errcheck covers unhandled errors more precisely; the tests
# directory is excluded (fixtures legitimately exercise "unsafe" patterns).
# Genuinely-safe production sites carry inline `// #nosec <rule> -- <reason>`.
GOSEC_FLAGS = -exclude=G104 -exclude-dir=tests -quiet

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) ./cmd/web-researcher-mcp

build-fips:
	GOEXPERIMENT=boringcrypto CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) ./cmd/web-researcher-mcp

# --- Tests ------------------------------------------------------------------

# Default unit/integration tests. Fast; safe to run constantly.
test:
	go test ./...

# Race detector across everything. Run before every push and in CI.
test-race:
	go test -race -count=1 ./...

test-cover:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html

# E2E suite drives the real binary over STDIO; needs the `e2e` build tag.
test-e2e:
	go test -tags=e2e -count=1 ./tests/e2e/...

# Live external-API integration tests (EPO, CrossRef, OpenAlex). Opt-in only:
# they depend on third-party endpoints and are non-deterministic, so they are
# excluded from the default suite and CI. Provide the relevant provider
# credentials in the environment before running.
test-live:
	go test -tags=live -count=1 ./internal/search/...

# Labeled accuracy eval for the trust suite (#180): runs verify_citation +
# audit_bibliography against a curated gold set of fabricated/retracted/real/
# mischaracterized citations and reports precision/recall per signal. Opt-in
# (live, network + CROSSREF_EMAIL); the eval fails on any FALSE POSITIVE
# (mislabeling a legitimate source) — the unacceptable error for a trust tool.
test-eval:
	go test -tags=live -count=1 -v -run TestTrustSuiteAccuracy ./internal/tools/...

# GEO-defense eval suite (arXiv:2607.05217): empirically tests the MCP against
# the paper's documented open-web-search failure modes — hard site: scoping
# vs. soft prompt-steering, fluency-blind domain reputation, claim
# corroboration tallying, and never-fabricate-on-zero-results. Eval 1/5 need a
# live provider (DuckDuckGo) so this target carries the `live` tag; Eval 2/3/4
# are hermetic and would also run under plain `make test`. One target runs the
# whole suite together for a single "does our trust story hold up" check.
# See the suite-level map in internal/tools/geo_eval_reputation_test.go.
test-geo-eval:
	go test -tags=live -count=1 -v -run TestGeoEval ./internal/search/... ./internal/tools/...

# Concurrency-focused tests (shared-state contention). Always on: they are
# bounded (a few seconds) and only meaningful under -race.
test-concurrency:
	go test -race -count=1 -run 'Concurren|Parallel|Contention' \
		./internal/persist/... ./internal/session/... ./internal/ratelimit/... ./internal/auth/...

test-bench:
	go test -bench=. -benchmem ./tests/benchmark/

# Python client library tests (no binary required — uses a mock HTTP server)
test-python:
	python3 -m pytest tests/python/ --ignore=tests/python/test_live_e2e.py -v 2>&1 || python3 -m unittest discover -s tests/python -v

# Python live E2E tests — build the real Go binary, start it, and exercise
# the SDK against real external APIs. Keyless providers (DuckDuckGo, PubMed,
# World Bank, ClinicalTrials.gov) run unconditionally; keyed tests skip when
# their env var is absent. Requires Go on PATH and internet access.
test-python-live:
	python3 -m pytest tests/python/test_live_e2e.py -v

# Regenerate the Python client from the live Go tool schemas.
gen-python-client:
	go run ./cmd/gen-python-client | python3 scripts/gen_python_client.py

# Fail if the generated Python client is stale (use in CI / pre-commit).
check-python-drift:
	go run ./cmd/gen-python-client | python3 scripts/gen_python_client.py --dry-run

# --- Quality gates ----------------------------------------------------------

fmt:
	gofmt -s -w .

# Fail if anything is not gofmt-clean (CI + pre-commit use this).
fmt-check:
	@unformatted=$$(gofmt -s -l $$(git ls-files '*.go')); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean (run 'make fmt'):"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

lint:
	$(GOLANGCI) run --timeout=5m

vet:
	go vet ./...

vuln:
	$(GOVULN) ./...

# Go security scanner (command/SQL injection, weak crypto, SSRF sinks, unsafe
# file ops). Complements golangci-lint + CodeQL; tuned to be signal-only.
sec:
	$(GOSEC) $(GOSEC_FLAGS) ./...

# --- Developer setup --------------------------------------------------------

# Materialize the pinned tools into the build cache (optional; `go tool`
# fetches on first use anyway). Run once after cloning.
tools:
	go mod download
	$(GOLANGCI) version
	$(GOVULN) -version
	$(GOSEC) -version

# Install the repo's git hooks (one command; idempotent).
hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks enabled (.githooks). Pre-commit will run fmt-check + lint on staged Go files."

# --- Aggregate gates --------------------------------------------------------

# Fast pre-commit gate (what the hook runs): formatting + lint + vet + unit tests.
# Deliberately excludes vuln (network) and -race (slow) to keep commits snappy.
precommit: fmt-check vet lint test

# Validate every bundled lens JSON against the schema validator (search.ValidateLens),
# so a malformed/typo'd lens that would silently fail to restrict a search is caught
# in CI rather than at runtime. Exercised via the search package's lens tests.
validate-lenses:
	go test ./internal/search/ -run 'TestBundledLensesValid|TestValidateLens' -count=1

# Regenerate the embedded lens copy from the canonical root lenses/ dir. The
# binary embeds these so lenses work from any CWD / install method (uvx, go
# install). Run after adding/editing a root lens; CI's TestEmbeddedLensesMatchRoot
# fails if the embedded copy drifts from root.
sync-lenses:
	cp lenses/*.json internal/search/lenses_embed/
	@echo "synced $$(ls internal/search/lenses_embed/*.json | wc -l | tr -d ' ') lenses into the embed"

# Full verification, matching CI. Run before opening a PR.
verify: fmt-check vet lint sec vuln validate-lenses test-race test-e2e check-python-drift test-python build

clean:
	rm -f $(BINARY) coverage.out coverage.html

run: build
	./$(BINARY)

dev:
	air

docker:
	docker build -t $(BINARY):$(VERSION) .

# Build the shipped image and validate it serves MCP over HTTP end-to-end
# (initialize + web_search) with PORT set and no stdin — the regression guard
# for the HTTP-lifecycle fix. No API keys (DuckDuckGo zero-config fallback).
docker-smoke:
	bash scripts/docker-smoke.sh $(BINARY):smoke

release:
	goreleaser release --snapshot --clean

version-sync:
	bash scripts/sync-version.sh

# Deterministic local rebuild for IRL testing: clears the Go build cache + the
# MCP response cache (NOT sessions/persist personal data), rebuilds from
# scratch, and reinstalls over the binary on PATH using the macOS-SIGKILL-safe
# rm+cp+codesign sequence. Override INSTALL_PATH / CACHE_DIR as needed; pass
# ARGS="--no-install" to build without installing.
rebuild-local:
	bash scripts/rebuild-local.sh $(ARGS)

help:
	@grep -E '^[a-zA-Z_-]+:.*' Makefile | grep -v '^\.PHONY' | sort | \
		awk -F: '{printf "  %-18s\n", $$1}'

all: verify

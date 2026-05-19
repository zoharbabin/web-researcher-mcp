# CLAUDE.md — web-researcher-mcp

## Project Overview

An MCP (Model Context Protocol) server providing web search, content extraction, and multi-source research tools for AI assistants. Built in Go.

## Quick Commands

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp    # Build
go test ./...                                      # All tests
go test -race ./...                                # Tests with race detector
go test -v ./tests/e2e/...                         # E2E tests only
go test -bench=. ./tests/benchmark/                # Benchmarks
golangci-lint run                                  # Lint
govulncheck ./...                                  # Security audit
```

## Architecture

```
cmd/web-researcher-mcp/main.go          # Entry point (wiring only)
internal/
├── config/                 # Env-based configuration
├── server/                 # MCP server lifecycle
├── tools/                  # Tool handlers (one file per tool)
├── search/                 # Pluggable search providers (Brave, Google, Serper, SearXNG)
├── scraper/                # Tiered scraping pipeline (markdown → stealth → HTML → browser)
├── documents/              # PDF, DOCX, PPTX parsing
├── cache/                  # Hybrid cache (memory + disk + optional Redis)
├── auth/                   # OAuth 2.1 middleware (JWT/JWKS)
├── audit/                  # Structured audit logging
├── session/                # Per-tenant session management
├── content/                # Sanitize, dedup, truncate, quality score
├── metrics/                # Prometheus metrics + per-tool stats
├── ratelimit/              # Three-tier rate limiting
├── circuit/                # Circuit breaker
└── resources/              # MCP Resources + Prompts
lenses/                     # Search lens JSON files (curated domain lists)
mcpb/manifest.json          # Claude Desktop .mcpb bundle template
scripts/build-mcpb.sh       # Builds .mcpb bundles for all platforms (CI)
.mcp.json                   # Claude Code / Cursor project config
.vscode/mcp.json            # VS Code / GitHub Copilot config
server.json                 # Official MCP Registry manifest
smithery.yaml               # Smithery.ai marketplace config
```

## MCP Tools

- `web_search` — Web search (supports search lenses)
- `scrape_page` — URL content extraction (web, PDF, DOCX, YouTube)
- `search_and_scrape` — Combined pipeline with quality scoring
- `image_search` — Image search with filters
- `news_search` — News search with freshness
- `academic_search` — Scholar/arXiv/PubMed search
- `patent_search` — Patent database search
- `sequential_search` — Multi-step research tracking

## Environment Variables

Required:
- `GOOGLE_CUSTOM_SEARCH_API_KEY` — Google API key
- `GOOGLE_CUSTOM_SEARCH_ID` — Search engine ID

Search Provider:
- `SEARCH_PROVIDER` — brave | google | serper | searxng (default: google)
- `BRAVE_API_KEY` — Brave Search API key

HTTP Transport:
- `PORT` — Enables HTTP mode (default: STDIO only)
- `OAUTH_ISSUER_URL` — JWT issuer
- `OAUTH_AUDIENCE` — Expected audience

## Design Principles

1. Zero global state — all deps injected
2. Interface-driven — every external dep behind an interface
3. Bounded concurrency — explicit semaphores
4. Defense in depth — SSRF, rate limiting, content sanitization
5. Fail loud — return errors, validate at boundaries

## Release & Distribution

On `v*` tag push, CI automatically:
1. Builds cross-platform binaries via GoReleaser
2. Pushes Docker images to GHCR + Docker Hub (multi-arch)
3. Builds `.mcpb` bundles for all platforms (Claude Desktop)
4. Attaches all artifacts + SBOM to GitHub Release
5. Signs artifacts with cosign

Distribution configs (auto-included in release archives):
- `.mcp.json` — Claude Code / Cursor
- `.vscode/mcp.json` — VS Code / GitHub Copilot
- `server.json` — Official MCP Registry
- `smithery.yaml` — Smithery.ai marketplace

## Key Docs

- `ARCHITECTURE.md` — Full architecture overview
- `CONTRIBUTING.md` — Code style, workflow, adding tools/providers
- `docs/TOOLS.md` — Tool specifications
- `docs/SECURITY.md` — Security architecture
- `docs/SEARCH_PROVIDERS.md` — Provider system + lenses
- `docs/DEPLOYMENT.md` — Build, Docker, Kubernetes, client configs
- `docs/TESTING.md` — Test strategy and patterns
- `docs/COMPLIANCE.md` — SOC2, GDPR, FedRAMP
- `docs/GO_MODULE.md` — Dependencies with rationale
- `docs/SPECIFICATIONS.md` — Config struct, error types, CI/CD, Resources/Prompts

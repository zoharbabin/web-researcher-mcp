# From Node.js to Go: Rebuilding an MCP Server for Production

This is the story of why I rebuilt [google-researcher-mcp](https://github.com/zoharbabin/google-researcher-mcp) (Node.js/TypeScript) from scratch as [web-researcher-mcp](https://github.com/zoharbabin/web-researcher-mcp) (Go), and what the lessons learned along the way.

## The Starting Point

The original project — `google-researcher-mcp` — was a TypeScript/Node.js MCP server distributed via npm. It had real traction: GitHub stars, steady npm downloads, a broad test suite, and active users. But five critical issues kept surfacing that couldn't be solved within the existing architecture.

## Why Rewrite in Go (Not Refactored)

### Orphan Processes (Issue #108)

npx spawns deeply nested process trees. When the parent MCP client (Claude Desktop, Cursor) crashes or closes unexpectedly, the Node.js process doesn't receive a signal — it keeps running, consuming memory and holding file locks.

Myself and collaborators spent three versions (v6.2.0 through v6.4.0) building increasingly complex orphan detection: a Worker thread watchdog with CPU spin detection, three-layer parent-alive checks, and graceful degradation. It was all band-aids on a fundamental runtime limitation.

**Go fix**: A single static binary. No runtime process tree. EOF on stdin = immediate exit. The entire problem category disappeared.

### Google Discontinuing "Entire Web" Search (Issue #107)

Google announced it would be discontinuing support for Programmable Search Engines configured to search the "entire web." The project was named `google-researcher-mcp` — the dependency on a single search provider was an foundational risk.

**Go fix**: A clean provider interface that lets you swap search backends freely, plus a routing layer that automatically switches to the next provider when one fails.

### Alternative Search Engines (Issue #55)

Users wanted Brave, Bing (go figure), and other providers. But the TypeScript codebase was too tightly coupled to Google's API response format — the shared directory (41 files) made every change risky and far-reaching.

**Go fix**: A clean `Provider` interface — each adapter normalizes provider-specific responses to common types (`SearchResult`, `ImageResult`, `NewsResult`). Adding a new provider is one file implementing one interface.

### Redis Caching (Issue #72)

The in-memory cache was lost on every process restart — which happened frequently with npx-launched servers. The complex persistence manager offered four strategies (Periodic, WriteThrough, OnShutdown, Hybrid), but none reliably survived the volatile process lifecycle.

**Go fix**: A `cache.Cache` interface with a hybrid implementation: an in-memory layer over an AES-encrypted disk layer, plus an optional shared Redis tier in HTTP mode for cross-pod deployments. Simple, testable, and it never loses data because the disk layer persists across restarts.

### Monolithic Architecture (Issue #40)

The project had 100+ source files but a tightly coupled `shared/` directory with 41 files. Adding a single tool required touching 4+ documentation sections, and the import graph made refactoring perilous.

**Go fix**: One package per concern. Tool handlers are self-contained files. Adding a tool means writing one file and one line in the registry.

## What Changed Architecturally

| Aspect | Node.js (old) | Go (new) |
|--------|---------------|----------|
| Distribution | npm/npx (runtime required) | Single static binary |
| Memory | 430MB idle (80MB after optimization) | ~25MB baseline |
| Startup | 2-4 seconds (lazy imports) | <100ms |
| Process lifecycle | Worker thread watchdog | EOF detection, no orphans |
| Search providers | Google only | Multiple providers + fallback routing |
| Concurrency | Event loop + async/await | Goroutines + semaphores |
| Type safety | TypeScript + Zod | Go type system + struct tags |
| Testing | Jest test suite | Table-driven tests + race detector |
| Scraping | Playwright (heavy) | Multi-tier pipeline (lightweight first) |

## Key Lessons Learned

### 1. Don't Fight Your Runtime

Node.js process management is fundamentally fragile for long-lived servers launched via npx. The runtime doesn't support robust parent-death detection, and the nested process tree (npx → node → worker) makes signal propagation unreliable. We spent three versions building increasingly complex orphan detection. Go's single binary eliminated the entire category of problems.

**Takeaway**: If you're spending significant engineering effort working around your runtime's limitations, that's a signal to evaluate whether the runtime fits the problem.

> Side note: looking for a better runtime I looked into both Go and Rust (isn't Rust awesome!?). Go won primarily for its lightweight goroutines excelling at I/O-bound operations, and the official `modelcontextprotocol/go-sdk` is superbly maintained.

### 2. Interface-Driven Design Enables Fearless Extension

Adding Brave Search in the Go version was one file implementing one interface. In the Node.js version, the equivalent change would have touched 6+ files due to tightly coupled imports in the shared directory.

**Takeaway**: When you know extension is likely (new providers, new tools), invest in clean interfaces upfront. The interface is the specification; implementations are interchangeable.

### 3. Memory Matters for MCP Servers

MCP servers run alongside AI assistants on developer machines. They're always-on background processes. A 430MB idle memory footprint was unacceptable — users would notice and uninstall. Go's ~25MB baseline lets the server stay resident without impact.

**Takeaway**: For developer tools that run continuously, memory efficiency is a feature, not an optimization. Choose your runtime accordingly.

### 4. Caching Architecture Should Be Boring

The old project had four persistence strategies with complex heuristics for when to flush. The new one layers an in-memory cache over encrypted disk, with an optional shared Redis tier for multi-pod HTTP deployments. Each layer is simple and independently testable. No heuristics, no race conditions, no data loss.

**Takeaway**: Boring infrastructure is reliable infrastructure. If your caching layer needs its own debugging session, it's too complex.

### 5. Documentation Should Be Drift-Resistant

The old project required updating four separate documentation files per new tool. Inevitably, docs drifted from reality. The new project's test suite programmatically validates documentation claims — tool descriptions must mention alternatives, output schemas must match actual responses, and annotations must be consistent.

**Takeaway**: If documentation can be wrong without a test failing, it will eventually be wrong.

## What We Kept

The rewrite preserved the user-facing contract:

- **Same tools** with identical semantics and parameter names
- **Same MCP protocol** compatibility (Claude Desktop, Cursor, VS Code, any MCP client)
- **Same environment variables** (drop-in replacement for existing configs)
- **Same search lenses** (curated domain lists, identical JSON format)

What improved (without breaking backwards compatibility):

- OAuth 2.1 authentication for multi-client deployments
- Multi-tenancy with per-tenant session isolation
- Automatic failover between search providers when one goes down
- Prometheus metrics for observability
- Structured audit logging for compliance

## Results

Since launching the Go version:

- Zero orphan process reports (vs. recurring issue in Node.js version)
- Multiple search providers with automatic failover (vs. single provider)
- Multi-tier scraping pipeline that tries lightweight methods first (vs. Playwright-only)
- Sub-100ms cold startup (vs. 2-4 seconds)
- Production-ready: rate limiting, automatic failover, user isolation, and a compliance-ready audit trail

## Should You Rewrite?

Probably not. Most rewrites fail because they're motivated by developer preference ("I want to use a new language") rather than architectural necessity. Ours succeeded because:

1. The problems were **architectural**, not implementational — no amount of refactoring within Node.js would fix process orphaning
2. The user-facing contract was **well-defined** — MCP provides a clean protocol boundary
3. The scope was **bounded** — we knew exactly what the server needed to do
4. We had **comprehensive tests** on the old version to validate behavioral equivalence

If your problems are solvable within your current architecture, refactor. If they're fundamentally incompatible with your runtime or architecture, consider a rewrite — but only with clear success criteria and a well-defined boundary.

---

*This article covers the migration from [google-researcher-mcp](https://github.com/zoharbabin/google-researcher-mcp) to [web-researcher-mcp](https://github.com/zoharbabin/web-researcher-mcp). The new project is open source under MIT and works with any MCP-compatible AI assistant.*

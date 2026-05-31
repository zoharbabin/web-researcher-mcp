# Migrating from google-researcher-mcp

This guide is for users of the deprecated [`google-researcher-mcp`](https://github.com/zoharbabin/google-researcher-mcp) (TypeScript/npm) who want to switch to the new Go-based replacement.

---

> **WARNING: Never have both servers configured simultaneously.**
>
> Both projects register identical tool names (`web_search`, `scrape_page`, `search_and_scrape`, etc.). If both MCP servers are active at the same time, your AI assistant will see duplicate tools and behavior will be unpredictable. **Remove the old server entry BEFORE adding the new one.**

---

## What Changed

| Aspect | Old (`google-researcher-mcp`) | New (`web-researcher-mcp`) |
|--------|-------------------------------|----------------------------|
| Language | TypeScript / Node.js 20+ | Go (single static binary) |
| Install | `npx -y google-researcher-mcp` | `go install`, binary download, or Docker |
| Process model | npm spawns Node.js — orphan detection issues | Native binary — clean EOF/SIGPIPE lifecycle |
| Search backends | Google PSE only | Google PSE + Brave + Serper + SearXNG + SearchAPI.io (with multi-provider routing) |
| Caching | In-memory only | Hybrid (memory + AES-encrypted disk) |
| Architecture | Monolithic `server.ts` | Modular (one package per concern) |
| Binary size | ~200MB (Node.js + Chromium) | ~22MB standalone (Chromium optional, auto-downloaded) |

## Migration Steps

### 1. Remove the old server from your MCP client config

Delete the `google-researcher` entry from your MCP configuration file:

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):
```json
{
  "mcpServers": {
    "google-researcher": { ... }  // ← DELETE THIS ENTIRE BLOCK
  }
}
```

**Claude Code** (`~/.claude.json` or `.mcp.json`):
```json
{
  "mcpServers": {
    "google-researcher": { ... }  // ← DELETE THIS ENTIRE BLOCK
  }
}
```

### 2. Install the new binary

Choose one method:

```bash
# Option A: Go install (if you have Go installed)
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest

# Option B: Download pre-built binary
# Visit https://github.com/zoharbabin/web-researcher-mcp/releases

# Option C: Docker
docker pull zoharbabin/web-researcher-mcp:latest
```

### 3. Add the new server to your MCP config

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_EXISTING_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_EXISTING_CX"
      }
    }
  }
}
```

Your existing Google API keys work without any changes.

### 4. (Optional) Add alternative search providers

The new version supports multiple backends for unrestricted whole-web search:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_CX",
        "SEARCH_PROVIDER": "brave",
        "BRAVE_API_KEY": "BSA..."
      }
    }
  }
}
```

Supported providers: `google` (default), `brave`, `serper`, `searxng`, `searchapi`, `duckduckgo` (zero-config fallback, no API key). Canonical list: `search.SupportedProviders`.

You can also enable multi-provider routing with automatic fallback:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "SEARCH_ROUTING": "brave,google,serper",
        "BRAVE_API_KEY": "BSA...",
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_CX",
        "SERPER_API_KEY": "..."
      }
    }
  }
}
```

---

## Issues Resolved

All open issues from the old project are resolved in the new version:

| Issue | Problem | Resolution |
|-------|---------|------------|
| [#108](https://github.com/zoharbabin/google-researcher-mcp/issues/108) | Orphan detection fails via npx | Go binary runs directly — no intermediate npm process, clean EOF/SIGPIPE handling |
| [#107](https://github.com/zoharbabin/google-researcher-mcp/issues/107) | Google discontinuing 'entire web' search | Supports Brave, Serper, SearXNG for unrestricted search; Google PSE retained for lens queries |
| [#55](https://github.com/zoharbabin/google-researcher-mcp/issues/55) | Support alternative search engines | Built-in support for Brave Search, Serper.dev, and SearXNG |
| [#72](https://github.com/zoharbabin/google-researcher-mcp/issues/72) | Add distributed Redis caching | Hybrid cache: memory + AES-encrypted disk (`CACHE_DIR`, `CACHE_ENCRYPTION_KEY`) |
| [#40](https://github.com/zoharbabin/google-researcher-mcp/issues/40) | Split server.ts into modules | Fully modular: one package per concern, one file per tool |

---

## Tool Compatibility

The tool names and parameters are identical between old and new:

- `web_search` — same parameters (`query`, `num_results`, `time_range`, `lens`, `site`, etc.)
- `scrape_page` — same parameters (`url`, `max_length`, `mode`)
- `search_and_scrape` — same parameters (`query`, `num_results`, `deduplicate`, etc.)
- `image_search` — same parameters (`query`, `size`, `type`, `color_type`, etc.)
- `news_search` — same parameters (`query`, `freshness`, `news_source`, etc.)
- `academic_search` — same parameters (`query`, `source`, `year_from`, `year_to`, etc.)
- `patent_search` — same parameters (`query`, `patent_office`, `cpc_code`, etc.)
- `sequential_search` — same parameters (`searchStep`, `stepNumber`, `nextStepNeeded`, etc.)

No changes needed in your prompts or workflows.

---

## Upgrade Notes (within web-researcher-mcp)

These notes apply when upgrading between versions of `web-researcher-mcp` itself.

### Disk cache is cleared once on first run of this version

The on-disk cache format changed (encrypted blobs now bind their key as GCM additional authenticated data). The internal cache `Version` was bumped so the cache is **invalidated and cleared once** on the first run of this version. This is automatic and safe — there is nothing to do. Cached entries simply repopulate on demand; no configuration, keys, or data outside the cache are affected.

### Planned change: CORS default will flip to fail-closed

Today, in HTTP mode, an empty `ALLOWED_ORIGINS` reflects any `Origin` (permissive) when `CORS_STRICT=false` (the current default). A **future release will flip the default of `CORS_STRICT` to `true`**, so an empty `ALLOWED_ORIGINS` will deny all cross-origin requests (fail-closed). No date is set for this change.

**What to do now to be unaffected by the flip:**

- If you rely on cross-origin browser access, set `ALLOWED_ORIGINS` to the explicit origins you trust (recommended), e.g. `ALLOWED_ORIGINS=https://app.example.com`.
- Alternatively, to keep the current permissive behavior after the flip, set `CORS_STRICT=false` explicitly.
- STDIO mode is unaffected — there is no HTTP server and no CORS handling.

Setting `ALLOWED_ORIGINS` explicitly is the durable, recommended configuration: it behaves identically before and after the flip.

---

## Need Help?

- New project: https://github.com/zoharbabin/web-researcher-mcp
- Issues: https://github.com/zoharbabin/web-researcher-mcp/issues
- Discussions: https://github.com/zoharbabin/web-researcher-mcp/discussions

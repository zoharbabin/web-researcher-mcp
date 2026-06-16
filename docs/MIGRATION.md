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
| Install | `npx -y google-researcher-mcp` | `uvx web-researcher-mcp` (one-line swap), Homebrew, binary download, or Docker |
| Process model | npm spawns Node.js — orphan detection issues | Native binary — clean EOF/SIGPIPE lifecycle |
| Search backends | Google PSE only | Google PSE plus multiple alternatives (Brave, Serper, SearXNG, SearchAPI, Tavily, Exa) and a zero-config DuckDuckGo fallback, with multi-provider routing — canonical list: `search.SupportedProviders` |
| Caching | In-memory only | Hybrid (memory + AES-encrypted disk) |
| Architecture | Monolithic `server.ts` | Modular (one package per concern) |
| Binary size | ~200MB (Node.js + Chromium) | Single static binary, no runtime bundled (Chromium optional; auto-detects a local install) |

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

### 2. Install the new server

Coming from `npx google-researcher-mcp`, the **lowest-friction path needs no new runtime** — the installer drops a single signed binary on your PATH and (when the `claude` CLI is present) registers it with Claude Code automatically:

```bash
# macOS / Linux — one line, no Node, no Python, no compile:
curl -fsSL https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.sh | sh

# Windows (PowerShell):
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.ps1 | iex"
```

**Python users** — if you have (or want) [`uv`](https://docs.astral.sh/uv/), the `npx`→`uvx` shape is a direct swap (it installs `uv` once, then fetches the prebuilt binary):

```bash
uvx web-researcher-mcp                 # run on demand
uv tool install web-researcher-mcp     # or install as a persistent tool
pip install web-researcher-mcp         # or via pip
```

Prefer a package manager or container? Any of these also work:

```bash
# Homebrew (macOS)
brew install zoharbabin/tap/web-researcher-mcp

# Pre-built binary — https://github.com/zoharbabin/web-researcher-mcp/releases

# Docker
docker pull zoharbabin/web-researcher-mcp:latest

# Go install (if you have Go)
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
```

### 3. Add the new server to your MCP config

The config is the **same shape** as the old server — only the `command` changes. The simplest, using `uvx`:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "uvx",
      "args": ["web-researcher-mcp"],
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_EXISTING_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_EXISTING_CX"
      }
    }
  }
}
```

If you installed the binary directly (Homebrew / release download), use `"command": "web-researcher-mcp"` with no `args` instead. **Your existing Google API keys work without any changes.**

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

Supported providers: `google` (default), `brave`, `serper`, `searxng`, `searchapi`, `tavily`, `exa`, `duckduckgo` (zero-config fallback, no API key), `hackernews` (HN Algolia index, no API key). Canonical list: `search.SupportedProviders`.

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
| [#107](https://github.com/zoharbabin/google-researcher-mcp/issues/107) | Google discontinuing 'entire web' search | Supports multiple whole-web providers (Brave, Serper, SearXNG, SearchAPI, Tavily, Exa, DuckDuckGo) for unrestricted search; Google PSE retained for lens queries |
| [#55](https://github.com/zoharbabin/google-researcher-mcp/issues/55) | Support alternative search engines | Built-in support for several alternative engines (Brave, Serper, SearXNG, SearchAPI, Tavily, Exa) plus zero-config DuckDuckGo |
| [#72](https://github.com/zoharbabin/google-researcher-mcp/issues/72) | Add distributed Redis caching | Hybrid cache: memory + AES-encrypted disk (`CACHE_DIR`, `CACHE_ENCRYPTION_KEY`) |
| [#40](https://github.com/zoharbabin/google-researcher-mcp/issues/40) | Split server.ts into modules | Fully modular: one package per concern, one file per tool |

---

## Tool Compatibility

The tool names and parameters are identical between old and new:

- `web_search` — same parameters (`query`, `num_results`, `time_range`, `lens`, `site`, etc.)
- `scrape_page` — same parameters (`url`, `max_length`, `mode`)
- `search_and_scrape` — same parameters (`query`, `num_results`, `deduplicate`, etc.)
- `image_search` — same parameters (`query`, `size`, `type`, `color_type`, etc.)
- `news_search` — same parameters (`query`, `time_range`, `news_source`, etc.)
- `academic_search` — same parameters (`query`, `source`, `year_from`, `year_to`, etc.)
- `patent_search` — same parameters (`query`, `patent_office`, `cpc_code`, etc.)
- `sequential_search` — same parameters (`searchStep`, `stepNumber`, `nextStepNeeded`, etc.)

No changes needed in your prompts or workflows.

---

## Need Help?

- New project: https://github.com/zoharbabin/web-researcher-mcp
- Issues: https://github.com/zoharbabin/web-researcher-mcp/issues
- Discussions: https://github.com/zoharbabin/web-researcher-mcp/discussions

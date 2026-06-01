# Installing web-researcher-mcp

This guide is designed for AI coding agents (Cline, Claude Code, Cursor) to autonomously install and configure web-researcher-mcp.

## What this is

An MCP server that gives AI assistants web search, content extraction, academic paper lookup, patent search, and multi-step research capabilities — with real, verifiable citations.

## Prerequisites

One of:
- macOS (arm64 or amd64), Linux (amd64 or arm64), or Windows (amd64)
- OR Docker installed
- OR Go (version per `go.mod`) installed

## Option A: Binary Install (recommended)

1. Download the latest release for your platform:
   ```
   https://github.com/zoharbabin/web-researcher-mcp/releases/latest
   ```
   Files are named: `web-researcher-mcp_<version>_<os>_<arch>.tar.gz`

2. Extract and place the binary in your PATH (e.g., `~/.local/bin/` or `/usr/local/bin/`)

3. Add to your MCP client configuration. The `env` block is optional — omit it entirely to run zero-config with the DuckDuckGo fallback, or add a provider key for better results:

**Claude Code** (`~/.claude.json`):
```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_API_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_SEARCH_ENGINE_ID"
      }
    }
  }
}
```

**Cursor** (Settings > MCP Servers):
```json
{
  "web-researcher": {
    "command": "web-researcher-mcp",
    "env": {
      "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_API_KEY",
      "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_SEARCH_ENGINE_ID"
    }
  }
}
```

**Cline** (MCP Settings):
```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_API_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_SEARCH_ENGINE_ID"
      }
    }
  }
}
```

## Option B: Docker

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "docker",
      "args": ["run", "-i", "--rm",
        "-e", "GOOGLE_CUSTOM_SEARCH_API_KEY=YOUR_API_KEY",
        "-e", "GOOGLE_CUSTOM_SEARCH_ID=YOUR_SEARCH_ENGINE_ID",
        "ghcr.io/zoharbabin/web-researcher-mcp:latest"
      ]
    }
  }
}
```

The Docker image bundles Chromium (with `CHROME_PATH` preset), so JavaScript-heavy pages render out of the box with no extra download.

## Option C: Go Install

```bash
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
```

Then configure as in Option A.

## Environment Variables

**None required.** With no configuration, the server uses DuckDuckGo as a zero-config fallback search provider (no API key needed). The variables below are optional upgrades for higher quality and image/news search.

**Recommended — Google (best quality whole-web + image + news):**

| Variable | Description | Get it at |
|----------|-------------|-----------|
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Google Custom Search API key | https://console.cloud.google.com |
| `GOOGLE_CUSTOM_SEARCH_ID` | Google Programmable Search Engine ID | https://programmablesearchengine.google.com |

**Or use an alternative provider:**

| Variable | Description | Get it at |
|----------|-------------|-----------|
| `BRAVE_API_KEY` | Brave Search API key | https://brave.com/search/api/ |
| `SERPER_API_KEY` | Serper.dev API key | https://serper.dev |
| `SEARCHAPI_API_KEY` | SearchAPI.io API key | https://searchapi.io |
| `SEARXNG_URL` | Self-hosted SearXNG instance URL | https://docs.searxng.org |

**Optional:**

| Variable | Description |
|----------|-------------|
| `SEARCH_PROVIDER` | Which provider to use: `google`, `brave`, `serper`, `searxng`, `searchapi`, `duckduckgo` (defaults to `google`, falling back to `duckduckgo` when no key is set) |
| `SEARCH_ROUTING` | Multi-provider fallback list (e.g., `brave,google,serper`) |

## Available Tools

Once configured, the always-on core tools include web search, full-page/document scraping (`mode: raw` for verbatim source), combined search-and-scrape with quality ranking, image and news search, academic search (real DOIs), patent search (US/EP/WO/JP/CN/KR), and multi-step `sequential_search` with recoverable sessions.

Operators can additionally enable opt-in, consent-gated tools (per-user analytics, long-term memory, shared workspaces) that register only when their feature is turned on.

See [`docs/TOOLS.md`](docs/TOOLS.md) for the authoritative, CI-verified list with full parameter and output schemas (`internal/tools/registry.go` is the source of truth).

## Verification

After configuration, test by asking your AI assistant:
```
Search for recent news about AI regulation
```

The assistant should invoke `news_search` and return results with real, clickable source URLs.

## Troubleshooting

- **"command not found"** — Ensure the binary is in your PATH or use the full path in the config
- **"invalid API key"** — Verify your Google API key is enabled for Custom Search API
- **No results** — Check that your Search Engine ID (cx) is configured to search the entire web
- **Timeout errors** — Each scrape tier has its own bounded timeout (a few seconds up to ~30s for browser rendering); slow or JavaScript-heavy sites may hit these and fall through to the next tier

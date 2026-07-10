---
description: Clear caches, rebuild from scratch, and reinstall the local binary (no LLM roundtrips)
allowed-tools: Bash(bash scripts/rebuild-local.sh*), Bash(make rebuild-local*)
---

Run the deterministic local rebuild script and report its output verbatim. This does the whole clear-cache → rebuild → reinstall flow in one shell call with no further analysis or tool roundtrips.

Default (clear + build + install to every location the binary might be invoked from — the `~/.claude.json`-configured path plus every distinct `web-researcher-mcp` found on `$PATH`):

!`bash scripts/rebuild-local.sh $ARGUMENTS`

After it finishes, state the installed version and every install path from the script output, and remind the user to restart their MCP client(s) to load the new binary. Do not run any additional commands unless the script reports an error.

Useful `$ARGUMENTS` to pass after `/rebuild-local`:

- `--no-install` — build only, skip the install step
- `--keep-build-cache` — skip `go clean -cache` (faster incremental build)
- `--help` — print the script's full usage/env-override reference
- `INSTALL_PATH=/some/path` (env var, not an argument) — install to exactly that one path only, skipping auto-discovery of every other location

The script preserves personal data (`sessions/`, `persist/`) and only clears the response cache (`*.cache` + `.version`).

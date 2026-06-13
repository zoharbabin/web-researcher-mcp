---
description: Clear caches, rebuild from scratch, and reinstall the local binary (no LLM roundtrips)
allowed-tools: Bash(bash scripts/rebuild-local.sh*), Bash(make rebuild-local*)
---

Run the deterministic local rebuild script and report its output verbatim. This does the whole clear-cache → rebuild → reinstall flow in one shell call with no further analysis or tool roundtrips.

Default (clear + build + install over the binary on PATH):

!`bash scripts/rebuild-local.sh $ARGUMENTS`

After it finishes, state the installed version and path from the script output, and remind the user to restart their MCP client to load the new binary. Do not run any additional commands unless the script reports an error.

Useful `$ARGUMENTS` to pass after `/rebuild-local`:

- `--no-install` — build only, skip the install step
- `--keep-build-cache` — skip `go clean -cache` (faster incremental build)
- `--help` — print the script's full usage/env-override reference

The script preserves personal data (`sessions/`, `persist/`) and only clears the response cache (`*.cache` + `.version`).

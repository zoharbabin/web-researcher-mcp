# PR: Add web-researcher-mcp to awesome-selfhosted

## Entry to add

Category: **Search Engines** (or **Miscellaneous** if no exact fit)

```markdown
- [web-researcher-mcp](https://github.com/zoharbabin/web-researcher-mcp) - MCP server providing AI assistants with web search, content extraction, and multi-source research capabilities. Multi-provider routing with automatic failover. `MIT` `Go` `Docker`
```

## Checklist (per CONTRIBUTING.md)

- [x] Software is actively maintained
- [x] Software has a stable release (v1.2.3)
- [x] Software is self-hostable (Docker, single binary)
- [x] Software is open-source (MIT license)
- [x] Software is not a SaaS or cloud-only product
- [x] Link points to the source code repository
- [x] Description is concise and explains what it does
- [x] Tags include license (`MIT`), language (`Go`), and `Docker`

## Why it fits awesome-selfhosted

web-researcher-mcp is a self-hosted research server that:
- Runs as a single binary or Docker container on your own infrastructure
- Supports SearXNG (a popular self-hosted search engine) as a provider
- Keeps all data local — no telemetry, no external dependencies beyond search APIs
- Provides 8 specialized research tools via the Model Context Protocol
- Supports multi-provider routing so users aren't locked into any single search API

## Submission command

```bash
# Fork awesome-selfhosted/awesome-selfhosted, then:
# Add entry alphabetically under "Search Engines" section
# PR title: "Add web-researcher-mcp"
# PR body: brief description + link to repo
```

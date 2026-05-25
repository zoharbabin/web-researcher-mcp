# VHS Demo Script: web-researcher-mcp in Claude Code
# Records a GIF showcasing the key value propositions of using this MCP server.
#
# Usage: vhs demo.tape
# Requirements: claude CLI installed, web-researcher-mcp configured as MCP server
#
# Output: demo.gif (for README, PH, Twitter), demo.mp4 (for YouTube/PH video)

Output demo.gif
Output demo.mp4

Set Shell "zsh"
Set FontFamily "JetBrains Mono"
Set FontSize 15
Set Width 1400
Set Height 800
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set WindowBar Colorful
Set WindowBarSize 40
Set CursorBlink true

# ─── Scene 1: Installation (show how easy it is) ────────────────────────────

Type "# Install web-researcher-mcp in seconds"
Enter
Sleep 500ms

Type "go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest"
Enter
Sleep 3s

Type "# ✓ Single binary, zero dependencies, 25MB memory"
Enter
Sleep 2s

Type "clear"
Enter
Sleep 500ms

# ─── Scene 2: Show Claude Code with web_search ──────────────────────────────

Type "# Let's use Claude Code with web research superpowers"
Enter
Sleep 1s

Type "claude"
Enter
Sleep 2s

Type "Search the web for the latest Model Context Protocol specification changes in 2026"
Enter
Sleep 8s

# Claude will call web_search and display results
Sleep 5s

Type "/clear"
Enter
Sleep 1s

# ─── Scene 3: search_and_scrape — the killer feature ────────────────────────

Type "Research how Go's new range-over-func feature works. Search and extract content from the best sources, with quality scoring."
Enter
Sleep 10s

# This shows multi-source extraction with dedup and quality scores
Sleep 8s

Type "/clear"
Enter
Sleep 1s

# ─── Scene 4: Academic research ─────────────────────────────────────────────

Type "Find recent academic papers on retrieval-augmented generation published in 2025-2026. Focus on arxiv and ACL."
Enter
Sleep 8s

# Shows academic_search with year filtering and source targeting
Sleep 5s

Type "/clear"
Enter
Sleep 1s

# ─── Scene 5: News search — real-time awareness ─────────────────────────────

Type "What are today's top news stories about AI regulation in the EU?"
Enter
Sleep 8s

# Shows news_search with freshness filtering
Sleep 5s

Type "/clear"
Enter
Sleep 1s

# ─── Scene 6: Search Lenses — domain-focused research ───────────────────────

Type "Using the programming lens, search for best practices for implementing circuit breakers in Go microservices"
Enter
Sleep 8s

# Shows lens-restricted search hitting only programming-quality sources
Sleep 5s

Type "/clear"
Enter
Sleep 1s

# ─── Scene 7: Multi-step research session ───────────────────────────────────

Type "I need a comprehensive analysis of WebAssembly adoption in server-side applications. Start with current state, then look at performance benchmarks, then identify key players and their approaches."
Enter
Sleep 12s

# Shows sequential_search with multi-step research tracking
Sleep 8s

Type "/exit"
Enter
Sleep 1s

# ─── Scene 8: Closing ───────────────────────────────────────────────────────

Type "# web-researcher-mcp: 8 tools, 5 providers, single binary"
Enter
Type "# github.com/zoharbabin/web-researcher-mcp"
Enter
Type "# MIT Licensed | Works with Claude, Cursor, and any MCP client"
Enter
Sleep 4s

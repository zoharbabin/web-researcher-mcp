# Brand Guidelines

How to represent web-researcher-mcp visually and verbally. Follow these guidelines when contributing to docs, creating assets, or writing about the project.

This covers voice, logo, color tokens, and typography. For the visual system governing marketing/info graphics (`assets/ph-gallery-*.svg`, layout patterns, component rules), see [Design Guidelines](DESIGN.md).

---

## Voice & Messaging

### Tagline

**Your AI research assistant. It searches, reads what you can open, and checks its sources.**

### Short description

Search the web and primary sources, read pages behind your logins, verify citations. Runs locally.

Max 100 characters (MCP registry limit). `TestMessagingSurfacesConsistent` checks that `server.json`, `glama.json`, `.claude-plugin/plugin.json` and `mcpb/manifest.json` start with this exact line. Change it here first, then sync those files.

### Supporting message

Search the web or narrow it to sites you trust. Reach primary sources: SEC filings, court records, patents, clinical trials, full-text papers, and community threads. Read the whole page, not a snippet, including pages behind your own login, such as LinkedIn or X (opt-in, encrypted). Check every citation before you rely on it: does it exist, was it retracted, does the link work, does the page say what is claimed. Keep your research across chats, watch topics for new results, and share findings with a team. Runs on your machine. No API key needed to start.

### Five positioning pillars

Trust is one pillar of five. Do not lead every surface with citations alone.

1. **Search everywhere you would look** — Web search with lenses for sites you trust, plus filings, courts, patents, trials, economic data, full-text papers and community threads. Works with no API key.
2. **Works as you** — Optional saved logins (`site_login`, opt-in, encrypted, per user) let the AI read pages you can already open: LinkedIn, X, Facebook, Instagram, YouTube and more. You sign in yourself, 2FA included. The password is never saved, and `site_logout` removes the login.
3. **Verified, not asserted** — `verify_citation`, `audit_bibliography` and `verify_recommendation` check existence, retraction, dead links and mischaracterization. They return evidence, not a verdict. Accuracy is measured on a labeled gold set (`docs/TRUST_EVAL.md`).
4. **Keeps and watches** — `sequential_search` keeps a session, `research_panel` compares models, `research_export` writes citations in 5 formats, monitors flag new results, and opt-in memory and team workspaces carry findings forward.
5. **Yours to run** — Local binary or your own server. Free, open source (MIT). No account. Nothing is stored unless you turn it on.

### Claims we can back

| Say | Don't say |
|---|---|
| "Checks every citation" | "Never fabricates", "100% accurate" |
| "Evidence, not a verdict" | "Proves a source is real" |
| "Runs on your machine. Your queries go only to the search providers you pick." | "Nobody ever sees your searches" |
| "Reads pages behind your own login, opt-in, encrypted" | "Bypasses paywalls", "gets around bot protection", "scrapes LinkedIn at scale" |
| "Some sites, such as LinkedIn, forbid automated access in their terms. Use it where yours allow." (state this in the section that explains saved logins) | Naming a site without that note |
| A stat with its source and date | A stat without a link |
| "Works with any MCP client" | A client name we have not tested |

### Tone rules

- Write for a grad student or journalist first, developers second
- Lead with the problem users feel, not the solution's architecture
- Use first-person pain language: "If I cite a case that doesn't exist, I get fined $50,000"
- Explain features by what they mean for the user, not how they work
- Protocol names (MCP, STDIO, OAuth) belong in setup sections, never in above-the-fold messaging
- Wrap developer-only content in `<details>` tags in public-facing docs
- No buzzwords: avoid "multi-provider fallback", "circuit breakers", "STDIO transport" in user-facing copy

---

## Logo & Mark

The logo is an abstract geometric mark — a faceted prism/lens shape in indigo and cyan. It evokes precision, synthesis, and intelligence without resorting to literal depictions of search or web.

### Design principles

| Principle | Requirement |
|-----------|-------------|
| Simple | Describable in one sentence, sketchable from memory |
| Memorable | Distinctive shape recognizable after 3 seconds |
| Timeless | No trendy effects (glassmorphism, 3D, complex gradients) |
| Versatile | Works at 16px favicon AND 1200px, in one color, on dark AND light |
| Appropriate | Matches the audience without being a tech cliche |

### What to avoid in visual identity

| Don't | Why |
|-------|-----|
| Magnifying glasses, globes, or "search" imagery | Cliche, indistinguishable from every other tool |
| Neural networks, brains, or AI cliches | Doesn't differentiate us |
| More than 2 colors in the mark | Scaling issues, visual noise |
| Fine lines or small internal details | Disappear at favicon sizes |
| Trendy effects (glassmorphism, 3D) | Date quickly |

---

## Color System

| Role | Color | Hex | Usage |
|------|-------|-----|-------|
| Primary | Deep Indigo | `#4F46E5` | Main brand color, logo primary |
| Accent | Cyan/Teal | `#06B6D4` | Secondary brand color, highlights |
| Dark surface | Slate 900 | `#0F172A` | Dark backgrounds (primary context) |
| Light surface | White | `#FFFFFF` | Light backgrounds |
| Text on dark | Pure White | `#FFFFFF` | Mark/text on dark backgrounds |
| Text on light | Slate 900 | `#0F172A` | Mark/text on light backgrounds |

### Rules

- The mark uses at most 2 colors (primary + accent, or single color)
- The logo mark's primary form is the dark rounded-square tile (`#0F172A` background) — this is the icon's identity, distinct from the canvas rule for marketing graphics below
- Marketing and info graphics (`ph-gallery-*`, `social-preview`) use a **light canvas** as the default — see [Design Guidelines](DESIGN.md#canvas) for the full rule and its rationale
- The flat single-color version is the canonical form; gradients are decorative only
- The logo must be fully recognizable in monochrome

---

## Typography

| Use | Font | Fallback | Weight |
|-----|------|----------|--------|
| Wordmark | Geist Sans or Inter | system sans-serif | Bold (700) or Semi-Bold (600) |
| Code/technical | Geist Mono or JetBrains Mono | monospace | Regular (400) |

### Wordmark rules

- All lowercase: `web-researcher`
- The hyphen is part of the name — never remove it
- When space is limited, the icon alone is sufficient

---

## Asset Inventory

All assets live in `/assets/`. Current deliverables:

### Logo variants

| File | Use |
|------|-----|
| `logo-final.svg` | Full color, vector source |
| `logo-final-dark.svg` | Full color on dark background (primary) |
| `logo-final-light.svg` | Adjusted for light backgrounds |
| `logo-final-transparent.svg` | Mark only, no background |
| `logo-final-mono-white.svg` | White mark (for dark backgrounds) |
| `logo-final-mono-dark.svg` | Dark mark (for light backgrounds) |

### Sized PNGs

| File | Size | Use |
|------|------|-----|
| `logo-final-512.png` | 512x512 | Standard icon |
| `logo-final-dark-512.png` | 512x512 | Dark-variant full icon |
| `logo-final-light-512.png` | 512x512 | Light-variant full icon |
| `logo-final-mono-dark-512.png` | 512x512 | Mono dark icon |
| `logo-final-mono-white-512.png` | 512x512 | Mono white icon |
| `logo-final-transparent-512.png` | 512x512 | Transparent-background icon |
| `logo-400x400.png` | 400x400 | Legacy / fallback icon |
| `logo-final-240.png` | 240x240 | Product Hunt, app stores |
| `logo-final-128.png` | 128x128 | Medium contexts |
| `logo-final-64.png` | 64x64 | Small icon |
| `logo-final-32.png` | 32x32 | Favicon |
| `logo-final-16.png` | 16x16 | Micro favicon |
| `favicon.ico` | Multi-size | Browser favicon |

### Lockups & wordmarks

| File | Use |
|------|-----|
| `wordmark.svg` | Text-only horizontal (dark) |
| `wordmark-light.svg` | Text-only horizontal (white) |
| `lockup.svg` / `lockup.png` | Icon + wordmark combined |

### Social & marketing

| File | Size | Use |
|------|------|-----|
| `social-preview.svg` / `.png` | 1280x640 | GitHub / OpenGraph shares |
| `ph-thumbnail.svg` / `.png` / `.gif` | 240x240 | Product Hunt feed |
| `ph-gallery-*.svg` / `.png` | 1270x760 | Product Hunt gallery (6 images) |

---

## Platform Requirements

### GitHub

- **Social preview**: 1280x640px — logo + tagline on dark background
- **Repo avatar**: Use `logo-final-512.png`
- **Favicon** (via docs site): 32x32 / 16x16

### Product Hunt

- **Thumbnail**: 240x240px — animated GIF catches eyes in the feed; keep animation subtle
- **Gallery**: 1270x760px, 3-8 images — show the tool in action, not marketing fluff

### General rule

When placing the logo in a new context, use the largest variant that fits and verify it's legible. At small sizes (<64px), use the icon alone without text.

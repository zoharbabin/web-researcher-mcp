# Brand & Logo Design Guidelines

Best practices, principles, and specifications for designing the web-researcher-mcp visual identity. Synthesized from industry research, expert principles, and platform-specific requirements.

---

## 1. The Five Universal Principles (Paul Rand / Smashing Magazine)

Every effective logo must satisfy all five. If any one fails, the logo fails.

| Principle | Test | Common Failure |
|-----------|------|----------------|
| **Simple** | Can you describe it in one sentence? Can someone sketch it from memory? | Too many elements, overly detailed geometry |
| **Memorable** | After 3 seconds of viewing, can a stranger recall the shape? | Generic abstract blobs, clip-art aesthetics |
| **Timeless** | Will it look dated in 5 years? Does it rely on current trends (heavy gradients, glassmorphism)? | Chasing trends over substance |
| **Versatile** | Does it work at 16px (favicon) AND 1200px? In one color? On dark AND light backgrounds? | Fine lines that vanish at small sizes, color-dependent designs |
| **Appropriate** | Does it match the audience (developers, technical users) without being cliche? | Generic tech symbols (circuits, binary, gears) |

> "A logo does not sell (directly), it identifies. A logo derives meaning from the quality of the thing it symbolizes, not the other way around." -- Paul Rand

---

## 2. Developer Tool Logo Conventions (What Works)

### Successful patterns in the ecosystem

| Project | Logo Style | Why It Works |
|---------|-----------|--------------|
| Docker | Whale with containers | Single metaphor, instant recognition, works at any size |
| Go (gopher) | Mascot character | Personality, community attachment, unique silhouette |
| Vercel | Simple triangle | Extreme minimalism, works at favicon size, bold identity |
| Supabase | Abstract "S" mark | Clean geometry, distinctive, dark-mode native |
| Tailwind CSS | Abstract wind shapes | Unique form, color-independent, scalable |
| Rust | Gear with "R" | Single concept, clear silhouette, B&W capable |
| GitHub | Octocat | Mascot with personality, endlessly remix-able |

### Key takeaways for developer tools

1. **Dark-mode first** -- developers live in dark themes; design on dark backgrounds first
2. **Monochrome must work** -- the mark should be fully recognizable in single-color/white-on-dark
3. **Single concept, not a diagram** -- the best logos communicate ONE idea, not an architecture diagram
4. **Avoid literal tech cliches** -- no circuits, no binary, no generic globe-with-nodes, no magnifying glass on a globe (our current logo)
5. **Own a unique silhouette** -- the outline alone should be identifiable (squint test)
6. **Geometric sans-serif typography** -- if a wordmark is used, stick to modern geometric fonts (Inter, Geist, Satoshi, Outfit)

---

## 3. What's Wrong With the Current Logo

Honest assessment against the five principles:

| Principle | Current Logo | Verdict |
|-----------|-------------|---------|
| Simple | Magnifying glass + globe + grid lines + nodes + connection lines + search result lines = 6+ distinct element types | FAIL |
| Memorable | Nothing distinctive; generic "search the web" clip-art aesthetic | FAIL |
| Timeless | Flat but with many fine strokes; aesthetically mid-2010s | WEAK |
| Versatile | At 16px favicon the internal lines vanish; on light backgrounds the white elements disappear | FAIL |
| Appropriate | Magnifying glass + globe is the most overused search icon in existence | FAIL |

**Root cause**: The current logo tries to literally depict what the tool does (search + web + connections) rather than creating a distinctive mark that identifies the brand.

---

## 4. Design Direction for Rebrand

### Core positioning

web-researcher-mcp is a **research intelligence layer** -- it doesn't just search, it orchestrates multiple providers, extracts structured knowledge, and delivers synthesized results. The logo should evoke **intelligence, precision, and synthesis** -- not "magnifying glass on Google."

### Recommended approaches (pick ONE)

| Approach | Description | Precedent |
|----------|-------------|-----------|
| **Abstract lettermark** | A stylized "W" or "WR" with a distinctive geometric treatment | Vercel (triangle = "V"), Supabase (abstract "S") |
| **Single metaphor** | One object that evokes synthesis/transformation (prism, lens, compass, beacon) | Docker (whale = "containers float"), Rust (gear = "systems") |
| **Distinctive geometric mark** | A unique shape that doesn't represent anything literal but is unmistakable | Tailwind (abstract shapes), Stripe (offset slashes) |

### Design constraints

- Must work at 16x16px (favicon), 32x32, 64x64, 240x240 (Product Hunt), 512x512, and 1280x640 (GitHub social preview)
- Must work on: #0F172A (dark), #FFFFFF (light), transparent
- Must work in: full color, single color (white), single color (dark)
- No more than 2 colors in the primary mark (accent color + white or dark)
- The icon must stand alone without text (for app icons, favicons, PH thumbnail)

---

## 5. Color System

### Primary palette

Based on research: blue dominates tech logos (39% of Fortune 500) because it signals trust and professionalism. Purple signals innovation and AI. For a developer tool in the AI/research space:

| Role | Color | Hex | Rationale |
|------|-------|-----|-----------|
| Primary | Deep Indigo | `#4F46E5` | Intelligence, depth, developer-friendly (not corporate blue) |
| Accent | Cyan/Teal | `#06B6D4` | Energy, data, modernity (complements indigo) |
| Dark surface | Slate 900 | `#0F172A` | Standard dark-mode background |
| Light surface | White | `#FFFFFF` | Clean light-mode |
| Text/Mark | Pure White | `#FFFFFF` | On dark backgrounds |
| Text/Mark alt | Slate 900 | `#0F172A` | On light backgrounds |

### Rules

- The logo mark itself uses AT MOST 2 colors (primary + accent, or single color)
- Gradients may be used sparingly in the icon but the logo MUST work without them (flat single-color version is the canonical form)
- Start designing in black & white. Only add color after the form works in monochrome

---

## 6. Typography

| Use | Font | Fallback | Weight |
|-----|------|----------|--------|
| Logo wordmark | Geist Sans or Inter | system sans-serif | Bold (700) or Semi-Bold (600) |
| Code/technical | Geist Mono or JetBrains Mono | monospace | Regular (400) |

### Wordmark rules

- All lowercase for approachability: `web-researcher`
- Hyphen is part of the name; don't remove it
- If space is limited, the icon alone is sufficient (no wordmark needed)

---

## 7. Platform-Specific Requirements

### Product Hunt

| Asset | Size | Format | Notes |
|-------|------|--------|-------|
| Thumbnail | 240x240px | GIF (animated) or PNG | Animated GIF catches the eye in the feed -- subtle motion, not flashy |
| Gallery images | 1270x760px | PNG/JPG | Min 3, max 8. Show the tool in action, not marketing fluff |
| Maker avatar | 100x100px | PNG/JPG | Your profile photo |

**PH thumbnail tip**: A clean icon on a solid dark background with one subtle animation (pulse, rotation, shimmer) outperforms complex illustrations. The 240x240 space is tiny; detail gets lost.

### GitHub

| Asset | Size | Format | Notes |
|-------|------|--------|-------|
| Social preview | 1280x640px | PNG/JPG | Shown on link shares. Include logo + tagline + dark background |
| Repository avatar | 500x500px | PNG | The icon alone, no text |
| Favicon (via docs) | 32x32px, 16x16px | PNG/ICO | Must be legible at this size |

### General

| Asset | Size | Format |
|-------|------|--------|
| Icon (standard) | 512x512px | SVG + PNG |
| Icon (small) | 64x64px | PNG |
| Icon (micro) | 16x16px | PNG/ICO |
| Wordmark horizontal | variable height 48-64px | SVG + PNG |
| Full lockup (icon + wordmark) | variable | SVG + PNG |

---

## 8. Design Process Checklist

Based on professional logo design methodology:

1. **Define the concept in one sentence** -- what single idea should a viewer take away?
2. **Sketch 20+ rough ideas on paper** -- explore lettermarks, symbols, abstract forms, negative space tricks
3. **Pick 3 strongest, develop digitally in black & white only** -- no color yet
4. **Apply the squint test** -- blur to 50%; is the shape still distinctive?
5. **Test at extreme sizes** -- render at 16px, 64px, 240px, 512px. Does it hold?
6. **Test on dark and light** -- white version on dark, dark version on light
7. **Test in context** -- paste into a GitHub README header, a Product Hunt feed, a terminal prompt
8. **Only then add color** -- choose one or two hues that enhance, not define
9. **Create the flat single-color canonical version** -- this is the "true" logo
10. **Derive all size variants and formats**

---

## 9. What to Avoid

| Don't | Why |
|-------|-----|
| Multiple concepts crammed into one mark | Confusion; fails simplicity test |
| Literal depictions of "search" or "web" | Cliche, forgettable, not distinctive |
| More than 3 colors in the mark | Printing issues, scaling issues, visual noise |
| Fine lines or small internal details | Disappear at favicon/small sizes |
| Trendy effects (glassmorphism, 3D, complex gradients) | Date quickly; fail the timeless test |
| Symmetry for symmetry's sake | Can look generic; asymmetry creates memorability |
| Looking like another well-known project | Legal risk, brand confusion |

---

## 10. Competitive Differentiation

What other MCP servers and research tools look like (to deliberately avoid):

- Most MCP servers have no custom logo (just GitHub's default identicon)
- Search tools default to magnifying glass iconography
- AI tools default to neural-network / brain imagery
- Research tools default to books or academic motifs

**Our opportunity**: The MCP ecosystem is visually unbranded. A strong, distinctive mark will immediately signal professionalism and maturity. We don't need to look like "a search tool" -- we need to look like a brand.

---

## 11. Inspiration References (not to copy, but to study)

These logos succeed because they own a unique shape, work at any size, and don't literally depict their product:

- **Vercel** -- triangle: extreme simplicity, geometric
- **Linear** -- angled shapes: precision, motion
- **Raycast** -- stylized lightning bolt: speed, developer productivity
- **Warp** -- abstract W form: distinctive lettermark
- **Fig** (now part of AWS) -- clean "F" in a rounded square: app-icon ready
- **Cursor** -- minimal cursor shape: one concept, owns it
- **Stripe** -- offset parallel lines: distinctive, abstract, not literal

---

## 12. Deliverables Checklist

When the logo is finalized, produce:

- [x] `logo-final.svg` -- vector source, full color on dark background
- [x] `logo-final-dark.svg` -- full color on dark background (primary)
- [x] `logo-final-light.svg` -- adjusted palette for light backgrounds
- [x] `logo-final-transparent.svg` -- mark only, no background
- [x] `logo-final-mono-white.svg` -- white mark on dark background
- [x] `logo-final-mono-dark.svg` -- dark mark on light background
- [x] `logo-final-512.png` -- 512x512, full color
- [x] `logo-final-240.png` -- 240x240
- [x] `logo-final-128.png` -- 128x128
- [x] `logo-final-64.png` -- 64x64, optimized for small use
- [x] `logo-final-32.png` -- 32x32
- [x] `logo-final-16.png` -- 16x16 favicon
- [x] `favicon.ico` -- multi-size ICO file (16+32+64)
- [x] `social-preview.png` -- 1280x640, for GitHub/OpenGraph
- [x] `ph-thumbnail.png` -- 240x240 static Product Hunt thumbnail
- [x] `ph-thumbnail.gif` -- 240x240 animated Product Hunt thumbnail
- [x] `wordmark.svg` -- text-only horizontal lockup (dark)
- [x] `wordmark-light.svg` -- text-only horizontal lockup (white)
- [x] `lockup.svg` -- icon + wordmark combined
- [ ] `ph-gallery-*.png` -- 1270x760 gallery images (3-5, created during launch)

---

## Sources

- Smashing Magazine, "Vital Tips For Effective Logo Design" (Jacob Cass)
- Paul Rand's Logo Design Principles (DesignBro)
- "Minimalist Logo Design: Crafting Memorable Brands with Less" (Rise Accelerator / Mint Stroke Creative)
- "Tech Startup Logo Design: Best Practices & Examples" (Magnt)
- "25+ Logo Statistics, Data, and Trends" (Exploding Topics)
- "How to Design Professional Logo" (Logo.im)
- "Mastering the Product Hunt Launch" (KickoffLabs)
- Product Hunt official posting guidelines (240x240 GIF thumbnail, 1270x760 gallery)

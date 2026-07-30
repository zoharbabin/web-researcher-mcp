# Design Guidelines

The visual design system for marketing and documentation graphics: color tokens, typography, layout patterns, and component rules. Follow these when creating or editing anything in `assets/*.svg` or docs-site imagery.

This is the *visual system* (canvas, color, layout, components). Voice, logo mark, and messaging rules live in [Brand Guidelines](BRAND_GUIDELINES.md) — read both before shipping a new graphic.

---

## Why this document exists

Every marketing graphic in this repo (`assets/ph-gallery-*.svg`, `social-preview.svg`, etc.) is hand-authored SVG, not exported from a design tool. Without a written system, each new graphic re-derives its own colors, spacing, and component shapes from whatever the previous one happened to use — and drifts further with every edit. This doc is the single source of truth those graphics must match, so a future edit (by a human or an agent) can check itself against a rule instead of guessing from vibes.

---

## Canvas

**Light canvas is canonical** for all marketing/info graphics (`ph-gallery-*`, `social-preview`): white or near-white background, dark text.

| Token | Hex | Use |
|---|---|---|
| Canvas | `#FFFFFF` | Default graphic background |
| Canvas tint | `#F8FAFC` | Subtle sectioning within a light canvas (rare; prefer plain white) |

**Why:** light canvas reads cleanly in both GitHub's light and dark rendering, in Product Hunt's feed, and in link-preview cards on light-themed platforms (Slack, X, LinkedIn) — the majority of surfaces these graphics appear on. A dark-canvas graphic looks like a floating box with hard edges on any light surface.

**Exception — the logo mark itself.** The icon (`logo-final.svg` and friends) keeps its dark rounded-square tile as its primary form — that's the brand mark's identity, not a graphic's canvas, and is governed by [Brand Guidelines](BRAND_GUIDELINES.md#logo--mark), not this rule.

**Exception — authentic dark UI.** A terminal window, code editor, or other real dark-themed UI screenshot embedded *within* a light-canvas graphic keeps its native dark chrome (see `ph-gallery-2-install.svg`). This isn't a light/dark violation — it's depicting a real dark tool on a light page, the same way a docs screenshot of a dark IDE doesn't get recolored.

---

## Color tokens

| Role | Hex | Use |
|---|---|---|
| Heading text | `#0F172A` | Titles, headline numbers |
| Body text | `#475569` | Subheads, captions |
| Secondary text | `#64748B` | Descriptors, muted labels |
| Tertiary text | `#94A3B8` | Sub-category labels, placeholders |
| Border | `#E2E8F0` | Pill/card outlines on light canvas |
| Primary brand | `#4F46E5` (indigo) | Primary accent, links, brand-color callouts |
| Secondary brand | `#06B6D4` / `#0EA5C9` (cyan) | Secondary accent, gradient partner to indigo |

### Category accent colors

Used for grouping headers and status dots when a graphic organizes content into named categories (providers, tool outcomes, lens domains). Pick one accent per category and use it consistently for that category's header pill, status dots, and any highlighted text within it.

| Category example | Hex | Tint (badge/callout background) |
|---|---|---|
| Blue | `#2563EB` | `#EFF6FF` |
| Purple | `#7C3AED` | `#F5F3FF` |
| Orange | `#D97706` | `#FFFBEB` |
| Green | `#16A34A` | `#F0FDF4` |

**Why:** a fixed, small palette keeps multi-category graphics (provider lists, tool grids) scannable — each category is a color, not a paragraph. More than ~4 category colors per graphic starts to look like a rainbow instead of a taxonomy; group further before adding a 5th.

### Status/semantic colors

| Meaning | Hex |
|---|---|
| Free / positive | `#16A34A` text on `#DCFCE7` background |
| Warning / attention | `#D97706` |
| Error | `#EF4444` |

---

## Typography

| Use | Font stack | Weight |
|---|---|---|
| Headlines | `'Inter', 'SF Pro Display', sans-serif` | 700 |
| Subheads/body | `'Inter', 'SF Pro Display', sans-serif` | 400–600 |
| Code/terminal | `'JetBrains Mono', 'SF Mono', monospace` | 400 |

Rules:
- One headline per graphic, sized 28–36px in a 1270×760 canvas; scale proportionally for other canvas sizes.
- `letter-spacing="-1"` on headlines reads better at this font size — keep it.
- Never mix in a second display typeface; JetBrains Mono is the only permitted departure, and only for literal code/terminal content.

---

## Layout patterns

### The pill

The core recurring shape. A pill is a fully-rounded rectangle (`rx` = half its height) used for: category headers, individual list items (providers, tools, features), badges, and mode/context tags.

```
┌──────────────────────────────────────┐
│ ● Label                    descriptor │   <- item pill: white fill, #E2E8F0 border,
└──────────────────────────────────────┘      colored status dot, bold label, muted descriptor
```

- **Category header pill**: solid category-color fill, white bold text, `rx` = full height/2.
- **Item pill**: white fill, `#E2E8F0` 1.5px border, a colored status dot (`r≈5`) at the left, bold dark label, optional muted descriptor right-aligned.
- **Badge** (e.g. "FREE"): small pill, tinted background matching its semantic color, bold colored text, sized to hug its label — not a fixed width.
- **Feature/callout pill**: tinted background (category or semantic tint), centered bold colored text, used for bottom-of-graphic takeaways.

**Why a pill and not a plain list or a bordered card per item:** individual pills let each item carry its own status dot and right-aligned descriptor without needing a table — this matters when the list mixes items of different kinds (a provider name vs. "+ 7 more providers"). A card-per-category with plain text lines inside (the pre-fix version of `ph-gallery-3-providers.svg`) loses the per-item status signal and reads as a wall of text instead of a scannable list.

### Category columns

When content splits into named categories (providers by domain, tools by outcome), lay out as parallel columns, each topped by its category header pill, containing that category's item pills stacked vertically. Use a small-caps tertiary-color sub-label (`font-size 10, letter-spacing 1.2, uppercase`) to further split a column into sub-groups without a new header pill (see "CITATION INTEGRITY" under Academic in `ph-gallery-3-providers.svg`).

### Bottom takeaways

Close a graphic with: 1–3 feature/callout pills (short, punchy, tinted), optionally a second row of mode/context badges (neutral gray pill, e.g. "STDIO" / "HTTP·Docker"), then a single muted tagline sentence beneath everything. Don't stack more than two rows of pills before the tagline — a third row reads as clutter.

---

## Component reference

| Component | Fill | Border/stroke | Text |
|---|---|---|---|
| Category header pill | category accent hex, solid | none | `#FFFFFF`, 700 |
| Item pill | `#FFFFFF` | `#E2E8F0`, 1.5px | label `#0F172A` 600; descriptor `#64748B` 400 |
| Status dot | category accent hex | none | — |
| Badge (e.g. FREE) | semantic tint | none | semantic hex, 700 |
| Sub-category label | none (text only) | none | `#94A3B8`, 700, letter-spacing 1.2, uppercase |
| Feature callout pill | category or semantic tint | none | matching hex, 600, centered |
| Mode/context badge | `#F1F5F9` | none | `#334155`, 500, centered |
| Tagline | none | none | `#64748B`, 400, centered |

---

## Governance — how to keep this from drifting again

1. **Before editing any `assets/*.svg` marketing graphic**, re-read this doc and the closest existing sibling graphic. If your change would introduce a canvas color, font, or pill shape not listed above, that's a signal to update this doc first, not to freelance a one-off.
2. **A single-graphic redesign is a design-system change**, not a content change — if you're touching the visual language (not just swapping a label or a count), say so explicitly when proposing the change, and check it against every other graphic in `assets/` for consistency, not just the one file you're editing.
3. **When this doc and a shipped graphic disagree**, the doc wins — fix the graphic, don't retroactively justify the drift.
4. **Regeneration workflow**: edit the `.svg` source, validate it's well-formed XML, then regenerate the matching `.png`:
   ```bash
   python3 -c "import xml.dom.minidom as m; m.parse('assets/<name>.svg')"
   rsvg-convert -w 1270 -h 760 assets/<name>.svg -o assets/<name>.png   # match the SVG's own viewBox dimensions
   ```
   Commit the `.svg` and regenerated `.png` together — never one without the other.

---

## Asset inventory

| File | Canvas | Notes |
|---|---|---|
| `ph-gallery-1-hero.svg` | Light | Hero: logo + tagline + stats row |
| `ph-gallery-2-install.svg` | Light | Install flow; terminal card keeps native dark chrome (see Canvas exception) |
| `ph-gallery-3-providers.svg` | Light | Providers by category, pill layout |
| `ph-gallery-4-tools.svg` | Light | Tools by outcome, pill layout |
| `ph-gallery-5-lenses.svg` | Light | Search lenses by domain, pill layout |
| `ph-gallery-6-trust-proof.svg` | Light | Before/after citation trust: same query, fabricated vs. verified answer card |
| `social-preview.svg` | Light | GitHub/OpenGraph share card |
| `ph-thumbnail.svg` | Dark (icon tile) | Product Hunt feed thumbnail — brand mark identity, not a content graphic |
| `logo-final*.svg` | Dark tile (primary) | Governed by [Brand Guidelines](BRAND_GUIDELINES.md), not this doc |

For logo variants, sizing, and platform-specific requirements (favicon, repo avatar, Product Hunt thumbnail dimensions), see [Brand Guidelines](BRAND_GUIDELINES.md).

# decks/

Presentation decks about this project — source + rendered outputs, one folder
per deck. Published to the docs site under `/<deck>/` via `.github/workflows/docs.yml`.

| Deck | What it covers |
|------|----------------|
| [`compliance/`](compliance/) | *Compliance as Architecture* — how architecture (not paperwork) keeps a solo-maintained repo aligned with 23 security & privacy standards |

## Convention

Each deck is a folder holding three files with the folder's name as the stem:

```
decks/<name>/
├── <name>-deck.md      # Marp source (source of truth)
├── <name>-deck.html    # rendered, self-contained (logo inlined as data URI)
└── <name>-deck.pdf     # rendered, self-contained
```

- **Source is [Marp](https://marp.app/)** (`marp: true` frontmatter). Slides are
  Markdown; styling is a single inline `<style>` block matched to
  `assets/social-preview.svg`.
- **Rendered outputs are self-contained** — the logo is embedded as a
  `data:image/svg+xml;base64` URI, so the HTML works on GitHub Pages with no
  external fetch and the PDF needs no local files.
- **Accuracy rule (same as docs):** every factual claim must match the current
  code, and each slide cites the file that proves it. Re-verify against the
  codebase before re-rendering.

## Re-rendering

```bash
cd decks/<name>
export CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
npx @marp-team/marp-cli@latest <name>-deck.md --html -o <name>-deck.html
npx @marp-team/marp-cli@latest <name>-deck.md --pdf --allow-local-files -o <name>-deck.pdf
```

## Publishing

`docs.yml` copies each deck's rendered `*.html` (as `index.html`) and `*.pdf`
into `site_src/decks/<name>/`, so they serve at
`https://zoharbabin.github.io/web-researcher-mcp/decks/<name>/`. Add a new deck's
copy lines to that workflow's assemble step.

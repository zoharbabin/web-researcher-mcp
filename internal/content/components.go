package content

// Generative-UI components (#90): optional, additive, renderable structures
// built deterministically from data already extracted — no server-side LLM
// call. Every component carries an "AI-formatted" marker and a reference back
// to the raw source data so nothing is hidden or unverifiable. Components are
// NEVER a substitute for the raw text/sources, which are always present.

// AIFormattedLabel is the non-disableable marker stamped on every generated
// component, signalling that the structure was assembled by the server from
// extracted data (a deterministic transform, not model-generated prose).
const AIFormattedLabel = "AI-formatted"

// Component is a small, stable renderable unit. Type is one of "card", "table",
// or "list". The schema is intentionally minimal and additive — clients that
// don't understand a type can ignore it and fall back to the raw content.
type Component struct {
	Type        string          `json:"type"`
	AIFormatted bool            `json:"aiFormatted"` // always true; the mandatory label
	Label       string          `json:"label"`       // AIFormattedLabel
	Title       string          `json:"title,omitempty"`
	SourceRefs  []string        `json:"sourceRefs,omitempty"` // raw-data references (URLs)
	Card        *CardComponent  `json:"card,omitempty"`
	Table       *TableComponent `json:"table,omitempty"`
}

// CardComponent summarizes a single source with its authority/quality metadata.
type CardComponent struct {
	URL     string  `json:"url"`
	Title   string  `json:"title,omitempty"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// TableComponent is a simple comparison table built from per-source signals.
type TableComponent struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

func aiComponent(typ, title string, refs []string) Component {
	return Component{
		Type:        typ,
		AIFormatted: true,
		Label:       AIFormattedLabel,
		Title:       title,
		SourceRefs:  refs,
	}
}

// BuildComponents assembles AI-formatted components from scored sources. It is
// deterministic and additive. Returns nil (field omitted) when there is nothing
// to render, so a disabled or empty case is a clean no-op. snippets maps a
// source URL to a short content excerpt (already extracted; may be empty).
func BuildComponents(sources []ScoredSource, snippets map[string]string) []Component {
	withText := make([]ScoredSource, 0, len(sources))
	for _, s := range sources {
		if s.HasText {
			withText = append(withText, s)
		}
	}
	if len(withText) == 0 {
		return nil
	}

	components := make([]Component, 0, len(withText)+1)

	// One source card per source, carrying authority/quality and a raw-data ref.
	for _, s := range withText {
		card := aiComponent("card", s.Title, []string{s.URL})
		card.Card = &CardComponent{
			URL:     s.URL,
			Title:   s.Title,
			Score:   s.Score.Overall,
			Snippet: clampSnippet(snippets[s.URL]),
		}
		components = append(components, card)
	}

	// A comparison table across sources (quality breakdown) when there are at
	// least two — a single-row table adds nothing over the card.
	if len(withText) >= 2 {
		refs := make([]string, 0, len(withText))
		rows := make([][]string, 0, len(withText))
		for _, s := range withText {
			refs = append(refs, s.URL)
			rows = append(rows, []string{
				s.URL,
				formatScore(s.Score.Overall),
				formatScore(s.Score.Authority),
				formatScore(s.Score.Relevance),
				formatScore(s.Score.Freshness),
			})
		}
		table := aiComponent("table", "Source quality comparison", refs)
		table.Table = &TableComponent{
			Columns: []string{"source", "overall", "authority", "relevance", "freshness"},
			Rows:    rows,
		}
		components = append(components, table)
	}

	return components
}

func clampSnippet(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func formatScore(f float64) string {
	// Two-decimal fixed format without importing fmt for one call site.
	hundredths := int(f*100 + 0.5)
	whole := hundredths / 100
	frac := hundredths % 100
	digits := []byte{byte('0' + whole), '.', byte('0' + frac/10), byte('0' + frac%10)}
	return string(digits)
}

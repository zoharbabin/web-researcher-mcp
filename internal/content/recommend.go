package content

import "sort"

// ScoredSource pairs a source's identity with its already-computed quality
// score. Recommendations and components are derived purely from these signals —
// no user behavior, no profiling, no second scoring pass (#95, #90).
type ScoredSource struct {
	URL     string
	Title   string
	Score   QualityScore
	HasText bool // true when the source yielded extractable content
}

// Recommendation is an advisory pointer to a higher-quality source in the
// current result set. It never hides or re-ranks the actual results — the
// caller's LLM/user decides whether to act on it. Criteria are transparent and
// content-derived (the existing quality signals), so this is safe by default
// and explicitly NOT behavioral profiling (#95).
type Recommendation struct {
	URL    string  `json:"url"`
	Title  string  `json:"title,omitempty"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// RecommendSources returns up to maxN advisory recommendations drawn from the
// current result set, ranked by overall quality. It is deterministic and
// content-only: identical inputs always yield identical output. A source must
// have extractable text and clear the minimum overall-quality bar to be
// recommended. Returns nil (omitted from output) when nothing qualifies.
func RecommendSources(sources []ScoredSource, maxN int) []Recommendation {
	if maxN <= 0 {
		maxN = 3
	}

	// Copy before sorting so we never reorder the caller's results — the
	// recommendation layer is advisory and must not mutate result order (#95).
	ranked := make([]ScoredSource, 0, len(sources))
	for _, s := range sources {
		if s.HasText && s.Score.Overall >= 0.6 {
			ranked = append(ranked, s)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].Score.Overall > ranked[j].Score.Overall
	})

	if len(ranked) > maxN {
		ranked = ranked[:maxN]
	}

	recs := make([]Recommendation, 0, len(ranked))
	for _, s := range ranked {
		recs = append(recs, Recommendation{
			URL:    s.URL,
			Title:  s.Title,
			Score:  s.Score.Overall,
			Reason: recommendationReason(s.Score),
		})
	}
	if len(recs) == 0 {
		return nil
	}
	return recs
}

// recommendationReason explains, in transparent terms, why a source scored
// well — naming the dominant content signal. No hidden criteria.
func recommendationReason(q QualityScore) string {
	type signal struct {
		name  string
		value float64
	}
	signals := []signal{
		{"high authority", q.Authority},
		{"strong relevance", q.Relevance},
		{"recent", q.Freshness},
		{"substantial content", q.ContentQuality},
	}
	best := signals[0]
	for _, s := range signals[1:] {
		if s.value > best.value {
			best = s
		}
	}
	return best.name + " (content-based; advisory only)"
}

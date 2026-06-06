package session

import "sort"

// errorKindSuggestion maps a typed error kind to a session-level remediation
// hint (distinct from the per-call SuggestedAction). Kept here so the
// aggregation layer owns the cross-call advice; kinds mirror
// internal/tools/errors.go ErrorKind values.
var errorKindSuggestion = map[string]string{
	"auth_required":        "Consider open_access=true or target preprint servers (arxiv, biorxiv).",
	"blocked":              "Try alternative sources or use web_search for cached versions.",
	"rate_limited":         "Switch to a different provider or space requests further apart.",
	"browser_unavailable":  "Set CHROME_PATH for JavaScript-heavy sites.",
	"network":              "Transient network errors — retry, or try a different source.",
	"content_empty":        "The page yielded no usable text — try a different source or the original PDF.",
	"upstream_unavailable": "The provider is unavailable — switch providers or retry later.",
}

// maxAffectedURLs caps how many example URLs a pattern lists, keeping the
// surfaced metadata bounded.
const maxAffectedURLs = 5

// appendOutcome adds an outcome to a session, enforcing the FIFO MaxOutcomes
// bound. Exported-internal helper shared by every Manager implementation so the
// retention bound is identical across backends.
func appendOutcome(sess *Session, ev OutcomeEvent) {
	sess.Outcomes = append(sess.Outcomes, ev)
	if len(sess.Outcomes) > MaxOutcomes {
		sess.Outcomes = sess.Outcomes[len(sess.Outcomes)-MaxOutcomes:]
	}
}

// AggregateOutcomes derives the session-level error patterns and provider stats
// from a session's outcome log. Patterns are surfaced only when a kind occurs at
// least ErrorPatternMinCount times (false-positive guard). Deterministic
// ordering: patterns by descending count then kind; provider stats are a map.
// Returns (nil, nil) when there are no outcomes, so empty sessions stay clean.
func AggregateOutcomes(outcomes []OutcomeEvent) ([]ErrorPattern, map[string]ProviderStat) {
	if len(outcomes) == 0 {
		return nil, nil
	}

	type kindAgg struct {
		count    int
		urls     []string
		urlSeen  map[string]struct{}
		lastSeen string
	}
	kinds := map[string]*kindAgg{}
	stats := map[string]ProviderStat{}

	for _, ev := range outcomes {
		if ev.Provider != "" {
			s := stats[ev.Provider]
			s.Attempts++
			if ev.Success {
				s.Successes++
			}
			stats[ev.Provider] = s
		}
		if ev.Success || ev.ErrorKind == "" {
			continue
		}
		a := kinds[ev.ErrorKind]
		if a == nil {
			a = &kindAgg{urlSeen: map[string]struct{}{}}
			kinds[ev.ErrorKind] = a
		}
		a.count++
		if ev.Timestamp != "" {
			a.lastSeen = ev.Timestamp // outcomes are appended in order; last wins
		}
		if ev.URL != "" {
			if _, dup := a.urlSeen[ev.URL]; !dup && len(a.urls) < maxAffectedURLs {
				a.urls = append(a.urls, ev.URL)
				a.urlSeen[ev.URL] = struct{}{}
			}
		}
	}

	var patterns []ErrorPattern
	for kind, a := range kinds {
		if a.count < ErrorPatternMinCount {
			continue
		}
		patterns = append(patterns, ErrorPattern{
			Kind:         kind,
			Count:        a.count,
			AffectedURLs: a.urls,
			Suggestion:   errorKindSuggestion[kind],
			LastSeen:     a.lastSeen,
		})
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Count != patterns[j].Count {
			return patterns[i].Count > patterns[j].Count
		}
		return patterns[i].Kind < patterns[j].Kind
	})

	if len(stats) == 0 {
		stats = nil
	}
	return patterns, stats
}

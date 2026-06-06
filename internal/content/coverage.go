package content

import (
	"net/url"
	"sort"
	"strings"
)

// CoverageInput is one recorded source reduced to the signals the coverage
// analyzer needs: its URL (for domain spread) and its type tag (the session's
// ResearchSource.Relevance — "academic"/"news"/"scraped"/"patent"/…).
type CoverageInput struct {
	URL  string
	Type string
}

// Coverage is a lightweight, deterministic assessment of how well a set of
// recorded research sources spans the problem space. It is descriptive metadata
// — it never synthesizes an answer. Gaps drive the refinement-query suggestions
// the caller's AI may choose to act on (#67, "standard"/"thorough" depth).
type Coverage struct {
	SourceCount    int            `json:"sourceCount"`
	UniqueDomains  int            `json:"uniqueDomains"`
	DomainSpread   float64        `json:"domainSpread"`             // uniqueDomains / sourceCount, 0..1
	DominantDomain string         `json:"dominantDomain,omitempty"` // the most-cited domain when concentration is high
	SourceTypes    map[string]int `json:"sourceTypes,omitempty"`    // count per type tag
	Gaps           []string       `json:"gaps,omitempty"`           // human-readable coverage gaps
}

// concentration threshold: a single domain supplying more than this share of
// sources is flagged as over-concentrated.
const dominantDomainShare = 0.6

// AnalyzeCoverage computes coverage signals from recorded sources. Deterministic
// for a given input. Empty input yields a zero-value Coverage with a single
// "no sources yet" gap so the caller still gets actionable guidance.
func AnalyzeCoverage(sources []CoverageInput) Coverage {
	cov := Coverage{SourceCount: len(sources)}
	if len(sources) == 0 {
		cov.Gaps = []string{"No sources recorded yet — run a search and record results before refining."}
		return cov
	}

	domainCount := map[string]int{}
	types := map[string]int{}
	for _, s := range sources {
		if d := hostOf(s.URL); d != "" {
			domainCount[d]++
		}
		t := s.Type
		if t == "" {
			t = "other"
		}
		types[t]++
	}

	cov.UniqueDomains = len(domainCount)
	if len(sources) > 0 {
		cov.DomainSpread = round2(float64(len(domainCount)) / float64(len(sources)))
	}
	cov.SourceTypes = types

	// Domain concentration gap.
	topDomain, topN := "", 0
	for d, n := range domainCount {
		if n > topN || (n == topN && d < topDomain) {
			topDomain, topN = d, n
		}
	}
	if topDomain != "" && float64(topN)/float64(len(sources)) > dominantDomainShare && len(sources) > 1 {
		cov.DominantDomain = topDomain
		cov.Gaps = append(cov.Gaps, "Sources are concentrated on "+topDomain+" — diversify across other domains.")
	}

	// Source-type balance gap: only one kind of source recorded.
	if len(types) == 1 {
		for t := range types {
			cov.Gaps = append(cov.Gaps, "All sources are of type \""+t+"\" — consider complementary source types (e.g. academic vs. news vs. primary web).")
		}
	}

	// Thin-coverage gap.
	if len(sources) < 3 {
		cov.Gaps = append(cov.Gaps, "Few sources gathered so far — broaden the search to build confidence.")
	}

	sort.Strings(cov.Gaps)
	return cov
}

// hostOf returns the registrable host of a URL (lowercased, no port, no leading
// "www."), or "" if it can't be parsed.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	return strings.TrimPrefix(h, "www.")
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

package content

import "testing"

func sample() []ScoredSource {
	return []ScoredSource{
		{URL: "https://low.example.com", Title: "Low", HasText: true, Score: QualityScore{Overall: 0.4, Authority: 0.5}},
		{URL: "https://high.gov", Title: "Gov", HasText: true, Score: QualityScore{Overall: 0.9, Authority: 0.9}},
		{URL: "https://mid.edu", Title: "Edu", HasText: true, Score: QualityScore{Overall: 0.7, Relevance: 0.8}},
		{URL: "https://empty.example.com", Title: "Empty", HasText: false, Score: QualityScore{Overall: 0.95}},
	}
}

func TestRecommendSourcesRanksAndFilters(t *testing.T) {
	recs := RecommendSources(sample(), 3)
	if len(recs) != 2 {
		t.Fatalf("expected 2 recommendations (>=0.6 with text), got %d", len(recs))
	}
	if recs[0].URL != "https://high.gov" {
		t.Errorf("expected highest-quality source first, got %q", recs[0].URL)
	}
	// The 0.4 source is filtered (below 0.6); the no-text 0.95 source is excluded.
	for _, r := range recs {
		if r.URL == "https://low.example.com" || r.URL == "https://empty.example.com" {
			t.Errorf("did not expect %q to be recommended", r.URL)
		}
		if r.Reason == "" {
			t.Error("expected a transparent reason string")
		}
	}
}

func TestRecommendSourcesDeterministic(t *testing.T) {
	a := RecommendSources(sample(), 3)
	b := RecommendSources(sample(), 3)
	if len(a) != len(b) {
		t.Fatalf("non-deterministic length: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("non-deterministic output at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestRecommendSourcesEmptyWhenNothingQualifies(t *testing.T) {
	low := []ScoredSource{{URL: "x", HasText: true, Score: QualityScore{Overall: 0.3}}}
	if recs := RecommendSources(low, 3); recs != nil {
		t.Errorf("expected nil when nothing clears the bar, got %v", recs)
	}
}

func TestBuildComponentsLabelsAndStructure(t *testing.T) {
	comps := BuildComponents(sample(), map[string]string{"https://high.gov": "snippet text"})
	if len(comps) == 0 {
		t.Fatal("expected components for sources with text")
	}
	var cards, tables int
	for _, c := range comps {
		if !c.AutoFormatted || c.Label != AutoFormattedLabel {
			t.Errorf("every component must carry the mcp-auto-formatted label, got %+v", c)
		}
		if len(c.SourceRefs) == 0 {
			t.Errorf("every component must reference raw source data, got %+v", c)
		}
		switch c.Type {
		case "card":
			cards++
		case "table":
			tables++
		}
	}
	if cards != 3 {
		t.Errorf("expected 3 cards (sources with text), got %d", cards)
	}
	if tables != 1 {
		t.Errorf("expected 1 comparison table (>=2 sources), got %d", tables)
	}
}

func TestBuildComponentsEmptyWhenNoText(t *testing.T) {
	noText := []ScoredSource{{URL: "x", HasText: false, Score: QualityScore{Overall: 0.9}}}
	if comps := BuildComponents(noText, nil); comps != nil {
		t.Errorf("expected nil when no source has text, got %v", comps)
	}
}

func TestFormatScore(t *testing.T) {
	cases := map[float64]string{0: "0.00", 0.5: "0.50", 0.9: "0.90", 1.0: "1.00", 0.666: "0.67"}
	for in, want := range cases {
		if got := formatScore(in); got != want {
			t.Errorf("formatScore(%v) = %q, want %q", in, got, want)
		}
	}
}

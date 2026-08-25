package tools

import (
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

// TestLowQualityDominanceWarning is the #667 regression: assembleCombined
// concatenates per-source content purely by length, with no regard for the
// already-computed per-source quality score, so a single low-quality,
// verbose source can occupy most of the combined output with no signal
// surfaced to the caller. lowQualityDominanceWarning must fire only when a
// source scoring below the threshold accounts for more than half of the
// combined content's length.
func TestLowQualityDominanceWarning(t *testing.T) {
	const threshold = 0.4

	t.Run("low-score source dominates the combined output", func(t *testing.T) {
		sources := []sourceOutput{
			{URL: "https://low.example.com/rambling-thread", Scores: &content.QualityScore{Overall: 0.2}},
			{URL: "https://high.example.com/article", Scores: &content.QualityScore{Overall: 0.8}},
		}
		combinedParts := []string{strings.Repeat("a", 700), strings.Repeat("b", 300)}

		got := lowQualityDominanceWarning(sources, combinedParts, threshold)
		if got == "" {
			t.Fatal("expected a non-empty warning when a low-quality source dominates the combined output")
		}
		if !strings.Contains(got, "https://low.example.com/rambling-thread") {
			t.Errorf("expected warning to name the dominant low-quality source's URL, got %q", got)
		}
	})

	t.Run("all sources score above threshold", func(t *testing.T) {
		sources := []sourceOutput{
			{URL: "https://a.example.com", Scores: &content.QualityScore{Overall: 0.5}},
			{URL: "https://b.example.com", Scores: &content.QualityScore{Overall: 0.6}},
		}
		combinedParts := []string{strings.Repeat("a", 700), strings.Repeat("b", 300)}

		if got := lowQualityDominanceWarning(sources, combinedParts, threshold); got != "" {
			t.Errorf("expected no warning when every source scores above threshold, got %q", got)
		}
	})

	t.Run("low-score source but small share", func(t *testing.T) {
		sources := []sourceOutput{
			{URL: "https://low.example.com", Scores: &content.QualityScore{Overall: 0.2}},
			{URL: "https://high.example.com", Scores: &content.QualityScore{Overall: 0.8}},
		}
		combinedParts := []string{strings.Repeat("a", 200), strings.Repeat("b", 800)}

		if got := lowQualityDominanceWarning(sources, combinedParts, threshold); got != "" {
			t.Errorf("expected no warning when the low-quality source's share is under 50%%, got %q", got)
		}
	})
}

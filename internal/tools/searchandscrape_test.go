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
// combined content's length. includedLens is what assembleCombined actually
// contributed per source (post-dedup, post-truncation) — see
// TestLowQualityDominanceWarning_UsesPostTruncationShare below for the case
// this distinction exists to fix.
func TestLowQualityDominanceWarning(t *testing.T) {
	const threshold = 0.4

	t.Run("low-score source dominates the combined output", func(t *testing.T) {
		sources := []sourceOutput{
			{URL: "https://low.example.com/rambling-thread", Scores: &content.QualityScore{Overall: 0.2}},
			{URL: "https://high.example.com/article", Scores: &content.QualityScore{Overall: 0.8}},
		}
		includedLens := []int{700, 300}

		got := lowQualityDominanceWarning(sources, includedLens, threshold)
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
		includedLens := []int{700, 300}

		if got := lowQualityDominanceWarning(sources, includedLens, threshold); got != "" {
			t.Errorf("expected no warning when every source scores above threshold, got %q", got)
		}
	})

	t.Run("low-score source but small share", func(t *testing.T) {
		sources := []sourceOutput{
			{URL: "https://low.example.com", Scores: &content.QualityScore{Overall: 0.2}},
			{URL: "https://high.example.com", Scores: &content.QualityScore{Overall: 0.8}},
		}
		includedLens := []int{200, 800}

		if got := lowQualityDominanceWarning(sources, includedLens, threshold); got != "" {
			t.Errorf("expected no warning when the low-quality source's share is under 50%%, got %q", got)
		}
	})
}

// TestLowQualityDominanceWarning_UsesPostTruncationShare is the fix on top of
// #667: a low-quality source's raw (pre-truncation) content can be small next
// to a high-quality source's raw content, so pre-truncation lengths alone
// would say "no dominance" — but if totalMaxLen truncates the high-quality
// source down hard while the low-quality source survives untouched, the
// low-quality source actually dominates the real combined output. The
// warning must fire on the truncated share, not the raw one.
func TestLowQualityDominanceWarning_UsesPostTruncationShare(t *testing.T) {
	sources := []sourceOutput{
		{URL: "https://low.example.com", Scores: &content.QualityScore{Overall: 0.2}},
		{URL: "https://high.example.com", Scores: &content.QualityScore{Overall: 0.8}},
	}
	// Raw: low=200 (20%), high=800 (80%) — would NOT fire on raw lengths.
	// Truncated: low fits whole (200), high gets cut to 100 — low now
	// dominates the actual combined output (200/300 = 67%).
	combinedParts := []string{strings.Repeat("a", 200), strings.Repeat("b", 800)}
	_, includedLens := assembleCombined(combinedParts, false, 300)

	got := lowQualityDominanceWarning(sources, includedLens, 0.4)
	if got == "" {
		t.Fatal("expected a warning based on post-truncation share, got none")
	}
	if !strings.Contains(got, "https://low.example.com") {
		t.Errorf("expected warning to name the low-quality source, got %q", got)
	}
}

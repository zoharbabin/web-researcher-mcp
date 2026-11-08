package content

import (
	"fmt"
	"strings"
	"testing"
)

// largeClaimPage builds a ~2 MB page (the scrape_page full-mode cap) of
// distinct paragraphs, with the supporting sentence placed near the middle.
func largeClaimPage(support string) string {
	var b strings.Builder
	inserted := false
	for i := 0; b.Len() < 2_000_000; i++ {
		if !inserted && b.Len() > 1_000_000 {
			b.WriteString(support + "\n\n")
			inserted = true
		}
		fmt.Fprintf(&b, "Paragraph %d: PUT and DELETE methods are discussed for topic %d. The idempotent property of methods matters for caches and retries.\n\n", i, i*7)
	}
	return b.String()
}

const largeClaim = "PUT and DELETE methods are idempotent"
const largeSupport = "The methods PUT and DELETE are defined to be idempotent."

// BenchmarkClaimCheck_2MB measures the two claim-check steps on a 2 MB page.
func BenchmarkClaimCheck_2MB(b *testing.B) {
	text := largeClaimPage(largeSupport)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ClaimTermCoverageWindowedSpan(text, largeClaim, 0)
		ExtractClaimEvidence(text, largeClaim)
	}
}

// TestClaimCheck_2MBFindsEvidence guards the cost and the result of the claim
// check on a page at the scrape cap (#852).
func TestClaimCheck_2MBFindsEvidence(t *testing.T) {
	text := largeClaimPage(largeSupport)
	if len(text) < 2_000_000 {
		t.Fatalf("page too small: %d", len(text))
	}
	ev := ExtractClaimEvidence(text, largeClaim)
	found := false
	for _, s := range ev.KeySentences {
		if strings.Contains(s, "defined to be idempotent") {
			found = true
		}
	}
	if !found {
		t.Errorf("evidence missed the supporting sentence: %v", ev.KeySentences)
	}
}

// A page that hard-wraps its lines (RFCs, plain text) must not turn each line
// into its own "sentence". Otherwise the sentence that states the claim is
// cut into fragments and loses to unrelated lines (#852).
func TestExtractClaimEvidence_HardWrappedSentence(t *testing.T) {
	t.Parallel()
	text := "Section 9.2.2\n\n" +
		"HTTP does not define exactly how a PUT method affects the state\n" +
		"of an origin server.\n\n" +
		"Of the request methods defined by this\n" +
		"specification, PUT, DELETE, and safe request\n" +
		"methods are idempotent.\n"
	ev := ExtractClaimEvidence(text, "PUT and DELETE methods are idempotent")
	want := "Of the request methods defined by this specification, PUT, DELETE, and safe request methods are idempotent."
	if ev.Signal != want {
		t.Errorf("Signal = %q, want %q", ev.Signal, want)
	}
}

func TestSplitSentences_SoftWrap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"wrapped line joins", "the quick brown fox\njumps over the lazy dog.", []string{"the quick brown fox jumps over the lazy dog."}},
		{"uppercase next line splits", "Introduction to the topic\nThe next heading here", []string{"Introduction to the topic", "The next heading here"}},
		{"terminal period splits", "A full sentence ends here.\nand a lowercase tail follows", []string{"A full sentence ends here.", "and a lowercase tail follows"}},
		{"blank line splits", "no punctuation here at all\n\nnext paragraph starts low", []string{"no punctuation here at all", "next paragraph starts low"}},
		{"colon splits", "the list follows:\nitem one is here", []string{"the list follows:", "item one is here"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitSentences(tt.in)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("splitSentences(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

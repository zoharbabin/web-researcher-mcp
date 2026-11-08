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

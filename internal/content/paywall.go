package content

import "strings"

// paywalledPublishers are news hosts that put most articles behind a
// subscription. Free outlets (BBC, Reuters, AP, the Guardian) are not listed.
var paywalledPublishers = []string{
	"wsj.com", "ft.com", "nytimes.com", "washingtonpost.com", "bloomberg.com", "economist.com",
}

// PaywallPreviewMaxWords is the longest page still treated as a possible
// preview. It is above SparseWordThreshold on purpose: a live WSJ preview
// measured 155 words, because headline, deck and byline repeat. Full articles
// run far longer.
const PaywallPreviewMaxWords = 400

// PaywallSuspected reports whether a thin page is probably the free preview of
// a paywalled article, not the article. words is the word count of the page as
// delivered.
//
// What the publisher declares decides first: schema.org isAccessibleForFree
// (sig.AccessibleForFree) says false for a paywalled page and true for a free
// one. Without that markup, which some extraction tiers strip, a thin page from
// a known paywalled publisher counts. A page of PaywallPreviewMaxWords words or
// more is never a preview. It is a heuristic: a prompt to check, not proof.
func PaywallSuspected(rawURL string, words int, sig StructuredSignals) bool {
	if words >= PaywallPreviewMaxWords {
		return false
	}
	if sig.AccessibleForFree != nil {
		return !*sig.AccessibleForFree
	}
	host := classifyHost(rawURL)
	for _, h := range paywalledPublishers {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

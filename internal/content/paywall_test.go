package content

import (
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestPaywallSuspected(t *testing.T) {
	t.Parallel()
	thin, full := PaywallPreviewMaxWords-1, PaywallPreviewMaxWords
	tests := []struct {
		name  string
		url   string
		words int
		sig   StructuredSignals
		want  bool
	}{
		{"markup says paywalled, thin", "https://example.com/a", thin, StructuredSignals{AccessibleForFree: boolPtr(false)}, true},
		{"markup says paywalled, full text", "https://example.com/a", full, StructuredSignals{AccessibleForFree: boolPtr(false)}, false},
		{"markup says free, thin, known publisher", "https://www.wsj.com/a", thin, StructuredSignals{AccessibleForFree: boolPtr(true)}, false},
		{"known publisher, thin, no markup", "https://www.wsj.com/articles/x", thin, StructuredSignals{}, true},
		{"known publisher, full text", "https://www.wsj.com/articles/x", full, StructuredSignals{}, false},
		{"known publisher, 155-word preview (live WSJ case)", "https://www.wsj.com/articles/x", 155, StructuredSignals{}, true},
		{"known publisher bare host", "https://nytimes.com/a", thin, StructuredSignals{}, true},
		{"known publisher subdomain", "https://markets.ft.com/a", thin, StructuredSignals{}, true},
		{"every listed publisher", "https://www.bloomberg.com/a", thin, StructuredSignals{}, true},
		{"unknown site, thin, no markup", "https://example.com/a", thin, StructuredSignals{}, false},
		{"free outlet", "https://www.bbc.com/news/a", thin, StructuredSignals{}, false},
		{"look-alike host", "https://notwsj.com/a", thin, StructuredSignals{}, false},
		{"publisher name in path only", "https://example.com/wsj.com", thin, StructuredSignals{}, false},
		{"bad url", "://", thin, StructuredSignals{}, false},
	}
	for _, tt := range tests {
		if got := PaywallSuspected(tt.url, tt.words, tt.sig); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestPaywalledPublishersAreRegistrableDomains(t *testing.T) {
	t.Parallel()
	for _, h := range paywalledPublishers {
		if h != strings.ToLower(h) || strings.HasPrefix(h, "www.") || strings.Count(h, ".") != 1 {
			t.Errorf("%q must be a lower-case registrable domain without www", h)
		}
	}
}

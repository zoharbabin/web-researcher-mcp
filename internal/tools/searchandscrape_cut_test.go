package tools

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// TestAssembleCombined_CutsOnBoundaryAndKeepsValidUTF8 is the #850 regression:
// a part cut by the total budget must end on a word or sentence boundary,
// never inside a character, and includedLens must count the bytes kept.
func TestAssembleCombined_CutsOnBoundaryAndKeepsValidUTF8(t *testing.T) {
	t.Run("multi-byte text without a nearby space", func(t *testing.T) {
		// Each character is 3 bytes, so a budget that is not a multiple of 3 lands inside one.
		part := strings.Repeat("日本語", 40)
		for budget := 20; budget < 30; budget++ {
			combined, lens := assembleCombined([]string{part}, false, budget)
			if !utf8.ValidString(combined) {
				t.Fatalf("budget %d: combined is not valid UTF-8: %q", budget, combined)
			}
			if len(combined) > budget {
				t.Fatalf("budget %d: kept %d bytes", budget, len(combined))
			}
			if lens[0] != len(combined) {
				t.Fatalf("budget %d: includedLens=%d, kept=%d", budget, lens[0], len(combined))
			}
		}
	})

	t.Run("second part cut at a word boundary", func(t *testing.T) {
		first := "Alpha beta gamma delta."
		second := "Épsilon zèta êta thêta iota kappa lambda mu nu xi omicron pi rho sigma tau"
		sep := "\n\n---\n\n"
		budget := len(first) + len(sep) + 30
		combined, lens := assembleCombined([]string{first, second}, false, budget)
		if !utf8.ValidString(combined) {
			t.Fatalf("combined is not valid UTF-8: %q", combined)
		}
		tail := strings.TrimPrefix(combined, first+sep)
		if tail == "" || !strings.HasPrefix(second, tail) {
			t.Fatalf("tail %q is not a prefix of the second part", tail)
		}
		if next := second[len(tail):]; !strings.HasPrefix(next, " ") {
			t.Errorf("cut is mid-word: kept %q, next %q", tail, next)
		}
		if lens[0] != len(first) || lens[1] != len(tail) {
			t.Errorf("includedLens = %v, want [%d %d]", lens, len(first), len(tail))
		}
	})

	t.Run("sentence boundary", func(t *testing.T) {
		part := "One short sentence. " + strings.Repeat("ünï ", 30)
		combined, lens := assembleCombined([]string{part}, false, 25)
		if combined != "One short sentence." {
			t.Errorf("got %q, want the first sentence", combined)
		}
		if lens[0] != len(combined) {
			t.Errorf("includedLens = %d, want %d", lens[0], len(combined))
		}
	})
}

// contextProvider is a search provider that also answers with server-side
// assembled context, which takes the search_and_scrape fast path.
type contextProvider struct {
	mockProvider
	text string
}

func (c *contextProvider) Context(_ context.Context, _ search.ContextParams) (*search.ContextResult, error) {
	return &search.ContextResult{
		Context:  c.text,
		Snippets: []search.ContextSnippet{{URL: "https://example.com/a", Title: "A", Text: "snippet"}},
		Source:   "ctx",
	}, nil
}

// TestSearchAndScrape_ContextPathCutsOnBoundary covers the context fast path
// of #850: the assembled context is cut to total_max_length on a word boundary,
// never inside a character.
func TestSearchAndScrape_ContextPathCutsOnBoundary(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		text string
		want func(t *testing.T, got string)
	}{
		"words": {
			// 9-byte words: the 100-byte limit falls inside a word.
			text: strings.Repeat("alphabet ", 50),
			want: func(t *testing.T, got string) {
				if !strings.HasSuffix(got, "alphabet") {
					t.Errorf("cut inside a word: %q", got)
				}
			},
		},
		"multi-byte": {
			// 3-byte characters, no spaces: 100 is not a multiple of 3.
			text: strings.Repeat("日本語", 50),
			want: func(t *testing.T, got string) {
				if !utf8.ValidString(got) {
					t.Errorf("cut inside a character: %q", got)
				}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deps := setupTestDeps()
			deps.Search = &contextProvider{text: tc.text}
			out := callToolJSON(t, deps, "search_and_scrape", map[string]any{
				"query":            "q",
				"total_max_length": 100,
			})
			got, _ := out["combinedContent"].(string)
			if got == "" || len(got) > 100 {
				t.Fatalf("combinedContent len %d, want 1..100: %q", len(got), got)
			}
			tc.want(t, got)
		})
	}
}

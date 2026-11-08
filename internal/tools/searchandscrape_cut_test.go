package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
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

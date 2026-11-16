package scraper

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeTierError_NeverSplitsCharacter(t *testing.T) {
	t.Parallel()
	// The leading "x" moves the 120-byte cut inside a 3-byte character.
	got := sanitizeTierError(errors.New("x" + strings.Repeat("日本語", 60)))
	if !utf8.ValidString(got) {
		t.Errorf("sanitizeTierError returned invalid UTF-8: %q", got)
	}
}

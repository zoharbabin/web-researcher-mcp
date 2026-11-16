package content

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCutAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		n    int
		want string
	}{
		{"ascii word boundary", "The quick brown fox jumps over the lazy dog", 30, "The quick brown fox jumps"},
		{"ascii sentence boundary", "First sentence is here. Second sentence goes on and on", 40, "First sentence is here."},
		{"paragraph boundary", "Alpha beta gamma.\n\nDelta epsilon zeta eta theta", 30, "Alpha beta gamma."},
		{"text fits", "short", 100, "short"},
		{"exact fit", "short", 5, "short"},
		{"n zero", "short", 0, ""},
		{"n negative", "short", -3, ""},
		{"empty text", "", 10, ""},
		{"no spaces ascii", strings.Repeat("a", 50), 20, strings.Repeat("a", 20)},
		// Each CJK character is 3 bytes. n=7 lands inside the third one.
		{"cjk no spaces", "日本語日本語", 7, "日本"},
		// Each emoji is 4 bytes. n=6 lands inside the second one.
		{"emoji no spaces", "😀😀😀😀", 6, "😀"},
		{"cjk with spaces", "日本語 日本語 日本語 日本語", 20, "日本語 日本語"},
		{"n smaller than first char", "日本語", 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CutAt(tc.text, tc.n)
			if got != tc.want {
				t.Errorf("CutAt(%q, %d) = %q, want %q", tc.text, tc.n, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("CutAt(%q, %d) returned invalid UTF-8: %q", tc.text, tc.n, got)
			}
			if tc.n > 0 && len(got) > tc.n {
				t.Errorf("CutAt(%q, %d) kept %d bytes, over the limit", tc.text, tc.n, len(got))
			}
		})
	}
}

func TestCutAt_MatchesTruncatePrefix(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("Words over the limit. ", 20)
	got := CutAt(text, 100)
	trunc, _ := Truncate(text, 100)
	if !strings.HasPrefix(trunc, got) {
		t.Errorf("CutAt result %q is not the prefix of Truncate output %q", got, trunc)
	}
}

func TestClampSnippet_NeverSplitsCharacter(t *testing.T) {
	t.Parallel()
	got := clampSnippet(strings.Repeat("日本語", 100))
	if !utf8.ValidString(got) {
		t.Errorf("clampSnippet returned invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clampSnippet lost the ellipsis: %q", got)
	}
}

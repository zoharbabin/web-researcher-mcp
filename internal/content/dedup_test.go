package content

import (
	"strings"
	"testing"
)

// paraOf builds a paragraph of exactly `words` space-separated words and
// `bytes` total bytes (words >= 1, bytes >= 2*words-1).
func paraOf(words, bytes int) string {
	letters := bytes - (words - 1)
	per := letters / words
	parts := make([]string, words)
	for i := range parts {
		n := per
		if i == words-1 {
			n = letters - per*(words-1)
		}
		parts[i] = strings.Repeat("a", n)
	}
	return strings.Join(parts, " ")
}

func TestDedup_Floor(t *testing.T) {
	t.Parallel()

	atFloor := paraOf(dedupMinWords, dedupMinBytes)
	if len(atFloor) != dedupMinBytes || WordCount(atFloor) != dedupMinWords {
		t.Fatalf("fixture wrong: %d bytes, %d words", len(atFloor), WordCount(atFloor))
	}

	tests := []struct {
		name string
		para string
		kept int // copies kept out of 3 identical inputs
	}{
		{name: "short ID", para: "A6283", kept: 3},
		{name: "N/A", para: "N/A", kept: 3},
		{name: "Active", para: "Active", kept: 3},
		{name: "label with value", para: "Algorithm ID: A6283", kept: 3},
		{name: "79 bytes, 8 words", para: paraOf(dedupMinWords, dedupMinBytes-1), kept: 3},
		{name: "80 bytes, 7 words", para: paraOf(dedupMinWords-1, dedupMinBytes), kept: 3},
		{name: "80 bytes, 8 words", para: atFloor, kept: 1},
		{name: "long cookie notice", para: "We use cookies to improve your experience on this site. By continuing you accept our cookie policy and terms of use.", kept: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Dedup([]string{tt.para, tt.para, tt.para})
			if len(got) != tt.kept {
				t.Errorf("kept %d copies, want %d", len(got), tt.kept)
			}
		})
	}
}

func TestDedup_DenseScriptLongParagraph(t *testing.T) {
	t.Parallel()
	// CJK text has no spaces. WordCount counts each rune, so a long repeated
	// paragraph is still removed.
	p := strings.Repeat("这是一个很长的重复段落", 4)
	if got := Dedup([]string{p, p}); len(got) != 1 {
		t.Errorf("kept %d copies of a long CJK paragraph, want 1", len(got))
	}
}

// cmvpFixture is shaped like a CMVP certificate page: each algorithm name is
// followed by the same short algorithm ID, and the page repeats a long notice.
func cmvpFixture(algs []string, id string) string {
	notice := "This site uses cookies to improve your experience. By continuing to browse you accept the use of cookies."
	var paras []string
	paras = append(paras, notice)
	for _, a := range algs {
		paras = append(paras, a, id, "Active", "N/A")
	}
	paras = append(paras, notice, notice)
	return strings.Join(paras, "\n\n")
}

func TestProcess_CMVPShapedFixtureKeepsEveryID(t *testing.T) {
	t.Parallel()
	algs := []string{"AES-CCM", "AES-CMAC", "AES-GCM", "AES-ECB", "HMAC-SHA2-256", "SHA2-256", "DRBG", "KAS-ECC"}
	raw := cmvpFixture(algs, "A6283")

	got, truncated := NewProcessor().Process(raw, 30000)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if n := strings.Count(got, "A6283"); n != len(algs) {
		t.Errorf("A6283 appears %d times, want %d", n, len(algs))
	}
	if n := strings.Count(got, "Active"); n != len(algs) {
		t.Errorf("Active appears %d times, want %d", n, len(algs))
	}
	if n := strings.Count(got, "This site uses cookies"); n != 1 {
		t.Errorf("cookie notice appears %d times, want 1", n)
	}
}

func TestSparsityWarning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		text     string
		wantWord int
		wantWarn bool
	}{
		{name: "empty", text: "", wantWord: 0, wantWarn: true},
		{name: "below threshold", text: strings.Repeat("word ", SparseWordThreshold-1), wantWord: SparseWordThreshold - 1, wantWarn: true},
		{name: "at threshold", text: strings.Repeat("word ", SparseWordThreshold), wantWord: SparseWordThreshold, wantWarn: false},
		{name: "dense script", text: strings.Repeat("字", SparseWordThreshold), wantWord: SparseWordThreshold, wantWarn: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			words, warn := SparsityWarning(tt.text)
			if words != tt.wantWord {
				t.Errorf("words = %d, want %d", words, tt.wantWord)
			}
			if (warn != "") != tt.wantWarn {
				t.Errorf("warning = %q, want present=%v", warn, tt.wantWarn)
			}
			if tt.wantWarn && !strings.Contains(warn, "thin") {
				t.Errorf("warning %q should say the content is thin", warn)
			}
		})
	}
}

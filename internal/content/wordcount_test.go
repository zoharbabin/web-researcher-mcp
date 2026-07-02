package content

import "testing"

func TestWordCount(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"simple ascii", "the quick brown fox", 4},
		{"extra whitespace collapses", "  the   quick  ", 2},
		{"korean is space separated already", "안녕 하세요 세계", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WordCount(tt.text); got != tt.want {
				t.Errorf("WordCount(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

// TestWordCount_DenseScripts verifies CJK/Thai/Lao/Khmer/Myanmar text — which
// has no inter-word spaces — counts each rune as a word instead of collapsing
// to a handful of whitespace-delimited chunks (the strings.Fields bug this
// helper replaces).
func TestWordCount_DenseScripts(t *testing.T) {
	// 11 Han characters, no spaces.
	chinese := "这是一段完整的中文内容"
	if got := WordCount(chinese); got != 11 {
		t.Errorf("WordCount(chinese) = %d, want 11 (rune count)", got)
	}

	// Mixed: an ASCII word plus a run of dense-script runes.
	mixed := "prefix 你好世界"
	if got := WordCount(mixed); got != 5 { // "prefix" + 4 Han runes
		t.Errorf("WordCount(mixed) = %d, want 5", got)
	}
}

func TestWordCount_ClearsSparsityThresholdForCJK(t *testing.T) {
	// A genuine, complete article-length Chinese paragraph (well over 150
	// characters, zero ASCII whitespace) must not collapse to ~1 "word".
	article := ""
	for i := 0; i < 4; i++ {
		article += "这是一段完整的中文新闻内容用于测试提取质量与字数统计逻辑是否正确处理非拉丁语言的文本"
	}
	if got := WordCount(article); got < 150 {
		t.Errorf("WordCount(article) = %d, want >= 150 for a genuinely long CJK article", got)
	}
}

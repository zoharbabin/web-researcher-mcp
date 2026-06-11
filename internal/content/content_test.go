package content

import (
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Sanitization Tests
// =============================================================================

func TestSanitizeHTML_XSSRemoval(t *testing.T) {
	s := NewSanitizer()

	tests := []struct {
		name     string
		input    string
		contains string
		excludes string
	}{
		{
			name:     "removes script tags",
			input:    `<p>Hello</p><script>alert('xss')</script>`,
			contains: "<p>Hello</p>",
			excludes: "<script>",
		},
		{
			name:     "removes onclick handlers",
			input:    `<a href="https://example.com" onclick="evil()">link</a>`,
			contains: "link",
			excludes: "onclick",
		},
		{
			name:     "removes javascript href",
			input:    `<a href="javascript:alert(1)">click</a>`,
			contains: "click",
			excludes: "javascript:",
		},
		{
			name:     "removes iframe",
			input:    `<p>Safe</p><iframe src="evil.com"></iframe>`,
			contains: "Safe",
			excludes: "<iframe",
		},
		{
			name:     "removes onerror on img",
			input:    `<img src="x.png" alt="pic" onerror="alert(1)">`,
			contains: `src="x.png"`,
			excludes: "onerror",
		},
		{
			name:     "allows safe HTML elements",
			input:    `<h1>Title</h1><p>Paragraph with <strong>bold</strong> and <em>italic</em></p>`,
			contains: "<h1>Title</h1>",
			excludes: "",
		},
		{
			name:     "allows safe links with nofollow",
			input:    `<a href="https://example.com">Example</a>`,
			contains: `rel="nofollow"`,
			excludes: "",
		},
		{
			name:     "removes style tags",
			input:    `<style>body{display:none}</style><p>visible</p>`,
			contains: "visible",
			excludes: "<style>",
		},
		{
			name:     "removes object/embed tags",
			input:    `<object data="evil.swf"></object><embed src="evil.swf"><p>ok</p>`,
			contains: "ok",
			excludes: "<object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.SanitizeHTML(tt.input)
			if tt.contains != "" && !strings.Contains(result, tt.contains) {
				t.Errorf("expected result to contain %q, got %q", tt.contains, result)
			}
			if tt.excludes != "" && strings.Contains(result, tt.excludes) {
				t.Errorf("expected result to NOT contain %q, got %q", tt.excludes, result)
			}
		})
	}
}

func TestSanitizeText_ZeroWidthCharRemoval(t *testing.T) {
	s := NewSanitizer()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "removes zero-width space U+200B",
			input: "hello​world",
			want:  "helloworld",
		},
		{
			name:  "removes zero-width non-joiner U+200C",
			input: "test‌word",
			want:  "testword",
		},
		{
			name:  "removes zero-width joiner U+200D",
			input: "foo‍bar",
			want:  "foobar",
		},
		{
			name:  "removes BOM U+FEFF",
			input: "\uFEFF" + "hello",
			want:  "hello",
		},
		{
			name:  "removes word joiner U+2060",
			input: "word⁠break",
			want:  "wordbreak",
		},
		{
			name:  "removes multiple zero-width chars",
			input: "​‌‍\uFEFF⁠text​‌",
			want:  "text",
		},
		{
			name:  "preserves normal text without zero-width chars",
			input: "normal text here",
			want:  "normal text here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.SanitizeText(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeText_HiddenCSSRemoval(t *testing.T) {
	s := NewSanitizer()

	tests := []struct {
		name     string
		input    string
		contains string
		excludes string
	}{
		{
			name:     "removes display:none line",
			input:    "visible text\ndisplay: none; some hidden content\nmore visible",
			contains: "visible text",
			excludes: "hidden content",
		},
		{
			name:     "removes visibility:hidden line",
			input:    "show this\nvisibility: hidden stuff here\nand this",
			contains: "show this",
			excludes: "hidden stuff",
		},
		{
			name:     "removes font-size:0 line",
			input:    "keep\nfont-size: 0 hidden text\nalso keep",
			contains: "keep",
			excludes: "hidden text",
		},
		{
			name:     "case insensitive hidden CSS detection",
			input:    "visible\nDISPLAY: NONE sneaky\nstill visible",
			contains: "visible",
			excludes: "sneaky",
		},
		{
			name:     "handles display:none with no space",
			input:    "ok\ndisplay:none\nfine",
			contains: "fine",
			excludes: "display:none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.SanitizeText(tt.input)
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("expected result to contain %q, got %q", tt.contains, got)
			}
			if tt.excludes != "" && strings.Contains(got, tt.excludes) {
				t.Errorf("expected result to NOT contain %q, got %q", tt.excludes, got)
			}
		})
	}
}

func TestSanitizeText_WhitespaceNormalization(t *testing.T) {
	s := NewSanitizer()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "collapses multiple newlines to two",
			input: "para1\n\n\n\n\npara2",
			want:  "para1\n\npara2",
		},
		{
			name:  "collapses multiple spaces to one",
			input: "hello    world",
			want:  "hello world",
		},
		{
			name:  "collapses tabs to one space",
			input: "hello\t\tworld",
			want:  "hello world",
		},
		{
			name:  "trims leading and trailing whitespace",
			input: "   hello world   ",
			want:  "hello world",
		},
		{
			name:  "handles empty string",
			input: "",
			want:  "",
		},
		{
			name:  "handles only whitespace",
			input: "   \n\n\n   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.SanitizeText(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Truncation Tests
// =============================================================================

func TestTruncate_NoTruncationNeeded(t *testing.T) {
	content := "Short content"
	result, truncated := Truncate(content, 100)
	if truncated {
		t.Error("expected no truncation for short content")
	}
	if result != content {
		t.Errorf("expected unchanged content, got %q", result)
	}
}

func TestTruncate_ExactLength(t *testing.T) {
	content := "Exact"
	result, truncated := Truncate(content, 5)
	if truncated {
		t.Error("expected no truncation when content equals maxLength")
	}
	if result != content {
		t.Errorf("expected unchanged content, got %q", result)
	}
}

func TestTruncate_ParagraphBoundary(t *testing.T) {
	// Build content where a paragraph boundary exists in the second half
	para1 := strings.Repeat("a", 60)
	para2 := strings.Repeat("b", 60)
	content := para1 + "\n\n" + para2
	maxLen := 100

	result, truncated := Truncate(content, maxLen)
	if !truncated {
		t.Error("expected truncation")
	}
	if !strings.HasSuffix(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
	// Should have cut at paragraph boundary
	if !strings.Contains(result, para1) {
		t.Errorf("expected first paragraph to be preserved, got %q", result)
	}
}

func TestTruncate_SentenceBoundary(t *testing.T) {
	// No paragraph boundary, but has sentence boundary
	content := strings.Repeat("a", 40) + ". " + strings.Repeat("b", 60)
	maxLen := 80

	result, truncated := Truncate(content, maxLen)
	if !truncated {
		t.Error("expected truncation")
	}
	// Should have cut at sentence boundary (". ")
	if !strings.HasSuffix(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
	// The result before the marker should end with "."
	beforeMarker := strings.TrimSuffix(result, "\n\n[content truncated]")
	if !strings.HasSuffix(beforeMarker, ".") {
		t.Errorf("expected cut at sentence boundary, got %q", beforeMarker)
	}
}

func TestTruncate_WordBoundary(t *testing.T) {
	// No paragraph or sentence boundary, just word boundary
	content := "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11 word12 word13 word14 word15"
	maxLen := 50

	result, truncated := Truncate(content, maxLen)
	if !truncated {
		t.Error("expected truncation")
	}
	if !strings.HasSuffix(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
	// Should not cut in the middle of a word
	beforeMarker := strings.TrimSuffix(result, "\n\n[content truncated]")
	if strings.HasSuffix(beforeMarker, "wor") {
		t.Error("should not cut in middle of word")
	}
}

func TestTruncate_HardCut(t *testing.T) {
	// No spaces or boundaries at all - one continuous string
	content := strings.Repeat("x", 200)
	maxLen := 100

	result, truncated := Truncate(content, maxLen)
	if !truncated {
		t.Error("expected truncation")
	}
	if !strings.HasSuffix(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
	// Should hard cut at maxLen
	beforeMarker := strings.TrimSuffix(result, "\n\n[content truncated]")
	if len(beforeMarker) != maxLen {
		t.Errorf("expected hard cut at %d, got length %d", maxLen, len(beforeMarker))
	}
}

func TestTruncate_BoundaryInFirstHalf(t *testing.T) {
	// Paragraph boundary exists but in the first half (idx < maxLength/2)
	// Should fall through to next boundary type
	content := "a\n\n" + strings.Repeat("b", 200)
	maxLen := 100

	result, truncated := Truncate(content, maxLen)
	if !truncated {
		t.Error("expected truncation")
	}
	// The paragraph boundary at position 1 is < maxLen/2 (50), so it should skip it
	// and try sentence/newline/word boundary or hard cut
	if !strings.HasSuffix(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{name: "empty", content: "", want: 0},
		{name: "4 chars", content: "test", want: 1},
		{name: "8 chars", content: "testtest", want: 2},
		{name: "100 chars", content: strings.Repeat("a", 100), want: 25},
		{name: "3 chars rounds down", content: "abc", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.content)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

func TestSizeCategory(t *testing.T) {
	tests := []struct {
		name   string
		length int
		want   string
	}{
		{name: "zero is small", length: 0, want: "small"},
		{name: "4999 is small", length: 4999, want: "small"},
		{name: "5000 is medium", length: 5000, want: "medium"},
		{name: "19999 is medium", length: 19999, want: "medium"},
		{name: "20000 is large", length: 20000, want: "large"},
		{name: "49999 is large", length: 49999, want: "large"},
		{name: "50000 is very_large", length: 50000, want: "very_large"},
		{name: "100000 is very_large", length: 100000, want: "very_large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SizeCategory(tt.length)
			if got != tt.want {
				t.Errorf("SizeCategory(%d) = %q, want %q", tt.length, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Dedup Tests
// =============================================================================

func TestDedup_RemovesDuplicates(t *testing.T) {
	paragraphs := []string{
		"First paragraph",
		"Second paragraph",
		"First paragraph",
		"Third paragraph",
		"Second paragraph",
	}

	result := Dedup(paragraphs)
	if len(result) != 3 {
		t.Errorf("expected 3 unique paragraphs, got %d: %v", len(result), result)
	}
	expected := []string{"First paragraph", "Second paragraph", "Third paragraph"}
	for i, want := range expected {
		if result[i] != want {
			t.Errorf("result[%d] = %q, want %q", i, result[i], want)
		}
	}
}

func TestDedup_PreservesUniqueContent(t *testing.T) {
	paragraphs := []string{
		"Alpha",
		"Beta",
		"Gamma",
		"Delta",
	}

	result := Dedup(paragraphs)
	if len(result) != 4 {
		t.Errorf("expected 4 paragraphs, got %d", len(result))
	}
}

func TestDedup_SkipsEmptyParagraphs(t *testing.T) {
	paragraphs := []string{
		"Content",
		"",
		"   ",
		"More content",
		"\t",
	}

	result := Dedup(paragraphs)
	if len(result) != 2 {
		t.Errorf("expected 2 non-empty paragraphs, got %d: %v", len(result), result)
	}
}

func TestDedup_AllDuplicates(t *testing.T) {
	paragraphs := []string{"Same", "Same", "Same"}
	result := Dedup(paragraphs)
	if len(result) != 1 {
		t.Errorf("expected 1 unique paragraph, got %d", len(result))
	}
}

func TestDedup_NilInput(t *testing.T) {
	result := Dedup(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}

func TestDedupContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "removes duplicate paragraphs from text",
			input: "First para\n\nSecond para\n\nFirst para\n\nThird para",
			want:  "First para\n\nSecond para\n\nThird para",
		},
		{
			name:  "preserves unique content",
			input: "One\n\nTwo\n\nThree",
			want:  "One\n\nTwo\n\nThree",
		},
		{
			name:  "handles single paragraph",
			input: "Just one paragraph",
			want:  "Just one paragraph",
		},
		{
			name:  "handles empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DedupContent(tt.input)
			if got != tt.want {
				t.Errorf("DedupContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDjb2_Deterministic(t *testing.T) {
	input := "hello world"
	h1 := djb2(input)
	h2 := djb2(input)
	if h1 != h2 {
		t.Errorf("djb2 should be deterministic: %d != %d", h1, h2)
	}
}

func TestDjb2_DifferentStrings(t *testing.T) {
	h1 := djb2("hello")
	h2 := djb2("world")
	if h1 == h2 {
		t.Error("djb2 produced same hash for different strings")
	}
}

// =============================================================================
// Quality Scoring Tests
// =============================================================================

func TestScoreQuality_FullInput(t *testing.T) {
	input := QualityInput{
		Content:     strings.Repeat("This is a test sentence about Go programming. ", 50),
		URL:         "https://github.com/example/repo",
		Title:       "Go Programming Guide",
		Query:       "Go programming",
		PublishedAt: time.Now().Add(-2 * time.Hour),
	}

	score := ScoreQuality(input)

	if score.Overall < 0 || score.Overall > 1 {
		t.Errorf("overall score should be [0,1], got %f", score.Overall)
	}
	if score.Relevance < 0 || score.Relevance > 1 {
		t.Errorf("relevance should be [0,1], got %f", score.Relevance)
	}
	if score.Freshness < 0 || score.Freshness > 1 {
		t.Errorf("freshness should be [0,1], got %f", score.Freshness)
	}
	if score.Authority < 0 || score.Authority > 1 {
		t.Errorf("authority should be [0,1], got %f", score.Authority)
	}
	if score.ContentQuality < 0 || score.ContentQuality > 1 {
		t.Errorf("contentQuality should be [0,1], got %f", score.ContentQuality)
	}
}

func TestScoreRelevance(t *testing.T) {
	tests := []struct {
		name    string
		content string
		title   string
		query   string
		minWant float64
		maxWant float64
	}{
		{
			name:    "perfect match",
			content: "golang programming tutorial for beginners",
			title:   "Golang Programming Tutorial",
			query:   "golang programming",
			minWant: 0.8,
			maxWant: 1.0,
		},
		{
			name:    "no match",
			content: "cooking recipes for italian food",
			title:   "Italian Recipes",
			query:   "quantum physics",
			minWant: 0.0,
			maxWant: 0.1,
		},
		{
			name:    "partial match in content only",
			content: "this article discusses golang performance",
			title:   "Performance Analysis",
			query:   "golang speed",
			minWant: 0.2,
			maxWant: 0.7,
		},
		{
			name:    "empty query returns 0.5",
			content: "some content",
			title:   "Some Title",
			query:   "",
			minWant: 0.5,
			maxWant: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scoreRelevance(tt.content, tt.title, tt.query)
			if got < tt.minWant || got > tt.maxWant {
				t.Errorf("scoreRelevance() = %f, want [%f, %f]", got, tt.minWant, tt.maxWant)
			}
		})
	}
}

func TestScoreFreshness(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		want float64
	}{
		{name: "today", age: 1 * time.Hour, want: 1.0},
		{name: "3 days ago", age: 3 * 24 * time.Hour, want: 0.9},
		{name: "2 weeks ago", age: 14 * 24 * time.Hour, want: 0.7},
		{name: "6 months ago", age: 180 * 24 * time.Hour, want: 0.5},
		{name: "2 years ago", age: 730 * 24 * time.Hour, want: 0.3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publishedAt := time.Now().Add(-tt.age)
			got := scoreFreshness(publishedAt)
			if got != tt.want {
				t.Errorf("scoreFreshness() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestScoreFreshness_ZeroTime(t *testing.T) {
	got := scoreFreshness(time.Time{})
	if got != 0.5 {
		t.Errorf("scoreFreshness(zero) = %f, want 0.5", got)
	}
}

func TestScoreAuthority(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want float64
	}{
		{name: "gov site", url: "https://www.cdc.gov/health", want: 0.9},
		{name: "edu site", url: "https://stanford.edu/research", want: 0.9},
		{name: "github", url: "https://github.com/user/repo", want: 0.9},
		{name: "stackoverflow", url: "https://stackoverflow.com/questions/123", want: 0.9},
		{name: "arxiv", url: "https://arxiv.org/abs/2301.01234", want: 0.9},
		{name: "wikipedia", url: "https://en.wikipedia.org/wiki/Test", want: 0.9},
		{name: "mozilla mdn", url: "https://developer.mozilla.org/en-US/docs", want: 0.9},
		{name: "medium", url: "https://medium.com/article", want: 0.7},
		{name: "reddit", url: "https://reddit.com/r/golang", want: 0.7},
		{name: "bbc", url: "https://www.bbc.com/news", want: 0.7},
		{name: "nytimes", url: "https://www.nytimes.com/article", want: 0.7},
		{name: "unknown domain", url: "https://randomsite.xyz/page", want: 0.5},
		{name: "empty url", url: "", want: 0.5},
		// Reputation-dataset fallback (#213): tier:high host not in the hardcoded list.
		{name: "courtlistener (reputation high)", url: "https://www.courtlistener.com/opinion/1/mock/", want: 0.9},
		// law.cornell.edu is .edu so caught by the hardcoded list already.
		{name: "law.cornell.edu (edu)", url: "https://law.cornell.edu/uscode/", want: 0.9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scoreAuthority(tt.url)
			if got != tt.want {
				t.Errorf("scoreAuthority(%q) = %f, want %f", tt.url, got, tt.want)
			}
		})
	}
}

func TestScoreContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		minWant float64
		maxWant float64
	}{
		{
			name:    "empty content",
			content: "",
			minWant: 0.0,
			maxWant: 0.0,
		},
		{
			name:    "very short content",
			content: "Hello",
			minWant: 0.0,
			maxWant: 0.3,
		},
		{
			name: "rich long content with paragraphs and links",
			content: strings.Repeat("This is a well-written paragraph with multiple sentences. It covers important topics. ", 20) +
				"\n\n" + strings.Repeat("Another paragraph here with more details. Very informative content. ", 20) +
				"\n\n" + strings.Repeat("Third paragraph discusses http://example.com links. More sentences follow. ", 20) +
				"\n\n" + "Final paragraph with concluding thoughts. This wraps up everything nicely.",
			minWant: 0.7,
			maxWant: 1.0,
		},
		{
			name:    "moderate content",
			content: strings.Repeat("A sentence here. ", 40) + "\n\n" + strings.Repeat("More text. ", 30),
			minWant: 0.3,
			maxWant: 0.8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scoreContent(tt.content)
			if got < tt.minWant || got > tt.maxWant {
				t.Errorf("scoreContent() = %f, want [%f, %f]", got, tt.minWant, tt.maxWant)
			}
		})
	}
}

func TestScoreQuality_Weights(t *testing.T) {
	// Verify the overall score is a weighted sum: 0.35*rel + 0.20*fresh + 0.25*auth + 0.20*content
	input := QualityInput{
		Content:     strings.Repeat("Go programming is great. ", 100),
		URL:         "https://github.com/test",
		Title:       "Go Programming",
		Query:       "Go programming",
		PublishedAt: time.Now().Add(-2 * time.Hour),
	}

	score := ScoreQuality(input)

	// Manually compute expected overall
	expectedOverall := score.Relevance*0.35 + score.Freshness*0.20 + score.Authority*0.25 + score.ContentQuality*0.20
	// Allow for rounding differences
	diff := score.Overall - expectedOverall
	if diff > 0.02 || diff < -0.02 {
		t.Errorf("overall %f does not match weighted sum %f (diff=%f)", score.Overall, expectedOverall, diff)
	}
}

// =============================================================================
// Citation Tests
// =============================================================================

func TestExtractCitation_APA(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		title    string
		author   string
		siteName string
		pubDate  string
		contains []string
		excludes []string
	}{
		{
			name:     "full citation",
			url:      "https://example.com/article",
			title:    "Test Article",
			author:   "Smith, J.",
			siteName: "Example Site",
			pubDate:  "2024-01-15",
			contains: []string{"Smith, J.", "(2024-01-15).", "Test Article.", "Example Site.", "https://example.com/article"},
			// Author already ends in an initial+period — must not double to "J..".
			excludes: []string{".."},
		},
		{
			name:     "author ending in initial does not double the period",
			url:      "https://www.nature.com/articles/s41586-021-03819-2",
			title:    "Highly accurate protein structure prediction with AlphaFold",
			author:   "Jumper, J.; Hassabis, D.",
			siteName: "Nature",
			pubDate:  "2021",
			contains: []string{"Jumper, J.; Hassabis, D.", "(2021)."},
			excludes: []string{".."},
		},
		{
			name:     "no author",
			url:      "https://example.com/page",
			title:    "Page Title",
			author:   "",
			siteName: "Site",
			pubDate:  "2024-03-01",
			contains: []string{"(2024-03-01).", "Page Title.", "Site."},
			excludes: []string{".."},
		},
		{
			name:     "no date shows n.d.",
			url:      "https://example.com",
			title:    "Undated Article",
			author:   "Doe, A.",
			siteName: "News",
			pubDate:  "",
			contains: []string{"(n.d.).", "Undated Article."},
			excludes: nil,
		},
		{
			name:     "minimal citation",
			url:      "https://example.com",
			title:    "",
			author:   "",
			siteName: "",
			pubDate:  "",
			contains: []string{"(n.d.).", "https://example.com"},
			excludes: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ExtractCitation(tt.url, tt.title, tt.author, tt.siteName, tt.pubDate)

			for _, want := range tt.contains {
				if !strings.Contains(c.Formatted.APA, want) {
					t.Errorf("APA citation missing %q, got: %q", want, c.Formatted.APA)
				}
			}
			for _, notWant := range tt.excludes {
				if strings.Contains(c.Formatted.APA, notWant) {
					t.Errorf("APA citation should not contain %q, got: %q", notWant, c.Formatted.APA)
				}
			}
		})
	}
}

func TestExtractCitation_MLA(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		title    string
		author   string
		siteName string
		pubDate  string
		contains []string
	}{
		{
			name:     "full citation",
			url:      "https://example.com/article",
			title:    "Test Article",
			author:   "Smith, John",
			siteName: "Example Site",
			pubDate:  "15 Jan. 2024",
			contains: []string{"Smith, John.", "\"Test Article.\"", "Example Site,", "15 Jan. 2024,", "https://example.com/article.", "Accessed"},
		},
		{
			name:     "no author MLA",
			url:      "https://example.com/page",
			title:    "Page Title",
			author:   "",
			siteName: "Site",
			pubDate:  "2024",
			contains: []string{"\"Page Title.\"", "Site,", "2024,", "Accessed"},
		},
		{
			name:     "no date MLA",
			url:      "https://example.com",
			title:    "Undated",
			author:   "Author",
			siteName: "",
			pubDate:  "",
			contains: []string{"Author.", "\"Undated.\"", "Accessed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ExtractCitation(tt.url, tt.title, tt.author, tt.siteName, tt.pubDate)

			for _, want := range tt.contains {
				if !strings.Contains(c.Formatted.MLA, want) {
					t.Errorf("MLA citation missing %q, got: %q", want, c.Formatted.MLA)
				}
			}
		})
	}
}

func TestExtractCitation_Metadata(t *testing.T) {
	c := ExtractCitation("https://example.com", "Title", "Author", "Site", "2024")

	if c.URL != "https://example.com" {
		t.Errorf("URL = %q, want %q", c.URL, "https://example.com")
	}
	if c.Metadata.Title != "Title" {
		t.Errorf("Metadata.Title = %q, want %q", c.Metadata.Title, "Title")
	}
	if c.Metadata.Author != "Author" {
		t.Errorf("Metadata.Author = %q, want %q", c.Metadata.Author, "Author")
	}
	if c.Metadata.Site != "Site" {
		t.Errorf("Metadata.Site = %q, want %q", c.Metadata.Site, "Site")
	}
	if c.Metadata.Date != "2024" {
		t.Errorf("Metadata.Date = %q, want %q", c.Metadata.Date, "2024")
	}
	if c.AccessedDate == "" {
		t.Error("AccessedDate should not be empty")
	}
	// AccessedDate should be today's date
	today := time.Now().Format("2006-01-02")
	if c.AccessedDate != today {
		t.Errorf("AccessedDate = %q, want %q", c.AccessedDate, today)
	}
}

// =============================================================================
// Processor Tests (End-to-End Pipeline)
// =============================================================================

func TestProcessor_Process_Basic(t *testing.T) {
	p := NewProcessor()

	raw := "Hello world. This is a test."
	result, truncated := p.Process(raw, 1000)
	if truncated {
		t.Error("expected no truncation")
	}
	if result != raw {
		t.Errorf("Process() = %q, want %q", result, raw)
	}
}

func TestProcessor_Process_SanitizesAndDedupsAndTruncates(t *testing.T) {
	p := NewProcessor()

	// Input with zero-width chars, duplicates, and excessive length
	raw := "​First paragraph\n\n" +
		"Second paragraph\n\n" +
		"First paragraph\n\n" + // duplicate
		"Third paragraph‍"

	result, truncated := p.Process(raw, 1000)
	if truncated {
		t.Error("expected no truncation for short content")
	}

	// Zero-width chars should be removed
	if strings.Contains(result, "​") || strings.Contains(result, "‍") {
		t.Error("zero-width chars should be removed")
	}

	// Duplicate paragraph should be removed
	count := strings.Count(result, "First paragraph")
	if count != 1 {
		t.Errorf("expected 1 occurrence of 'First paragraph', got %d", count)
	}

	// Should contain all unique paragraphs
	if !strings.Contains(result, "Second paragraph") {
		t.Error("missing 'Second paragraph'")
	}
	if !strings.Contains(result, "Third paragraph") {
		t.Error("missing 'Third paragraph'")
	}
}

func TestProcessor_Process_TruncatesLongContent(t *testing.T) {
	p := NewProcessor()

	raw := strings.Repeat("This is a paragraph. ", 100)
	result, truncated := p.Process(raw, 200)
	if !truncated {
		t.Error("expected truncation for long content")
	}
	if !strings.Contains(result, "[content truncated]") {
		t.Error("expected truncation marker")
	}
}

func TestProcessor_Process_HiddenCSSRemoval(t *testing.T) {
	p := NewProcessor()

	raw := "Visible content\ndisplay: none; hidden stuff\nMore visible"
	result, _ := p.Process(raw, 1000)

	if strings.Contains(result, "hidden stuff") {
		t.Error("hidden CSS content should be removed")
	}
	if !strings.Contains(result, "Visible content") {
		t.Error("visible content should be preserved")
	}
}

func TestProcessor_Process_EmptyInput(t *testing.T) {
	p := NewProcessor()

	result, truncated := p.Process("", 1000)
	if truncated {
		t.Error("empty input should not be truncated")
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}

func TestProcessor_Process_WhitespaceOnlyInput(t *testing.T) {
	p := NewProcessor()

	result, truncated := p.Process("   \n\n\n   ", 1000)
	if truncated {
		t.Error("whitespace-only input should not be truncated")
	}
	if result != "" {
		t.Errorf("expected empty result after sanitization, got %q", result)
	}
}

func TestProcessor_SanitizeHTML(t *testing.T) {
	p := NewProcessor()

	html := `<p>Hello</p><script>alert('xss')</script><a href="https://safe.com">link</a>`
	result := p.SanitizeHTML(html)

	if strings.Contains(result, "<script>") {
		t.Error("script tag should be removed")
	}
	if !strings.Contains(result, "<p>Hello</p>") {
		t.Error("safe p tag should be preserved")
	}
	if !strings.Contains(result, "link") {
		t.Error("link text should be preserved")
	}
}

func TestProcessor_SanitizeText(t *testing.T) {
	p := NewProcessor()

	text := "​hello​    world\n\n\n\nmulti"
	result := p.SanitizeText(text)

	if strings.Contains(result, "​") {
		t.Error("zero-width chars should be removed")
	}
	if strings.Contains(result, "    ") {
		t.Error("multiple spaces should be collapsed")
	}
	if strings.Contains(result, "\n\n\n\n") {
		t.Error("multiple newlines should be collapsed")
	}
}

func TestProcessor_Process_CombinedPipeline(t *testing.T) {
	p := NewProcessor()

	// Comprehensive test with all pipeline stages involved
	raw := "​\uFEFF" + // zero-width chars
		"Important paragraph about testing.\n\n" +
		"display: none; sneaky hidden content\n\n" + // hidden CSS
		"Important paragraph about testing.\n\n" + // duplicate
		"   Another   unique   paragraph   here.   \n\n" + // extra spaces
		strings.Repeat("Filler text to make it long. ", 50) // will be truncated

	result, truncated := p.Process(raw, 200)

	// Should be truncated
	if !truncated {
		t.Error("expected truncation")
	}
	// No zero-width chars
	if strings.Contains(result, "​") || strings.Contains(result, "\uFEFF") {
		t.Error("zero-width chars should be removed")
	}
	// Hidden content removed
	if strings.Contains(result, "sneaky hidden") {
		t.Error("hidden CSS content should be removed")
	}
	// Should have truncation marker
	if !strings.Contains(result, "[content truncated]") {
		t.Error("expected truncation marker")
	}
}

// =============================================================================
// Edge Cases and Boundary Tests
// =============================================================================

func TestDedup_LargeInput(t *testing.T) {
	// Test with many paragraphs
	var paragraphs []string
	for i := 0; i < 1000; i++ {
		paragraphs = append(paragraphs, "Repeated paragraph")
	}
	paragraphs = append(paragraphs, "Unique one")

	result := Dedup(paragraphs)
	if len(result) != 2 {
		t.Errorf("expected 2 unique paragraphs from 1001 inputs, got %d", len(result))
	}
}

func TestTruncate_MaxLengthZero(t *testing.T) {
	content := "hello"
	result, truncated := Truncate(content, 0)
	if !truncated {
		t.Error("expected truncation when maxLength is 0")
	}
	if !strings.Contains(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
}

func TestTruncate_MaxLengthOne(t *testing.T) {
	content := "hello world"
	result, truncated := Truncate(content, 1)
	if !truncated {
		t.Error("expected truncation when maxLength is 1")
	}
	if !strings.Contains(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
}

func TestScoreQuality_EmptyInput(t *testing.T) {
	input := QualityInput{}
	score := ScoreQuality(input)

	if score.Relevance != 0.5 {
		t.Errorf("empty query should give relevance 0.5, got %f", score.Relevance)
	}
	if score.Freshness != 0.5 {
		t.Errorf("zero time should give freshness 0.5, got %f", score.Freshness)
	}
	if score.Authority != 0.5 {
		t.Errorf("empty URL should give authority 0.5, got %f", score.Authority)
	}
	if score.ContentQuality != 0 {
		t.Errorf("empty content should give quality 0, got %f", score.ContentQuality)
	}
}

func TestSanitizeHTML_PreservesStructure(t *testing.T) {
	s := NewSanitizer()

	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "preserves table structure",
			html: `<table><thead><tr><th>Header</th></tr></thead><tbody><tr><td>Data</td></tr></tbody></table>`,
			want: `<table><thead><tr><th>Header</th></tr></thead><tbody><tr><td>Data</td></tr></tbody></table>`,
		},
		{
			name: "preserves list structure",
			html: `<ul><li>Item 1</li><li>Item 2</li></ul>`,
			want: `<ul><li>Item 1</li><li>Item 2</li></ul>`,
		},
		{
			name: "preserves code blocks",
			html: `<pre><code>func main() {}</code></pre>`,
			want: `<pre><code>func main() {}</code></pre>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.SanitizeHTML(tt.html)
			if got != tt.want {
				t.Errorf("SanitizeHTML() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewlineTruncation(t *testing.T) {
	// Content with only newline boundaries (no paragraph or sentence boundaries)
	content := strings.Repeat("word", 20) + "\n" + strings.Repeat("more", 80)
	maxLen := 50

	result, truncated := Truncate(content, maxLen)
	if !truncated {
		t.Error("expected truncation")
	}
	if !strings.HasSuffix(result, "[content truncated]") {
		t.Errorf("expected truncation marker, got %q", result)
	}
}

package scraper

import (
	"strings"
	"testing"
)

// TestFormatTweetResult_Article covers the X Article (long-form) case where the
// top-level `text` is empty and the real content lives in an `article` object.
// Regression: the body was silently dropped, producing a near-empty result.
func TestFormatTweetResult_Article(t *testing.T) {
	t.Parallel()

	tweet := map[string]any{
		"text": "",
		"raw_text": map[string]any{
			"text": "https://t.co/ZVFA6duEo5",
		},
		"author": map[string]any{
			"screen_name": "ashwingop",
			"name":        "Ashwin Gopinath",
		},
		"likes":      float64(86),
		"retweets":   float64(7),
		"replies":    float64(12),
		"quotes":     float64(3),
		"views":      float64(9538),
		"created_at": "Thu Jun 11 14:35:34 +0000 2026",
		"article": map[string]any{
			"title":        "We Gave GPT-5.5 a Memory.",
			"preview_text": "88.31% on Terminal-Bench 2.1 at a quarter of the cost.",
			"content": map[string]any{
				"blocks": []any{
					map[string]any{"type": "unstyled", "text": "88.31% on Terminal-Bench 2.1 at a quarter of the cost."},
					map[string]any{"type": "header-two", "text": "The story"},
					map[string]any{"type": "unstyled", "text": "We took GPT-5.5 and gave it our memory system."},
					map[string]any{"type": "unordered-list-item", "text": "It scored 88.31%."},
					map[string]any{"type": "atomic", "text": ""}, // media block — skipped
				},
			},
		},
	}

	res := formatTweetResult("https://x.com/ashwingop/status/2065080505113125105", tweet, 10000)
	if res == nil {
		t.Fatal("expected a result, got nil")
	}
	if !strings.Contains(res.Content, "We Gave GPT-5.5 a Memory.") {
		t.Errorf("article title missing from content: %q", res.Content)
	}
	// Full body reconstructed from content.blocks[], not just the preview.
	if !strings.Contains(res.Content, "We took GPT-5.5 and gave it our memory system.") {
		t.Errorf("article body block missing from content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "## The story") {
		t.Errorf("header block shaping missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, "- It scored 88.31%.") {
		t.Errorf("list-item block shaping missing: %q", res.Content)
	}
}

// TestFormatTweetResult_ArticlePreviewFallback covers an article with no content
// blocks — the short preview_text is used so the body is not blank.
func TestFormatTweetResult_ArticlePreviewFallback(t *testing.T) {
	t.Parallel()

	tweet := map[string]any{
		"text": "",
		"author": map[string]any{
			"screen_name": "ashwingop",
			"name":        "Ashwin Gopinath",
		},
		"article": map[string]any{
			"title":        "Some Article",
			"preview_text": "A short preview only.",
		},
	}

	res := formatTweetResult("https://x.com/ashwingop/status/1", tweet, 10000)
	if res == nil {
		t.Fatal("expected a result, got nil")
	}
	if !strings.Contains(res.Content, "A short preview only.") {
		t.Errorf("preview_text fallback missing: %q", res.Content)
	}
}

// TestFormatTweetResult_RawTextFallback covers a link-only/media tweet with an
// empty `text` and no article — raw_text is used so the result is never blank.
func TestFormatTweetResult_RawTextFallback(t *testing.T) {
	t.Parallel()

	tweet := map[string]any{
		"text": "",
		"raw_text": map[string]any{
			"text": "Check this out https://t.co/abc",
		},
		"author": map[string]any{
			"screen_name": "someone",
			"name":        "Some One",
		},
	}

	res := formatTweetResult("https://x.com/someone/status/123", tweet, 10000)
	if res == nil {
		t.Fatal("expected a result, got nil")
	}
	if !strings.Contains(res.Content, "Check this out https://t.co/abc") {
		t.Errorf("raw_text fallback missing from content: %q", res.Content)
	}
}

// TestFormatTweetResult_PlainText confirms a normal tweet body is unchanged.
func TestFormatTweetResult_PlainText(t *testing.T) {
	t.Parallel()

	tweet := map[string]any{
		"text": "just a normal tweet",
		"author": map[string]any{
			"screen_name": "someone",
			"name":        "Some One",
		},
	}

	res := formatTweetResult("https://x.com/someone/status/123", tweet, 10000)
	if res == nil {
		t.Fatal("expected a result, got nil")
	}
	if !strings.Contains(res.Content, "just a normal tweet") {
		t.Errorf("plain tweet text missing from content: %q", res.Content)
	}
}

// TestFormatTweetResult_EngagementSignals (#280) confirms replies and quotes
// are read from the FXTwitter response and included in the engagement line
// alongside the existing likes/retweets/views counts.
func TestFormatTweetResult_EngagementSignals(t *testing.T) {
	t.Parallel()

	tweet := map[string]any{
		"text": "engagement test tweet",
		"author": map[string]any{
			"screen_name": "someone",
			"name":        "Some One",
		},
		"likes":      float64(100),
		"retweets":   float64(50),
		"replies":    float64(123),
		"quotes":     float64(45),
		"views":      float64(999),
		"created_at": "Thu Jun 11 14:35:34 +0000 2026",
	}

	res := formatTweetResult("https://x.com/user/status/1", tweet, 10000)
	if res == nil {
		t.Fatal("expected a result, got nil")
	}
	if !strings.Contains(res.Content, "100 likes") {
		t.Errorf("likes missing from content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "50 retweets") {
		t.Errorf("retweets missing from content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "123 replies") {
		t.Errorf("replies missing from content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "45 quotes") {
		t.Errorf("quotes missing from content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "999 views") {
		t.Errorf("views missing from content: %q", res.Content)
	}
}

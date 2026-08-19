package tools

import (
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// TestSanitizeClaimFetchError_MasksSecrets: a ScrapeError.Message frequently
// embeds the fetchURL verbatim (#631's composite tiered-fallback error), so a
// URL carrying an access token in its query string must never reach the
// claimFetchError field unmasked — matching the masking already applied to
// every other scrape-error-to-LLM-facing-field path (errors.go).
func TestSanitizeClaimFetchError_MasksSecrets(t *testing.T) {
	err := &scraper.ScrapeError{
		Kind:    scraper.ErrNetwork,
		Message: "no content extracted from https://example.com/doc?access_token=super-secret-value (network error)",
	}
	got := sanitizeClaimFetchError(err)
	if strings.Contains(got, "super-secret-value") {
		t.Errorf("sanitizeClaimFetchError leaked an unmasked secret: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected the masked placeholder in output, got %q", got)
	}
}

// TestClaimCoverageFromContent_CJKNotMisflaggedAsSparse is a regression test:
// claimCoverageFromContent used strings.Fields to count words, which collapses
// a complete CJK article (no ASCII whitespace) to ~1 "word" and fires a false
// SparsityNote even though the source was fully and correctly fetched.
func TestClaimCoverageFromContent_CJKNotMisflaggedAsSparse(t *testing.T) {
	article := strings.Repeat("这是一段完整的中文新闻内容用于测试提取质量与字数统计逻辑是否正确处理非拉丁语言的文本", 4)
	out := claimCoverageFromContent(article, "https://example.com/zh", "some claim")
	if out.ContentWords < sparseWordThreshold {
		t.Errorf("expected CJK content's script-aware word count to clear sparseWordThreshold, got ContentWords=%d", out.ContentWords)
	}
	if out.SparsityNote != "" {
		t.Errorf("expected no SparsityNote for complete CJK content, got %q", out.SparsityNote)
	}
}

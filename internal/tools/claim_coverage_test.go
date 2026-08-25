package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// buildEmptyDOCX returns a structurally valid .docx (a zip carrying an empty
// word/document.xml body) — documents.Parse succeeds on it with text=="",
// err==nil, which is the one path that gets a nil-error, empty-Content
// ScrapeResult out of the real scraper.Pipeline (an HTML/markdown/stealth
// fetch of an empty page instead surfaces a tier-level "content_empty"
// ScrapeError, never a nil-error empty success — see claim_coverage.go's two
// distinct claimSourceUnavailable branches).
func buildEmptyDOCX() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("word/document.xml")
	_, _ = f.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body></w:body>
</w:document>`))
	_ = w.Close()
	return buf.Bytes()
}

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

// TestClaimCoverageFor_EmptyContentSetsSourceURL is the #681 regression test:
// a fetch that succeeds but returns no usable content (documents.Parse
// succeeding on a structurally valid but textless .docx, exactly like a
// bot-wall/challenge page served as a plain 200 with no prose) must still
// record SourceURL — a fetch was actually attempted at this URL,
// distinguishable from fetchURL=="" (never attempted, claimCoverageFor's
// other empty-Support branch). FetchError stays empty: this is empty
// *content*, not a scrape *error*.
func TestClaimCoverageFor_EmptyContentSetsSourceURL(t *testing.T) {
	docx := buildEmptyDOCX()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		_, _ = w.Write(docx)
	}))
	defer server.Close()
	fetchURL := server.URL + "/empty.docx"

	deps := verifyClaimDeps(t)
	cc := claimCoverageFor(context.Background(), deps, fetchURL, "vaccine efficacy reduced infection")

	if cc.Support != claimSourceUnavailable {
		t.Fatalf("Support = %v, want %v", cc.Support, claimSourceUnavailable)
	}
	if cc.SourceURL != fetchURL {
		t.Errorf("SourceURL = %q, want %q (a fetch was attempted, so this must be attributable)", cc.SourceURL, fetchURL)
	}
	if cc.FetchError != "" {
		t.Errorf("FetchError = %q, want empty (empty content, not a scrape error)", cc.FetchError)
	}
}

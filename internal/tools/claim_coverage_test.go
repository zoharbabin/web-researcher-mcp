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

// TestClaimCoverageFromContent_FalseClaimNotAddressed is the #675 regression:
// a claim asserting CRISPR-Cas9 CAR-T therapy cures Alzheimer's disease,
// checked against a source genuinely about CRISPR-Cas9 CAR-T therapy for
// lymphoma. The claim's generic/topical terms (study, demonstrates, crispr,
// cas9, car, cures) alone clear the 0.6 claimAddressedThreshold ratio — 6 of
// 8 significant terms match — but the source never mentions "Alzheimer's",
// the one distinguishing term that would make the claim true. Before #675
// this returned claimAddressed purely on the ratio; it must now be capped at
// claimPartiallyAddressed (or lower).
func TestClaimCoverageFromContent_FalseClaimNotAddressed(t *testing.T) {
	body := "This study demonstrates that a CRISPR-Cas9 engineered CAR-T cell therapy cures B-cell lymphoma in a clinical trial. " +
		"The therapy uses CRISPR-Cas9 gene editing to modify CAR-T cells before they are infused into patients with lymphoma. " +
		"Researchers demonstrated durable remission in most patients treated with this CAR-T approach."
	claim := "This study demonstrates CRISPR-Cas9 CAR-T cures Alzheimer's disease"
	out := claimCoverageFromContent(body, "https://example.com/paper", claim)
	if out.Support == claimAddressed {
		t.Errorf("false claim whose distinguishing entity ('Alzheimer's') never matched must not be claimAddressed, got %q", out.Support)
	}
}

// TestClaimCoverageFromContent_TrueClaimStillAddressed is the no-regression
// companion (#675): a true claim's distinguishing entity ("Hodgkin") DOES
// appear in the source, so the new gate must not block it — it must still
// reach claimAddressed exactly as before this fix.
func TestClaimCoverageFromContent_TrueClaimStillAddressed(t *testing.T) {
	body := "This study demonstrates that a CRISPR-Cas9 engineered CAR-T cell therapy cures Hodgkin lymphoma in a clinical trial. " +
		"The therapy uses CRISPR-Cas9 gene editing to modify CAR-T cells before they are infused into patients with Hodgkin lymphoma. " +
		"Researchers demonstrated durable remission in most patients treated with this CAR-T approach."
	claim := "This study demonstrates CRISPR-Cas9 CAR-T cures Hodgkin lymphoma"
	out := claimCoverageFromContent(body, "https://example.com/paper", claim)
	if out.Support != claimAddressed {
		t.Errorf("true claim whose distinguishing entity ('Hodgkin') matched should still be claimAddressed, got %q", out.Support)
	}
}

// TestClaimCoverageFromContent_DistinguishingTermMustBeInPeakWindow is an
// adversarial regression: on a long document (>20 sentences, past the
// short-doc fallback), the false claim's distinguishing entity
// ("Alzheimer's") appears only in a sentence far from the topical passage
// that actually earns the coverage ratio. Before the fix,
// ClaimHasMatchedDistinguishingTerm scanned the WHOLE document, so a stray,
// topically-unrelated mention of "Alzheimer's" anywhere on the page
// satisfied the gate even though no local passage discusses it — exactly
// the #177/#523 dilution problem ClaimTermCoverageWindowed exists to guard
// against, just reintroduced through the distinguishing-term gate instead of
// the ratio itself.
func TestClaimCoverageFromContent_DistinguishingTermMustBeInPeakWindow(t *testing.T) {
	var sentences []string
	for i := 0; i < 10; i++ {
		sentences = append(sentences, "This is unrelated filler sentence number "+strings.Repeat("x", i+1)+" for the test.")
	}
	sentences = append(sentences,
		"This study demonstrates that a CRISPR-Cas9 engineered CAR-T cell therapy cures B-cell lymphoma in a clinical trial.",
		"The therapy uses CRISPR-Cas9 gene editing to modify CAR-T cells before they are infused into patients with lymphoma.",
		"Researchers demonstrated durable remission in most patients treated with this CAR-T approach.",
	)
	for i := 0; i < 9; i++ {
		sentences = append(sentences, "This is more unrelated filler sentence number "+strings.Repeat("y", i+1)+" for the test.")
	}
	sentences = append(sentences, "The hospital cafeteria menu referenced Alzheimer's awareness week today.")
	body := strings.Join(sentences, " ")

	claim := "This study demonstrates CRISPR-Cas9 CAR-T cures Alzheimer's disease"
	out := claimCoverageFromContent(body, "https://example.com/paper", claim)
	if out.Support == claimAddressed {
		t.Errorf("distinguishing term matched only far outside the peak coverage window must not satisfy the gate, got %q", out.Support)
	}
}

// TestClaimCoverageFromContent_GenericClaimUnaffected confirms a claim made
// entirely of generic vocabulary (no capitalized/rare distinguishing term at
// all) still reaches claimAddressed on strong overlap — the new gate is
// additive, not a general tightening that would produce false negatives on
// fully-generic claims (#675).
func TestClaimCoverageFromContent_GenericClaimUnaffected(t *testing.T) {
	body := strings.Repeat("The study found a significant increase in patient survival rates after treatment. ", 3)
	claim := "the study found a significant increase in patient survival rates"
	out := claimCoverageFromContent(body, "https://example.com/paper", claim)
	if out.Support != claimAddressed {
		t.Errorf("fully generic claim with strong overlap should still be claimAddressed, got %q", out.Support)
	}
}

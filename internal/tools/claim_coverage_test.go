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

// carTLymphomaAbstract is the real abstract of the live #675 repro source
// (nature.com/articles/s41586-022-05140-y, fetched 2026-08-24) — a CAR-T/CRISPR-Cas9
// lymphoma paper that never mentions Alzheimer's.
const carTLymphomaAbstract = `Recently, chimeric antigen receptor (CAR)-T cell therapy has shown great ` +
	`promise in treating haematological malignancies. However, CAR-T cell therapy currently has several ` +
	`limitations. Here we successfully developed a two-in-one approach to generate non-viral, gene-specific ` +
	`targeted CAR-T cells through CRISPR-Cas9. Using the optimized protocol, we demonstrated feasibility in a ` +
	`preclinical study by inserting an anti-CD19 CAR cassette into the AAVS1 safe-harbour locus. Furthermore, ` +
	`an innovative type of anti-CD19 CAR-T cell with PD1 integration was developed and showed superior ability ` +
	`to eradicate tumour cells in xenograft models. In adoptive therapy for relapsed or refractory aggressive ` +
	`B cell non-Hodgkin lymphoma, we observed a high rate of 87.5 percent complete remission and durable ` +
	`responses without serious adverse events in eight patients. Notably, these enhanced CAR-T cells were ` +
	`effective even at a low infusion dose and with a low percentage of CAR-positive cells. Single-cell ` +
	`analysis showed that the electroporation method resulted in a high percentage of memory T cells in ` +
	`infusion products, and PD1 interference enhanced anti-tumour immune functions, further validating the ` +
	`advantages of non-viral, PD1-integrated CAR-T cells. Collectively, our results demonstrate the high ` +
	`safety and efficacy of non-viral, gene-specific integrated CAR-T cells, thus providing an innovative ` +
	`technology for CAR-T cell therapy.`

// TestClaimCoverageFromContent_DistinguishingEntityGate is a regression test for
// #675: the real CAR-T/CRISPR-Cas9 lymphoma paper above genuinely mentions CRISPR,
// Cas9, and CAR — enough generic/topic overlap to have cleared claimAddressedThreshold
// under the old pure-ratio gate — but never mentions the fabricated claim's one
// actually-distinguishing (and false) entity, Alzheimer's. That must block
// claimAddressed regardless of how much other vocabulary matched.
func TestClaimCoverageFromContent_DistinguishingEntityGate(t *testing.T) {
	claim := "This study demonstrates CRISPR-Cas9 CAR-T cures Alzheimer's disease"
	out := claimCoverageFromContent(carTLymphomaAbstract, "https://example.com/paper", claim)
	if out.Support == claimAddressed {
		t.Errorf("false claim with an unmatched distinguishing entity (Alzheimer's) must not reach claimAddressed, got %q", out.Support)
	}
}

// TestClaimCoverageFromContent_DistinguishingEntityGateAllowsTrueClaim guards the
// no-regression requirement from #675: a claim whose distinguishing entities all
// genuinely appear in the source must still reach claimAddressed.
func TestClaimCoverageFromContent_DistinguishingEntityGateAllowsTrueClaim(t *testing.T) {
	claim := "CRISPR-Cas9 CAR-T cells achieved 87.5 percent complete remission in B cell lymphoma patients"
	out := claimCoverageFromContent(carTLymphomaAbstract, "https://example.com/paper", claim)
	if out.Support != claimAddressed {
		t.Errorf("true claim whose distinguishing entities all match must reach claimAddressed, got %q", out.Support)
	}
}

// TestClaimCoverageFromContent_DistinguishingEntityGateNoDistinguishingTerms
// guards #675's fallback: a claim made entirely of generic/lowercase vocabulary
// has no distinguishing term to gate on, so ratio-only behavior must be unchanged.
func TestClaimCoverageFromContent_DistinguishingEntityGateNoDistinguishingTerms(t *testing.T) {
	claim := "cell therapy cures cancer with high safety and efficacy"
	out := claimCoverageFromContent(carTLymphomaAbstract, "https://example.com/paper", claim)
	if out.Support != claimAddressed {
		t.Errorf("claim with no distinguishing term should fall back to ratio-only behavior, got %q", out.Support)
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

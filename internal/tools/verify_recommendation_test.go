package tools

import (
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

func TestVerifyRecommendationConflictOfInterest(t *testing.T) {
	// Test: Shopify employee recommending Shopify should detect conflict
	coi := content.DetectConflictOfInterest(
		"Jane is a senior developer at Shopify with 5 years experience",
		"Shopify — Top-rated e-commerce platform with excellent API support",
	)

	if coi == nil || !coi.Detected {
		t.Fatalf("Expected conflict of interest to be detected")
	}
	if coi.ConflictType != "employment" {
		t.Fatalf("Expected employment conflict, got %s", coi.ConflictType)
	}
	if coi.Confidence != "high" {
		t.Fatalf("Expected high confidence, got %s", coi.Confidence)
	}
	t.Logf("✓ Conflict detected: %s (confidence: %s)", coi.Evidence, coi.Confidence)
}

func TestVerifyRecommendationNoConflict(t *testing.T) {
	// Test: WooCommerce recommendation from Shopify employee should NOT detect conflict
	// (no mention of WooCommerce in the bio)
	coi := content.DetectConflictOfInterest(
		"Jane is a senior developer at Shopify",
		"WooCommerce — Strong open-source alternative",
	)

	if coi != nil {
		t.Fatalf("Expected no conflict, but got: %+v", coi)
	}
	t.Logf("✓ No conflict detected for WooCommerce recommendation")
}

func TestVerifyRecommendationSelfPromotion(t *testing.T) {
	// Test: Shopify blog ranking itself #1
	signal := content.DetectSelfPromotion("shopify.com", `
1. Shopify — Best overall e-commerce platform
2. WooCommerce — Good open-source option
3. BigCommerce — Another platform to consider
`)

	if signal == nil || !signal.Detected {
		t.Fatalf("Expected self-promotion to be detected")
	}
	if signal.RankPosition != 1 {
		t.Fatalf("Expected rank position 1, got %d", signal.RankPosition)
	}
	t.Logf("✓ Self-promotion detected at position %d (confidence: %s)", signal.RankPosition, signal.Confidence)
}

func TestVerifyRecommendationSelfPromotionMarkdownHeadings(t *testing.T) {
	// Real listicles render each entry as a markdown heading ("### 1. Shopify"),
	// not a bare "1." line — the shape scraped from shopify.com/blog/best-ecommerce-platforms.
	signal := content.DetectSelfPromotion("shopify.com", `
## The 11 best ecommerce platforms

### 1. Shopify

Shopify is the world's leading ecommerce platform.

### 2. Wix

Wix is a versatile drag-and-drop website builder.
`)

	if signal == nil || !signal.Detected {
		t.Fatalf("Expected self-promotion to be detected for heading-prefixed list")
	}
	if signal.RankPosition != 1 {
		t.Fatalf("Expected rank position 1, got %d", signal.RankPosition)
	}
	t.Logf("✓ Self-promotion detected in markdown-heading list at position %d", signal.RankPosition)
}

func TestVerifyRecommendationNoSelfPromotion(t *testing.T) {
	// Test: No self-promotion when brand is not #1
	signal := content.DetectSelfPromotion("shopify.com", `
1. WooCommerce — Best overall platform
2. Shopify — Close second
3. BigCommerce — Third
`)

	if signal != nil {
		t.Fatalf("Expected no self-promotion, but got: %+v", signal)
	}
	t.Logf("✓ No self-promotion detected when brand is not #1")
}

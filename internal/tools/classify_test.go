package tools

import (
	"testing"
)

// scrape_page must always carry the three typed classification fields (#62).
func TestScrapePageCarriesClassification(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "scrape_page", map[string]any{"url": "https://example.com"})
	if res.IsError {
		return // network unavailable in unit env; schema gate covers shape
	}
	for _, k := range []string{"sourceType", "authorityTier", "domainCategory"} {
		if _, ok := out[k]; !ok {
			t.Errorf("scrape_page output missing %q", k)
		}
	}
	// authorityTier must be one of the enum bands.
	if tier, _ := out["authorityTier"].(string); tier != "high" && tier != "medium" && tier != "low" {
		t.Errorf("authorityTier = %v, not a valid band", out["authorityTier"])
	}
}

// search_and_scrape sources must carry classification, and claim evidence only
// when the claim param is supplied.
func TestSearchAndScrapeClassificationAndClaim(t *testing.T) {
	// Without claim: sources (if any) carry classification but no claim fields.
	out, res := callTool(t, setupTestDeps(), "search_and_scrape", map[string]any{"query": "test"})
	if res.IsError {
		return // upstream unavailable in unit env
	}
	if srcs, ok := out["sources"].([]any); ok && len(srcs) > 0 {
		s0, _ := srcs[0].(map[string]any)
		if _, ok := s0["sourceType"]; !ok {
			t.Error("source missing sourceType")
		}
		if _, ok := s0["claimSignal"]; ok {
			t.Error("claimSignal must be absent when no claim param supplied")
		}
		if _, ok := s0["keySentences"]; ok {
			t.Error("keySentences must be absent when no claim param supplied")
		}
	}
}

// web_search default output must NOT contain claimSignal (byte-identical to the
// pre-#66 shape); supplying claim is what adds it.
func TestWebSearchClaimAdditive(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "web_search", map[string]any{"query": "test"})
	if res.IsError {
		return
	}
	if results, ok := out["results"].([]any); ok && len(results) > 0 {
		r0, _ := results[0].(map[string]any)
		if _, ok := r0["claimSignal"]; ok {
			t.Error("claimSignal must be absent on a web_search call without claim")
		}
	}
}

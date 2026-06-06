package content

import "testing"

func TestAnalyzeCoverageEmpty(t *testing.T) {
	cov := AnalyzeCoverage(nil)
	if cov.SourceCount != 0 {
		t.Errorf("sourceCount = %d", cov.SourceCount)
	}
	if len(cov.Gaps) != 1 {
		t.Fatalf("expected one no-sources gap, got %v", cov.Gaps)
	}
}

func TestAnalyzeCoverageDomainConcentration(t *testing.T) {
	src := []CoverageInput{
		{URL: "https://arxiv.org/a", Type: "academic"},
		{URL: "https://arxiv.org/b", Type: "academic"},
		{URL: "https://arxiv.org/c", Type: "academic"},
		{URL: "https://nature.com/x", Type: "academic"},
	}
	cov := AnalyzeCoverage(src)
	if cov.UniqueDomains != 2 {
		t.Errorf("uniqueDomains = %d, want 2", cov.UniqueDomains)
	}
	if cov.DominantDomain != "arxiv.org" {
		t.Errorf("dominantDomain = %q, want arxiv.org", cov.DominantDomain)
	}
	foundConc := false
	for _, g := range cov.Gaps {
		if contains(g, "concentrated on arxiv.org") {
			foundConc = true
		}
	}
	if !foundConc {
		t.Errorf("expected concentration gap, got %v", cov.Gaps)
	}
}

func TestAnalyzeCoverageSingleType(t *testing.T) {
	src := []CoverageInput{
		{URL: "https://a.com/1", Type: "news"},
		{URL: "https://b.com/2", Type: "news"},
		{URL: "https://c.com/3", Type: "news"},
	}
	cov := AnalyzeCoverage(src)
	if cov.DominantDomain != "" {
		t.Errorf("no single domain dominates; got %q", cov.DominantDomain)
	}
	foundType := false
	for _, g := range cov.Gaps {
		if contains(g, `type "news"`) {
			foundType = true
		}
	}
	if !foundType {
		t.Errorf("expected single-type gap, got %v", cov.Gaps)
	}
}

func TestAnalyzeCoverageDeterministic(t *testing.T) {
	src := []CoverageInput{
		{URL: "https://a.com/1", Type: "news"},
		{URL: "https://b.com/2", Type: "academic"},
		{URL: "https://c.com/3", Type: "scraped"},
		{URL: "https://d.com/4", Type: "news"},
	}
	a := AnalyzeCoverage(src)
	b := AnalyzeCoverage(src)
	if a.DomainSpread != b.DomainSpread || len(a.Gaps) != len(b.Gaps) {
		t.Error("AnalyzeCoverage must be deterministic")
	}
	if a.DomainSpread != 1.0 {
		t.Errorf("4 unique domains / 4 sources should be spread 1.0, got %v", a.DomainSpread)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://www.Example.com/path": "example.com",
		"http://arxiv.org":             "arxiv.org",
		"not a url":                    "",
		"https://sub.host.com:8443/x":  "sub.host.com",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

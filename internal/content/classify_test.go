package content

import "testing"

func TestAuthorityTier(t *testing.T) {
	cases := map[float64]string{0.95: "high", 0.8: "high", 0.7: "medium", 0.5: "medium", 0.49: "low", 0.0: "low"}
	for in, want := range cases {
		if got := authorityTier(in); got != want {
			t.Errorf("authorityTier(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifySourceFromCitationMeta(t *testing.T) {
	// Highwire citation_* meta is a strong peer-reviewed signal.
	c := ClassifySource("https://example.com/article", 0.9, StructuredSignals{HasCitationMeta: true}, "")
	if c.SourceType != SourceTypePeerReviewed {
		t.Errorf("sourceType = %q, want peer_reviewed", c.SourceType)
	}
	if c.AuthorityTier != "high" {
		t.Errorf("authorityTier = %q, want high", c.AuthorityTier)
	}
}

func TestClassifySourceFromSchemaType(t *testing.T) {
	cases := map[string]string{
		"NewsArticle":      SourceTypeNews,
		"ScholarlyArticle": SourceTypePeerReviewed,
		"BlogPosting":      SourceTypeBlog,
		"TechArticle":      SourceTypeOfficialDocs,
		"QAPage":           SourceTypeForum,
	}
	for schemaType, want := range cases {
		c := ClassifySource("https://example.com/x", 0.5, StructuredSignals{SchemaTypes: []string{schemaType}}, "")
		if c.SourceType != want {
			t.Errorf("schema %q → sourceType %q, want %q", schemaType, c.SourceType, want)
		}
	}
}

func TestClassifySourceHostHeuristic(t *testing.T) {
	cases := map[string]string{
		"https://arxiv.org/abs/1234":              SourceTypePeerReviewed,
		"https://www.nature.com/articles/x":       SourceTypePeerReviewed,
		"https://data.cdc.gov/report":             SourceTypeGovernment,
		"https://en.wikipedia.org/wiki/Go":        SourceTypeWiki,
		"https://stackoverflow.com/questions/1":   SourceTypeForum,
		"https://x.com/user/status/1":             SourceTypeSocial,
		"https://medium.com/@a/post":              SourceTypeBlog,
		"https://developer.mozilla.org/en-US/JS":  SourceTypeOfficialDocs,
		"https://some-random-unknown-site.io/abc": SourceTypeUnknown,
	}
	for url, want := range cases {
		c := ClassifySource(url, 0.5, StructuredSignals{}, "")
		if c.SourceType != want {
			t.Errorf("%s → sourceType %q, want %q", url, c.SourceType, want)
		}
	}
}

func TestClassifyStructuredBeatsHeuristic(t *testing.T) {
	// A blog host with a NewsArticle schema → structured wins (news).
	c := ClassifySource("https://medium.com/@a/post", 0.5, StructuredSignals{SchemaTypes: []string{"NewsArticle"}}, "")
	if c.SourceType != SourceTypeNews {
		t.Errorf("structured signal should win: got %q", c.SourceType)
	}
}

func TestDomainCategoryFromLens(t *testing.T) {
	cases := map[string]string{
		"academic": "academic", "legal": "legal", "medical": "medical",
		"clinical": "medical", "finance": "financial", "programming": "technical",
		"devops": "technical", "academic-extended": "academic",
	}
	for lens, want := range cases {
		c := ClassifySource("https://example.com/x", 0.5, StructuredSignals{}, lens)
		if c.DomainCategory != want {
			t.Errorf("lens %q → domainCategory %q, want %q", lens, c.DomainCategory, want)
		}
	}
}

func TestDomainCategoryLensBeatsHost(t *testing.T) {
	// legal lens on an academic host → lens wins.
	c := ClassifySource("https://arxiv.org/abs/1", 0.5, StructuredSignals{}, "legal")
	if c.DomainCategory != "legal" {
		t.Errorf("lens should win: got %q", c.DomainCategory)
	}
}

func TestDomainCategoryFallsBackToGeneral(t *testing.T) {
	c := ClassifySource("https://some-shop.example/widget", 0.5, StructuredSignals{}, "")
	if c.DomainCategory != "general" {
		t.Errorf("expected general, got %q", c.DomainCategory)
	}
}

func TestClassifyHost(t *testing.T) {
	cases := map[string]string{
		"https://www.Example.com:8080/x": "example.com",
		"http://Sub.Host.org/y":          "sub.host.org",
		"not a url":                      "",
	}
	for in, want := range cases {
		if got := classifyHost(in); got != want {
			t.Errorf("classifyHost(%q) = %q, want %q", in, got, want)
		}
	}
}

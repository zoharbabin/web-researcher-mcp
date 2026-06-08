package content

import "testing"

func TestLookupDomainReputation_EmbeddedAllowlist(t *testing.T) {
	// Known high-authority host from the embedded dataset.
	rep := LookupDomainReputation("sec.gov")
	if rep.Tier != ReputationHigh {
		t.Errorf("sec.gov tier = %q, want high", rep.Tier)
	}
	if rep.Basis == "" {
		t.Error("expected basis to be backfilled from dataset")
	}
}

func TestLookupDomainReputation_ParentMatch(t *testing.T) {
	// A subdomain inherits the registered parent.
	rep := LookupDomainReputation("data.sec.gov")
	if rep.Tier != ReputationHigh {
		t.Errorf("data.sec.gov tier = %q, want high (parent match)", rep.Tier)
	}
	// www. is stripped.
	if LookupDomainReputation("www.who.int").Tier != ReputationHigh {
		t.Error("www.who.int should match who.int")
	}
}

func TestLookupDomainReputation_UnknownAndEmpty(t *testing.T) {
	if LookupDomainReputation("some-random-blog.example").Tier != ReputationUnknown {
		t.Error("unlisted host should be unknown")
	}
	if LookupDomainReputation("").Tier != ReputationUnknown {
		t.Error("empty host should be unknown")
	}
}

func TestLoadReputationDataset_Override(t *testing.T) {
	// Operator override replaces the dataset.
	err := LoadReputationDataset([]byte(`{"version":"t","basis":"test","domains":{"my.example":{"tier":"high","note":"x"}}}`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if LookupDomainReputation("my.example").Tier != ReputationHigh {
		t.Error("override host not found")
	}
	// Malformed JSON returns an error and keeps the prior dataset.
	if err := LoadReputationDataset([]byte(`{bad`)); err == nil {
		t.Error("malformed dataset should error")
	}
	if LookupDomainReputation("my.example").Tier != ReputationHigh {
		t.Error("prior dataset should survive a failed override")
	}
}

func TestClassifySource_AttachesReputation(t *testing.T) {
	// Reload the embedded default so this test is order-independent.
	if err := LoadReputationDataset(defaultReputationJSON); err != nil {
		t.Fatalf("reload default: %v", err)
	}
	c := ClassifySource("https://www.sec.gov/cgi-bin/browse-edgar", 0.9, StructuredSignals{}, "")
	if c.DomainReputation == nil || c.DomainReputation.Tier != ReputationHigh {
		t.Fatalf("expected high reputation on sec.gov, got %+v", c.DomainReputation)
	}
	// Unlisted host → nil (no false confidence, clean output).
	c2 := ClassifySource("https://random-blog.example/post", 0.4, StructuredSignals{}, "")
	if c2.DomainReputation != nil {
		t.Errorf("unlisted host must have nil reputation, got %+v", c2.DomainReputation)
	}
}

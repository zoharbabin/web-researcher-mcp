package search

import "testing"

func TestProviderMeta_MatchesRegion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		regions []string
		query   string
		want    bool
	}{
		{"wildcard matches any", []string{"*"}, "EP", true},
		{"wildcard matches US", []string{"*"}, "US", true},
		{"US only matches US", []string{"US"}, "US", true},
		{"US only rejects EP", []string{"US"}, "EP", false},
		{"empty query matches all", []string{"US"}, "", true},
		{"all query matches all", []string{"US"}, "all", true},
		{"case insensitive match", []string{"US"}, "us", true},
		{"multi-region includes", []string{"US", "EP", "WO"}, "WO", true},
		{"multi-region excludes", []string{"US", "EP", "WO"}, "JP", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := ProviderMeta{Regions: tt.regions}
			got := meta.MatchesRegion(tt.query)
			if got != tt.want {
				t.Errorf("MatchesRegion(%q) with regions %v = %v, want %v",
					tt.query, tt.regions, got, tt.want)
			}
		})
	}
}

func TestProviderMeta_HasCapability(t *testing.T) {
	t.Parallel()

	meta := ProviderMeta{
		Capabilities: []string{"search", "biblio", "citations"},
	}

	if !meta.HasCapability("search") {
		t.Error("expected to have search capability")
	}
	if !meta.HasCapability("BIBLIO") {
		t.Error("expected case-insensitive match")
	}
	if meta.HasCapability("fulltext") {
		t.Error("expected NOT to have fulltext capability")
	}
}

func TestAvailablePatentProviders_EmptyConfig(t *testing.T) {
	t.Parallel()

	cfg := PatentProviderConfig{}
	providers := AvailablePatentProviders(cfg, Deps{
		HTTPClient: nil,
		Breaker:    nil,
	})

	if len(providers) != 0 {
		t.Errorf("expected 0 providers with empty config, got %d", len(providers))
	}
}

func TestNewPatentProviderByName(t *testing.T) {
	t.Parallel()

	cfg := PatentProviderConfig{
		USPTOAPIKey:       "test-key",
		EPOConsumerKey:    "epo-key",
		EPOConsumerSecret: "epo-secret",
		LensAPIToken:      "lens-token",
	}

	deps := Deps{HTTPClient: nil, Breaker: nil}

	tests := []struct {
		name     string
		provider string
		wantName string
	}{
		{"creates USPTO", "uspto", "uspto"},
		{"creates EPO", "epo", "epo"},
		{"creates Lens", "lens", "lens"},
		{"unknown returns nil", "unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPatentProviderByName(tt.provider, cfg, deps)
			if tt.wantName == "" {
				if p != nil {
					t.Error("expected nil for unknown provider")
				}
				return
			}
			if p == nil {
				t.Fatalf("expected non-nil provider for %s", tt.provider)
			}
			if p.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", p.Name(), tt.wantName)
			}
		})
	}
}

func TestNewPatentProviderByName_MissingCredentials(t *testing.T) {
	t.Parallel()

	cfg := PatentProviderConfig{}
	deps := Deps{HTTPClient: nil, Breaker: nil}

	for _, name := range []string{"uspto", "epo", "lens"} {
		p := NewPatentProviderByName(name, cfg, deps)
		if p != nil {
			t.Errorf("expected nil for %s without credentials", name)
		}
	}
}

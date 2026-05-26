package search

import (
	"context"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// ProviderMeta describes a domain provider's coverage and capabilities.
// Used internally for intelligent routing — not exposed to MCP clients.
type ProviderMeta struct {
	Regions      []string // ISO country codes (e.g. "US", "EP", "WO") or "*" for worldwide
	Capabilities []string // provider-specific tags: "search", "biblio", "fulltext", "citations", "family", "scholarly"
	RateClass    string   // "free", "metered", "unlimited"
	Description  string   // human-readable, used in error messages
}

// MatchesRegion returns true if this provider covers the given region.
// Empty region or "all" matches any provider. "*" in provider regions matches any query.
func (m ProviderMeta) MatchesRegion(region string) bool {
	if region == "" || strings.EqualFold(region, "all") {
		return true
	}
	for _, r := range m.Regions {
		if r == "*" || strings.EqualFold(r, region) {
			return true
		}
	}
	return false
}

// HasCapability returns true if the provider supports the given capability tag.
func (m ProviderMeta) HasCapability(cap string) bool {
	for _, c := range m.Capabilities {
		if strings.EqualFold(c, cap) {
			return true
		}
	}
	return false
}

// PatentProvider is a specialized provider for patent search.
// Unlike Provider, it does not support Web/Images/News — only structured patent queries.
type PatentProvider interface {
	PatentSearcher
	Name() string
	Metadata() ProviderMeta
}

// SupportedPatentProviders lists all patent-specific provider names.
var SupportedPatentProviders = []string{"searchapi", "epo", "lens", "uspto"}

// NewPatentProviderByName creates a patent provider by name if credentials are configured.
func NewPatentProviderByName(name string, cfg PatentProviderConfig, deps Deps) PatentProvider {
	switch name {
	case "uspto":
		if cfg.USPTOAPIKey != "" {
			return NewUSPTOProvider(cfg.USPTOAPIKey, deps)
		}
	case "epo":
		if cfg.EPOConsumerKey != "" && cfg.EPOConsumerSecret != "" {
			return NewEPOProvider(cfg.EPOConsumerKey, cfg.EPOConsumerSecret, deps)
		}
	case "lens":
		if cfg.LensAPIToken != "" {
			return NewLensProvider(cfg.LensAPIToken, deps)
		}
	case "searchapi":
		if cfg.SearchAPIKey != "" {
			return &searchAPIPatentAdapter{provider: NewSearchAPIProvider(cfg.SearchAPIKey, deps)}
		}
	}
	return nil
}

// searchAPIPatentAdapter wraps SearchAPIProvider to satisfy the PatentProvider interface.
type searchAPIPatentAdapter struct {
	provider *SearchAPIProvider
}

func (a *searchAPIPatentAdapter) Name() string { return "searchapi" }

func (a *searchAPIPatentAdapter) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biblio"},
		RateClass:    "metered",
		Description:  "SearchAPI — Google Patents via SerpAPI (structured results)",
	}
}

func (a *searchAPIPatentAdapter) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	return a.provider.Patents(ctx, params)
}

// PatentProviderConfig holds credentials for patent-specific providers.
type PatentProviderConfig struct {
	USPTOAPIKey       string
	EPOConsumerKey    string
	EPOConsumerSecret string
	LensAPIToken      string
	SearchAPIKey      string
}

// AvailablePatentProviders returns all patent providers that can be constructed from config.
// Each provider gets its own circuit breaker for isolation — a failure in one provider
// does not block fallback to another.
func AvailablePatentProviders(cfg PatentProviderConfig, deps Deps) map[string]PatentProvider {
	providers := make(map[string]PatentProvider)
	for _, name := range []string{"searchapi", "epo", "lens", "uspto"} {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewPatentProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

package search

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// ───────────────────── Awesome lists (ecosyste.ms, #375) ────────────────────

// AwesomeListSearcher finds community-curated "awesome-*" lists for a topic.
// Awesome lists don't fit the filing/case/econ/trial shapes (no monetary
// value, no opinion, no time series, no registration), so they get their own
// capability pair — same structure, new domain.
type AwesomeListSearcher interface {
	AwesomeLists(ctx context.Context, params AwesomeListSearchParams) ([]AwesomeListResult, error)
}

// AwesomeListProvider is a named, described AwesomeListSearcher.
type AwesomeListProvider interface {
	AwesomeListSearcher
	Name() string
	Metadata() ProviderMeta
}

// AwesomeListSearchParams drives an awesome-list search. Topic is a GitHub
// topic slug (e.g. "osint", "go"); Query is a free-text fallback when Topic
// doesn't resolve to a known topic. MinStars/MinProjects filter the
// underlying repository's popularity and the list's curated-entry count.
type AwesomeListSearchParams struct {
	Topic       string // GitHub topic slug, e.g. "osint"
	Query       string // free-text fallback when Topic is empty or unresolved
	MinStars    int    // minimum GitHub stars on the list's repository
	MinProjects int    // minimum curated-entry count
	SortBy      string // "stars" (default), "projects", or "updated"
	NumResults  int
}

// AwesomeListResult is one curated awesome-list repository.
type AwesomeListResult struct {
	Name          string   `json:"name"`
	FullName      string   `json:"fullName,omitempty"`
	URL           string   `json:"url"`
	Description   string   `json:"description,omitempty"`
	ProjectsCount int      `json:"projectsCount,omitempty"`
	Stars         int      `json:"stars,omitempty"`
	Topics        []string `json:"topics,omitempty"`
	LastSyncedAt  string   `json:"lastSyncedAt,omitempty"`
	Archived      bool     `json:"archived,omitempty"`
	Source        string   `json:"source"`
}

// SupportedAwesomeListProviders is the source of truth for awesome-list
// provider names.
var SupportedAwesomeListProviders = []string{"ecosystems"}

// AwesomeListProviderConfig holds awesome-list provider auth.
type AwesomeListProviderConfig struct {
	// EcosystemsAPIKey is optional; sent for forward compatibility, but per
	// ecosyste.ms's published pricing it's a no-op on the Free plan (key auth
	// only activates on paid Develop/Scale plans).
	EcosystemsAPIKey string
	// EcosystemsEmail is optional; opts into ecosyste.ms's "polite pool" via
	// mailto=, a verified rate-limit increase on the Free plan.
	EcosystemsEmail string
}

// NewAwesomeListProviderByName constructs an awesome-list provider.
// ecosyste.ms is keyless, so it always constructs regardless of cfg.
func NewAwesomeListProviderByName(name string, cfg AwesomeListProviderConfig, deps Deps) AwesomeListProvider {
	switch name {
	case "ecosystems":
		return NewEcosystemsAwesomeProvider(cfg.EcosystemsAPIKey, cfg.EcosystemsEmail, deps)
	}
	return nil
}

// AvailableAwesomeListProviders builds the awesome-list providers, each with
// its own circuit breaker (parity with the other structured-domain
// constructors).
func AvailableAwesomeListProviders(cfg AwesomeListProviderConfig, deps Deps) map[string]AwesomeListProvider {
	providers := make(map[string]AwesomeListProvider)
	for _, name := range SupportedAwesomeListProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewAwesomeListProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

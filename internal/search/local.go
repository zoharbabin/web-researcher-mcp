package search

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// This file defines the local-search capability added in #259: place search
// backed by Brave's three-call local pipeline (web/search?result_filter=locations
// → local/pois → local/descriptions). It follows the same shape as the
// structured-domain capability pairs: a LocalSearcher (the method) + a
// LocalProvider (Searcher + Name + Metadata) — with SupportedLocalProviders,
// NewLocalProviderByName, and AvailableLocalProviders. The tool layer depends
// only on these interfaces, so the provider stays swappable.

// LocalSearcher finds physical places (restaurants, shops, offices, …).
type LocalSearcher interface {
	Local(ctx context.Context, params LocalSearchParams) ([]LocalResult, error)
}

// LocalProvider is a named, described LocalSearcher.
type LocalProvider interface {
	LocalSearcher
	Name() string
	Metadata() ProviderMeta
}

// LocalSearchParams drives a place search. Query should carry clear local
// intent (e.g. "best coffee shops near downtown Seattle"). Near is an optional
// free-text location bias; Country is ISO 3166-1 alpha-2; Units is "metric" or
// "imperial". NumResults is clamped to 1-20, defaulting to 5.
type LocalSearchParams struct {
	Query      string
	Near       string // optional free-text location bias (city, neighborhood, region)
	Country    string // ISO 3166-1 alpha-2
	Units      string // "metric" or "imperial"
	NumResults int    // 1-20, default 5
}

// LocalResult is one physical place from the POI index. Coordinates are
// WGS-84 lat/lon. IDs are ephemeral — never persisted beyond the request
// lifecycle (Brave Local Search API contract).
type LocalResult struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Address     string   `json:"address,omitempty"`
	Lat         float64  `json:"lat,omitempty"`
	Lon         float64  `json:"lon,omitempty"`
	Phone       string   `json:"phone,omitempty"`
	Website     string   `json:"website,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Rating      float64  `json:"rating,omitempty"`
	RatingCount int      `json:"ratingCount,omitempty"`
	Hours       []string `json:"hours,omitempty"`
	Description string   `json:"description,omitempty"`
	Source      string   `json:"source"`
}

// SupportedLocalProviders is the source of truth for local-search provider names.
var SupportedLocalProviders = []string{"brave"}

// NewLocalProviderByName constructs a local provider, or nil when its required
// credential is absent (provider skipped — no dead config). Brave's local APIs
// require an API key, so the provider is only built when braveKey is non-empty.
func NewLocalProviderByName(name string, braveKey string, deps Deps) LocalProvider {
	switch name {
	case "brave":
		if braveKey != "" {
			return NewBraveProvider(braveKey, BraveConfig{}, deps)
		}
	}
	return nil
}

// AvailableLocalProviders builds the configured local providers, each with its
// own circuit breaker (parity with AvailablePatentProviders / AvailableTrialProviders).
func AvailableLocalProviders(braveKey string, deps Deps) map[string]LocalProvider {
	providers := make(map[string]LocalProvider)
	for _, name := range SupportedLocalProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewLocalProviderByName(name, braveKey, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

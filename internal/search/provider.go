package search

import (
	"context"
	"net/http"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

type WebSearchParams struct {
	Query        string
	NumResults   int
	TimeRange    string
	Safe         string
	Language     string
	Country      string
	Site         string
	ExactTerms   string
	ExcludeTerms string
}

type ImageSearchParams struct {
	Query         string
	NumResults    int
	Size          string
	Type          string
	ColorType     string
	DominantColor string
	FileType      string
	Safe          string
}

type NewsSearchParams struct {
	Query      string
	NumResults int
	Freshness  string
	SortBy     string
	Source     string
}

type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	DisplayLink string `json:"displayLink"`
}

type ImageResult struct {
	Title         string `json:"title"`
	Link          string `json:"link"`
	ThumbnailLink string `json:"thumbnailLink,omitempty"`
	DisplayLink   string `json:"displayLink"`
	ContextLink   string `json:"contextLink,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	FileSize      string `json:"fileSize,omitempty"`
}

type NewsResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Source      string `json:"source"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Snippet     string `json:"snippet"`
}

// Provider is the core web-search capability: Web, Images, and News.
//
// Capability sentinel: a provider that lacks a sub-capability (e.g. no image
// search) MUST return (nil, nil) from that method — never an error. Returning an
// error for a missing capability would trip the per-provider circuit breaker and
// break Router fallback for that operation. See ExaProvider.Images /
// TavilyProvider.Images for the canonical no-op implementation.
type Provider interface {
	Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error)
	Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error)
	News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error)
	Name() string
}

// PatentSearcher is an optional interface that providers can implement to
// support structured patent search (e.g. SerpAPI's Google Patents engine).
type PatentSearcher interface {
	Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error)
}

type PatentSearchParams struct {
	Query        string
	Assignee     string
	Inventor     string
	PatentOffice string
	YearFrom     int
	YearTo       int
	NumResults   int
}

type PatentResult struct {
	Title    string `json:"title"`
	Number   string `json:"number"`
	URL      string `json:"url"`
	Abstract string `json:"abstract"`
	Assignee string `json:"assignee"`
	Inventor string `json:"inventor,omitempty"`
	Filed    string `json:"filed"`
	Granted  string `json:"granted,omitempty"`
	PDF      string `json:"pdf,omitempty"`
	Status   string `json:"status,omitempty"`
}

type Deps struct {
	HTTPClient *http.Client
	Breaker    *circuit.Breaker
}

// SupportedProviders lists all provider names that can be configured.
var SupportedProviders = []string{"google", "brave", "serper", "searxng", "searchapi", "duckduckgo", "tavily", "exa", "hackernews"}

func NewProvider(cfg config.SearchConfig, deps Deps) Provider {
	switch cfg.Provider {
	case "brave":
		return NewBraveProvider(cfg.BraveAPIKey, deps)
	case "serper":
		return NewSerperProvider(cfg.SerperAPIKey, deps)
	case "searxng":
		return NewSearXNGProvider(cfg.SearXNGURL, cfg.SearXNGBasicAuth, cfg.SearXNGHeaders, deps)
	case "searchapi":
		return NewSearchAPIProvider(cfg.SearchAPIKey, deps)
	case "tavily":
		return NewTavilyProvider(cfg.TavilyAPIKey, deps)
	case "exa":
		return NewExaProvider(cfg.ExaAPIKey, deps)
	case "duckduckgo":
		return NewDuckDuckGoProvider(deps)
	case "hackernews":
		return NewHNProvider(deps)
	default:
		if cfg.GoogleAPIKey != "" && cfg.GoogleCX != "" {
			return NewGoogleProvider(cfg.GoogleAPIKey, cfg.GoogleCX, deps)
		}
		return NewDuckDuckGoProvider(deps)
	}
}

// NewProviderByName creates a single provider by name using the given config.
// Returns nil if the provider cannot be created (missing credentials).
func NewProviderByName(name string, cfg config.SearchConfig, deps Deps) Provider {
	switch name {
	case "google":
		if cfg.GoogleAPIKey != "" && cfg.GoogleCX != "" {
			return NewGoogleProvider(cfg.GoogleAPIKey, cfg.GoogleCX, deps)
		}
	case "brave":
		if cfg.BraveAPIKey != "" {
			return NewBraveProvider(cfg.BraveAPIKey, deps)
		}
	case "serper":
		if cfg.SerperAPIKey != "" {
			return NewSerperProvider(cfg.SerperAPIKey, deps)
		}
	case "searxng":
		if cfg.SearXNGURL != "" {
			return NewSearXNGProvider(cfg.SearXNGURL, cfg.SearXNGBasicAuth, cfg.SearXNGHeaders, deps)
		}
	case "searchapi":
		if cfg.SearchAPIKey != "" {
			return NewSearchAPIProvider(cfg.SearchAPIKey, deps)
		}
	case "tavily":
		if cfg.TavilyAPIKey != "" {
			return NewTavilyProvider(cfg.TavilyAPIKey, deps)
		}
	case "exa":
		if cfg.ExaAPIKey != "" {
			return NewExaProvider(cfg.ExaAPIKey, deps)
		}
	case "duckduckgo":
		return NewDuckDuckGoProvider(deps)
	case "hackernews":
		return NewHNProvider(deps)
	}
	return nil
}

// AvailableProviders returns all providers that can be constructed from the
// given config (i.e., have credentials configured). Each provider gets its own
// circuit breaker for isolation — failures in one provider don't affect others.
func AvailableProviders(cfg config.SearchConfig, deps Deps) map[string]Provider {
	providers := make(map[string]Provider)
	for _, name := range SupportedProviders {
		providerDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 3, ResetTimeout: 120}),
		}
		if p := NewProviderByName(name, cfg, providerDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

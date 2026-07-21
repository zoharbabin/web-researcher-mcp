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
	Sites        []string // ad hoc multi-domain scoping, OR-joined into site: operators by buildQuery (#374); mutually exclusive with Site at the tool boundary
	ExactTerms   string
	ExcludeTerms string
	Offset       int      // pagination offset (provider-specific, ignored when 0)
	ResultFilter string   // comma-separated types to return: web, news, images, videos, discussions, faq (Brave only)
	Goggles      []string // Brave Goggles re-ranking URLs (Brave only; up to 3; ignored by other providers)
}

type ImageSearchParams struct {
	Query         string
	NumResults    int
	Size          string // Google/SearchAPI only (Brave has no documented image size param)
	Type          string // Google/SearchAPI only (Brave has no documented image type param)
	ColorType     string // Google/SearchAPI only
	DominantColor string // Google/SearchAPI only
	FileType      string // Google/SearchAPI only
	Safe          string // SafeSearch level; Brave images accept only off|strict
	Country       string // ISO 3166-1 alpha-2; Brave search & Google cr/lr honor it
	Language      string // BCP 47 (search_lang on Brave, lr on Google)
}

type NewsSearchParams struct {
	Query      string
	NumResults int
	Freshness  string
	SortBy     string // Google only (no documented Brave news sort)
	Source     string // Google only (site: restriction; Brave has no news-source filter)
	Country    string // ISO 3166-1 alpha-2 (Brave news country)
	Language   string // BCP 47 (search_lang on Brave news)
	Safe       string // SafeSearch level; Brave news accepts off|moderate|strict
	Offset     int    // pagination offset 0–9 (Brave news); ignored when 0
}

type SearchResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Snippet       string   `json:"snippet"`
	DisplayLink   string   `json:"displayLink"`
	ExtraSnippets []string `json:"extraSnippets,omitempty"`
	// PublishedAt (#356) is an RFC3339 timestamp normalized by
	// normalizePublishedAt, populated only by providers whose web response
	// carries a date (Google, Tavily, Exa, SearXNG, HackerNews). Empty when
	// the provider has no date signal — never guessed from snippet/title text.
	PublishedAt string `json:"publishedAt,omitempty"`
	// Engagement (#281) carries provider-supplied engagement metrics. Nil
	// when the provider does not surface any — absence means "unavailable",
	// never "zero engagement".
	Engagement *EngagementSignals `json:"engagement,omitempty"`
}

// EngagementSignals carries optional provider-supplied engagement metrics.
// All fields are omitempty — callers must treat absence as "unavailable",
// not "zero engagement". Populated only by providers that natively surface
// these signals (HackerNews: Points/CommentCount; Exa: Score).
type EngagementSignals struct {
	Score        float64 `json:"score,omitempty"`        // relevance/quality score 0-1 (Exa)
	Points       int     `json:"points,omitempty"`       // upvote/karma points (HackerNews)
	CommentCount int     `json:"commentCount,omitempty"` // total comment count
	ReplyCount   int     `json:"replyCount,omitempty"`   // reply/thread depth (future providers)
	LikeCount    int     `json:"likeCount,omitempty"`    // likes/reactions (future providers)
	RepostCount  int     `json:"repostCount,omitempty"`  // reposts/shares (future providers)
	ViewCount    int     `json:"viewCount,omitempty"`    // view/impression count (future providers)
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
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Source        string   `json:"source"`
	PublishedAt   string   `json:"publishedAt,omitempty"`
	Snippet       string   `json:"snippet"`
	ExtraSnippets []string `json:"extraSnippets,omitempty"`
	// Engagement (#281) mirrors SearchResult.Engagement; nil when the
	// provider does not surface engagement metrics.
	Engagement *EngagementSignals `json:"engagement,omitempty"`
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
var SupportedProviders = []string{"google", "brave", "serper", "searxng", "searchapi", "duckduckgo", "tavily", "exa", "hackernews", "reddit", "github"}

func NewProvider(cfg config.SearchConfig, deps Deps) Provider {
	switch cfg.Provider {
	case "brave":
		return NewBraveProvider(cfg.BraveAPIKey, BraveConfig{ExtraSnippets: cfg.BraveExtraSnippets}, deps)
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
	case "reddit":
		return NewRedditProvider(deps)
	case "github":
		return NewGitHubProvider(cfg.GitHubToken, deps)
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
			return NewBraveProvider(cfg.BraveAPIKey, BraveConfig{ExtraSnippets: cfg.BraveExtraSnippets}, deps)
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
	case "reddit":
		return NewRedditProvider(deps)
	case "github":
		return NewGitHubProvider(cfg.GitHubToken, deps)
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

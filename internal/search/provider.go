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

type Provider interface {
	Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error)
	Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error)
	News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error)
	Name() string
}

type Deps struct {
	HTTPClient *http.Client
	Breaker    *circuit.Breaker
}

func NewProvider(cfg config.SearchConfig, deps Deps) Provider {
	switch cfg.Provider {
	case "brave":
		return NewBraveProvider(cfg.BraveAPIKey, deps)
	case "serper":
		return NewSerperProvider(cfg.SerperAPIKey, deps)
	case "searxng":
		return NewSearXNGProvider(cfg.SearXNGURL, deps)
	default:
		return NewGoogleProvider(cfg.GoogleAPIKey, cfg.GoogleCX, deps)
	}
}

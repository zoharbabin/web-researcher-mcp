package tools

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type Dependencies struct {
	Cache             cache.Cache
	Search            search.Provider
	SearchProviders   map[string]search.Provider
	PatentProviders   map[string]search.PatentProvider
	AcademicProviders map[string]search.AcademicProvider
	Scraper           *scraper.Pipeline
	Content           *content.Processor
	Sessions          *session.Manager
	Metrics           *metrics.Collector
	Auditor           audit.Auditor
	Logger            *slog.Logger
	Features          Features
}

// Features mirrors config.FeatureConfig for the tool layer (kept local so the
// tools package does not import config). All zero values are safe defaults:
// recommendations off, generative UI off — additive features that are
// byte-for-byte no-ops when disabled. main.go populates this from config.
type Features struct {
	SourceRecommendations bool
	GenerativeUI          bool
}

func RegisterAll(srv *mcp.Server, deps Dependencies) {
	registerWebSearch(srv, deps)
	registerScrapePage(srv, deps)
	registerSearchAndScrape(srv, deps)
	registerImageSearch(srv, deps)
	registerNewsSearch(srv, deps)
	registerAcademicSearch(srv, deps)
	registerPatentSearch(srv, deps)
	registerSequentialSearch(srv, deps)
	registerGetSession(srv, deps)
}

func boolPtr(b bool) *bool { return &b }

func readOnlyAnnotations(idempotent bool, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: boolPtr(false),
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolPtr(openWorld),
	}
}

package tools

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
	"github.com/zoharbabin/web-researcher-mcp/internal/useranalytics"
)

type Dependencies struct {
	Cache             cache.Cache
	Search            search.Provider
	SearchProviders   map[string]search.Provider
	PatentProviders   map[string]search.PatentProvider
	AcademicProviders map[string]search.AcademicProvider
	Scraper           *scraper.Pipeline
	Content           *content.Processor
	Sessions          session.Manager
	Metrics           *metrics.Collector
	Auditor           audit.Auditor
	Logger            *slog.Logger
	Features          Features
	// Consent records/verifies/honors consent for regulated features (#89).
	// Defaults to a Noop (grants nothing) when unset, so guarded processing is a
	// clean no-op until a regulated feature wires it in.
	Consent consent.Manager
	// UserAnalytics records consent-gated per-user usage (#92). Defaults to a
	// Noop (collects nothing). The get_my_analytics tool is registered only when
	// a non-Noop recorder is present.
	UserAnalytics useranalytics.Recorder
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

	// Regulated, opt-in tools — registered only when their feature is wired in
	// (a non-Noop dependency present), so the default tool surface is unchanged.
	if _, isNoop := deps.UserAnalytics.(*useranalytics.Noop); deps.UserAnalytics != nil && !isNoop {
		registerGetMyAnalytics(srv, deps)
	}
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

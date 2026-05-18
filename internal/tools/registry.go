package tools

import (
	"log/slog"

	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type Dependencies struct {
	Cache    cache.Cache
	Search   search.Provider
	Scraper  *scraper.Pipeline
	Content  *content.Processor
	Sessions *session.Manager
	Metrics  *metrics.Collector
	Auditor  audit.Auditor
	Logger   *slog.Logger
}

func RegisterAll(srv *server.MCPServer, deps Dependencies) {
	registerWebSearch(srv, deps)
	registerScrapePage(srv, deps)
	registerSearchAndScrape(srv, deps)
	registerImageSearch(srv, deps)
	registerNewsSearch(srv, deps)
	registerAcademicSearch(srv, deps)
	registerPatentSearch(srv, deps)
	registerSequentialSearch(srv, deps)
}

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/resources"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/server"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
	"github.com/zoharbabin/web-researcher-mcp/internal/tools"
)

var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "err", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	cacheStore := cache.NewHybrid(cache.HybridConfig{
		Memory:         cache.MemoryConfig{MaxSizeMB: cfg.CacheMaxMemoryMB},
		Disk:           cache.DiskConfig{Dir: cfg.CacheDir, EncryptionKey: cfg.CacheEncryptionKey, Version: version},
		RedisURL:       cfg.RedisURL,
		CacheIsolation: cfg.CacheIsolation,
	})
	defer cacheStore.Close()

	metricsCollector := metrics.NewCollector()
	rateLimiter := ratelimit.New(cfg.RateLimit)
	searchBreaker := circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})

	if err := search.GetLensRegistry().LoadFromDir("lenses"); err != nil {
		logger.Warn("failed to load search lenses", "err", err)
	}

	searchDeps := search.Deps{
		HTTPClient: scraper.NewSSRFSafeClient(cfg.AllowPrivateIPs),
		Breaker:    searchBreaker,
	}

	patentCfg := search.PatentProviderConfig{
		USPTOAPIKey:       cfg.Search.USPTOAPIKey,
		EPOConsumerKey:    cfg.Search.EPOConsumerKey,
		EPOConsumerSecret: cfg.Search.EPOConsumerSecret,
		LensAPIToken:      cfg.Search.LensAPIToken,
		SearchAPIKey:      cfg.Search.SearchAPIKey,
	}
	patentProviders := search.AvailablePatentProviders(patentCfg, searchDeps)

	var searchProvider search.Provider
	if cfg.Search.Routing != "" {
		routingCfg, routeErr := search.ParseRoutingConfig(cfg.Search.Routing)
		if routeErr != nil {
			logger.Error("invalid SEARCH_ROUTING", "err", routeErr)
			os.Exit(1)
		}
		providers := search.AvailableProviders(cfg.Search, searchDeps)
		if len(providers) == 0 {
			logger.Error("no search providers available for routing")
			os.Exit(1)
		}
		searchProvider = search.NewRouter(providers, search.RouterConfig{
			Routing:         routingCfg,
			Logger:          logger,
			PatentProviders: patentProviders,
		})
		logger.Info("search router initialized", "providers", len(providers),
			"patentProviders", len(patentProviders), "routing", cfg.Search.Routing)
	} else {
		searchProvider = search.NewProvider(cfg.Search, searchDeps)
		logger.Info("search provider initialized", "provider", searchProvider.Name(),
			"patentProviders", len(patentProviders))
	}

	scraperPipeline := scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  cfg.MaxScrapeConcurrency,
		AllowPrivateIPs: cfg.AllowPrivateIPs,
		AllowedDomains:  cfg.AllowedDomains,
		ChromePath:      cfg.ChromePath,
	})
	defer scraperPipeline.Close()

	contentProcessor := content.NewProcessor()
	sessionManager := session.NewManager(session.Config{
		MaxSessions: 50,
		SessionTTL:  cfg.SessionTTL,
		RedisURL:    cfg.RedisURL,
	})
	defer sessionManager.Close()

	var auditor audit.Auditor
	if cfg.Audit.Enabled {
		al, err := audit.NewLogger(audit.Config{
			Enabled:            true,
			OutputPath:         cfg.Audit.OutputPath,
			BufferSize:         cfg.Audit.BufferSize,
			IncludeRequestBody: cfg.Audit.IncludeRequestBody,
		})
		if err != nil {
			logger.Error("failed to create audit logger", "err", err)
			os.Exit(1)
		}
		auditor = al
	} else {
		auditor = audit.NewNoop()
	}
	defer auditor.Close()

	toolDeps := tools.Dependencies{
		Cache:           cacheStore,
		Search:          searchProvider,
		PatentProviders: patentProviders,
		Scraper:         scraperPipeline,
		Content:         contentProcessor,
		Sessions:        sessionManager,
		Metrics:         metricsCollector,
		Auditor:         auditor,
		Logger:          logger,
	}

	srv := server.New(server.Config{
		Name:    "web-researcher-mcp",
		Version: version,
		Logger:  logger,
	})

	tools.RegisterAll(srv.MCP(), toolDeps)
	resources.RegisterAll(srv.MCP(), metricsCollector, sessionManager, rateLimiter)

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if cfg.Port > 0 {
		httpCfg := server.HTTPConfig{
			Port:           cfg.Port,
			Auth:           auth.NewMiddleware(cfg.OAuth),
			RateLimiter:    rateLimiter,
			AllowedOrigins: cfg.AllowedOrigins,
			Metrics:        metricsCollector,
			AdminKey:       cfg.CacheAdminKey,
			Cache:          cacheStore,
			Sessions:       sessionManager,
		}
		go func() {
			if err := srv.ServeHTTP(ctx, httpCfg); err != nil {
				logger.Error("HTTP server error", "err", err)
			}
		}()
		logger.Info("HTTP transport started", "port", cfg.Port)
	}

	logger.Info("STDIO transport starting", "version", version)
	if err := srv.RunSTDIO(ctx); err != nil {
		if ctx.Err() == nil {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}

	logger.Info("shutdown complete")
}

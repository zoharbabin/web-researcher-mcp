package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/redisbackend"
	"github.com/zoharbabin/web-researcher-mcp/internal/resources"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/server"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
	"github.com/zoharbabin/web-researcher-mcp/internal/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Decision (g): config validation is fatal only in HTTP mode (Port>0),
		// where a misconfiguration is operationally significant. In STDIO mode we
		// log and continue so zero-config local use is never blocked by, e.g., a
		// missing Google key when DuckDuckGo can still serve as a fallback.
		slog.Error("configuration error", "err", err)
		if cfg.Port > 0 {
			os.Exit(1)
		}
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	for _, w := range cfg.Warnings {
		slog.Warn("configuration notice", "msg", w)
	}

	// ── Redis gate (#42): the ONE place distributed state is decided ──────────
	// Iron-clad two-path isolation: Redis is constructed if and ONLY if HTTP
	// mode is active (Port>0) AND REDIS_URL is set. STDIO never reaches this
	// code. Fail-fast: an operator who set REDIS_URL opted into cross-pod
	// correctness, so an unreachable/misconfigured Redis is a fatal startup
	// error — never a silent fallback to per-pod memory (which would reintroduce
	// the N×-rate-limit bug). When redisBackends stays nil, every store below
	// uses its in-memory/disk path, byte-for-byte unchanged.
	var redisBackends *redisbackend.Backends
	if cfg.Port > 0 && cfg.RedisURL != "" {
		rb, rerr := redisbackend.Connect(context.Background(), redisbackend.Config{
			URL:                  cfg.RedisURL,
			EncryptionKey:        cfg.CacheEncryptionKey,
			EncryptionKeyPrev:    cfg.CacheEncryptionKeyPrev,
			SessionTTL:           cfg.SessionTTL,
			MaxSessionsPerTenant: 50,
		})
		if rerr != nil {
			logger.Error("REDIS_URL is set but Redis is unavailable; refusing to start in degraded per-pod mode", "err", rerr)
			os.Exit(1)
		}
		defer rb.Close()
		redisBackends = rb
		logger.Info("distributed state enabled", "backend", "redis")
	} else if cfg.RedisURL != "" {
		// REDIS_URL set without HTTP mode: surface it loudly rather than silently
		// ignoring — STDIO is single-process and has no use for distributed state.
		logger.Warn("REDIS_URL is set but the server is in STDIO mode (no PORT); Redis is not used", "hint", "set PORT to enable HTTP + distributed state")
	}

	hybridCache := cache.NewHybrid(cache.HybridConfig{
		Memory:         cache.MemoryConfig{MaxSizeMB: cfg.CacheMaxMemoryMB},
		Disk:           cache.DiskConfig{Dir: cfg.CacheDir, EncryptionKey: cfg.CacheEncryptionKey, EncryptionKeyPrev: cfg.CacheEncryptionKeyPrev, Version: version},
		RedisURL:       cfg.RedisURL,
		CacheIsolation: cfg.CacheIsolation,
	})
	defer hybridCache.Close()

	// L2 shared cache tier: cross-pod cache fan-out when Redis is enabled.
	if redisBackends != nil {
		hybridCache.WithSharedLayer(redisBackends.SharedCache())
	}

	var cacheStore cache.Cache = hybridCache
	if cfg.CacheIsolation == "tenant" {
		cacheStore = cache.NewTenantAware(hybridCache, auth.TenantIDFromContext)
		logger.Info("cache isolation enabled", "mode", "per-tenant")
	}

	// Construct the single persist.Store shared by auth token revocation (H2) and
	// rate-limit daily-quota persistence (H7). When a CACHE_ENCRYPTION_KEY is set
	// it is disk-backed (encrypted, survives restarts) under a sibling directory
	// of the cache; otherwise it falls back to pure in-memory, preserving the
	// zero-config behavior. A disk-store construction error degrades to memory
	// rather than failing startup.
	var persistStore persist.Store
	switch {
	case redisBackends != nil:
		// Distributed: token revocation + daily quota shared across pods,
		// encrypted at rest in Redis (parity with disk).
		persistStore = redisBackends.PersistStore()
		logger.Info("persist store initialized", "backend", "redis")
	case cfg.CacheEncryptionKey != "":
		ds, perr := persist.NewDiskStore(
			filepath.Join(cfg.CacheDir, "persist"),
			cfg.CacheEncryptionKey,
			cfg.CacheEncryptionKeyPrev,
		)
		if perr != nil {
			logger.Warn("persist disk store unavailable, using in-memory", "err", perr)
			persistStore = persist.NewMemoryStore()
		} else {
			persistStore = ds
			logger.Info("persist store initialized", "backend", "encrypted-disk")
		}
	default:
		persistStore = persist.NewMemoryStore()
	}

	// Consent subsystem (#89): record-verify-honor for regulated features. It is
	// a no-op (grants nothing, stores nothing) unless at least one consent-gated
	// feature (#88/#92/#96) is enabled — no standalone CONSENT_ENABLED knob.
	var consentManager consent.Manager = consent.NewNoop()
	if cfg.Features.RegulatedEnabled() {
		consentManager = consent.NewStoreManager(persistStore)
		logger.Info("consent subsystem active", "reason", "regulated feature enabled")
	}

	// Data-subject rights registry (#85): every per-user/per-tenant store
	// registers an Exporter/Eraser here so GDPR access/erasure reaches it.
	// Sessions register unconditionally (tenant-scoped); regulated stores
	// register when enabled.
	dataSubjects := datasubject.NewRegistry()

	metricsCollector := metrics.NewCollector()
	rateLimiter := ratelimit.NewWithStore(cfg.RateLimit, persistStore)
	if redisBackends != nil {
		// Atomic cross-pod daily quota: N pods share one limit (#42).
		rateLimiter = rateLimiter.WithDailyIncrementer(redisBackends.PersistStore())
	}
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

	academicCfg := search.AcademicProviderConfig{
		OpenAlexEmail: cfg.Search.OpenAlexEmail,
		CrossRefEmail: cfg.Search.CrossRefEmail,
	}
	academicProviders := search.AvailableAcademicProviders(academicCfg, searchDeps)

	allProviders := search.AvailableProviders(cfg.Search, searchDeps)

	var searchProvider search.Provider
	if cfg.Search.Routing != "" {
		routingCfg, routeErr := search.ParseRoutingConfig(cfg.Search.Routing)
		if routeErr != nil {
			logger.Error("invalid SEARCH_ROUTING", "err", routeErr)
			os.Exit(1)
		}
		if len(allProviders) == 0 {
			logger.Error("no search providers available for routing")
			os.Exit(1)
		}
		searchProvider = search.NewRouter(allProviders, search.RouterConfig{
			Routing:           routingCfg,
			Logger:            logger,
			PatentProviders:   patentProviders,
			AcademicProviders: academicProviders,
		})
		logger.Info("search router initialized", "providers", len(allProviders),
			"patentProviders", len(patentProviders),
			"academicProviders", len(academicProviders), "routing", cfg.Search.Routing)
	} else {
		searchProvider = search.NewProvider(cfg.Search, searchDeps)
		logger.Info("search provider initialized", "provider", searchProvider.Name(),
			"patentProviders", len(patentProviders),
			"academicProviders", len(academicProviders),
			"availableProviders", len(allProviders))
	}

	scraperPipeline := scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  cfg.MaxScrapeConcurrency,
		AllowPrivateIPs: cfg.AllowPrivateIPs,
		AllowedDomains:  cfg.AllowedDomains,
		ChromePath:      cfg.ChromePath,
	})
	defer scraperPipeline.Close()

	contentProcessor := content.NewProcessor()

	// Session manager: Redis-backed (sessions survive pod restarts and are shared
	// across pods) when distributed state is enabled, else the in-memory +
	// encrypted-disk manager. Both satisfy session.Manager identically.
	var sessionManager session.Manager
	if redisBackends != nil {
		sessionManager = redisBackends.SessionManager()
		logger.Info("session manager initialized", "backend", "redis")
	} else {
		mm, err := session.NewManager(session.Config{
			MaxSessions:        50,
			MaxStepsPerSession: cfg.SessionMaxSteps,
			SessionTTL:         cfg.SessionTTL,
			DataDir:            cfg.SessionDataDir,
			EncryptionKey:      cfg.CacheEncryptionKey,
			EncryptionKeyPrev:  cfg.CacheEncryptionKeyPrev,
			RedisURL:           cfg.RedisURL,
		})
		if err != nil {
			logger.Error("failed to create session manager", "err", err)
			os.Exit(1)
		}
		sessionManager = mm
	}
	defer sessionManager.Close()

	// Sessions are tenant-scoped personal data → register them for data-subject
	// access/erasure (#85). Regulated per-user stores register here too when on.
	dataSubjects.Register("sessions", session.AsDataSubject(sessionManager), session.AsDataSubject(sessionManager))

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
		Cache:             cacheStore,
		Search:            searchProvider,
		SearchProviders:   allProviders,
		PatentProviders:   patentProviders,
		AcademicProviders: academicProviders,
		Scraper:           scraperPipeline,
		Content:           contentProcessor,
		Sessions:          sessionManager,
		Metrics:           metricsCollector,
		Auditor:           auditor,
		Logger:            logger,
		Features: tools.Features{
			SourceRecommendations: cfg.Features.SourceRecommendations,
			GenerativeUI:          cfg.Features.GenerativeUI,
		},
		Consent: consentManager,
	}

	srv := server.New(server.Config{
		Name:    "web-researcher-mcp",
		Version: version,
		Logger:  logger,
	})

	tools.RegisterAll(srv.MCP(), toolDeps)

	var providerInfos []resources.ProviderInfo
	for name := range allProviders {
		providerInfos = append(providerInfos, resources.ProviderInfo{Name: name, Type: "web"})
	}
	for name := range patentProviders {
		providerInfos = append(providerInfos, resources.ProviderInfo{Name: name, Type: "patent"})
	}
	for name := range academicProviders {
		providerInfos = append(providerInfos, resources.ProviderInfo{Name: name, Type: "academic"})
	}
	resources.RegisterAll(srv.MCP(), metricsCollector, sessionManager, rateLimiter, providerInfos)

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if cfg.Port > 0 {
		authMw := auth.NewMiddlewareWithStore(cfg.OAuth, persistStore)

		// Scope gate (C4): a server-side receiving middleware that enforces OAuth
		// scopes on tools/call. Registered ONLY in HTTP mode so the STDIO path is
		// 100% unchanged. The policy lives in auth.EnforceScopes (permissive by
		// default; fail-closed only on a present-but-insufficient scope claim).
		// A denial is returned as a CallToolResult with IsError=true so the LLM
		// can see and self-correct, not as a protocol-level error.
		srv.MCP().AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(reqCtx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				if method == "tools/call" {
					if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
						scopes := auth.ScopesFromContext(reqCtx)
						if err := authMw.EnforceScopes(scopes, params.Name); err != nil {
							res := &mcp.CallToolResult{IsError: true}
							res.Content = []mcp.Content{&mcp.TextContent{Text: "access denied: " + err.Error()}}
							return res, nil
						}
					}
				}
				return next(reqCtx, method, req)
			}
		})

		httpCfg := server.HTTPConfig{
			Port:              cfg.Port,
			Auth:              authMw,
			RateLimiter:       rateLimiter,
			AllowedOrigins:    cfg.AllowedOrigins,
			CORSStrict:        cfg.CORSStrict,
			Metrics:           metricsCollector,
			AdminKey:          cfg.AdminAPIKey,
			Cache:             cacheStore,
			Sessions:          sessionManager,
			DataSubjects:      dataSubjects,
			Consent:           consentManager,
			Auditor:           auditor,
			ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
			ReadTimeout:       cfg.HTTP.ReadTimeout,
			WriteTimeout:      cfg.HTTP.WriteTimeout,
			IdleTimeout:       cfg.HTTP.IdleTimeout,
			MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
			MaxRequestBody:    cfg.HTTP.MaxRequestBody,
			CSP:               cfg.HTTP.CSP,
			ReferrerPolicy:    cfg.HTTP.ReferrerPolicy,
			PermissionsPolicy: cfg.HTTP.PermissionsPolicy,
		}
		// HTTP and STDIO are mutually exclusive transports. When a port is
		// configured the server runs HTTP in the FOREGROUND and returns — it must
		// NOT fall through to RunSTDIO. A container started with `docker run -p
		// ... -e PORT=...` (no `-i`) hands the process a stdin already at EOF;
		// blocking on RunSTDIO there would return instantly and tear down the HTTP
		// server within milliseconds. Running HTTP in the foreground keeps the
		// process alive until ctx is cancelled (SIGINT/SIGTERM).
		logger.Info("HTTP transport starting", "port", cfg.Port, "version", version)
		if err := srv.ServeHTTP(ctx, httpCfg); err != nil {
			logger.Error("HTTP server error", "err", err)
			os.Exit(1)
		}
		logger.Info("shutdown complete")
		return
	}

	// STDIO transport (Port == 0): unchanged from the single-transport path.
	logger.Info("STDIO transport starting", "version", version)
	if err := srv.RunSTDIO(ctx); err != nil {
		if ctx.Err() == nil {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}

	logger.Info("shutdown complete")
}

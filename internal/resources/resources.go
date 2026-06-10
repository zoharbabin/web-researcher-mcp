package resources

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// ProviderInfo describes a configured search provider for the stats resource.
type ProviderInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// HealthProvider supplies a live provider/breaker health snapshot for the
// diagnostics://health Resource (#81). The Router satisfies it via its Health()
// method; resources depends on this small interface (not on the search package)
// to stay decoupled — the same pattern as metrics.AuditLossSource. A nil
// provider (single-provider / no-routing deployment) makes diagnostics://health
// report an empty, "healthy" snapshot.
type HealthProvider interface {
	// Health returns a JSON-marshalable snapshot: an aggregate status string and
	// a per-provider breaker list. Returned as `any` so resources need not import
	// the search package's concrete type.
	Health() any
}

func RegisterAll(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager session.Manager, rateLimiter *ratelimit.Limiter, providers []ProviderInfo, health HealthProvider) {
	registerResources(srv, metricsCollector, sessionManager, rateLimiter, providers)
	registerDiagnostics(srv, metricsCollector, health)
	registerPrompts(srv)
}

func registerResources(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager session.Manager, rateLimiter *ratelimit.Limiter, providers []ProviderInfo) {
	srv.AddResource(&mcp.Resource{
		URI:         "stats://tools",
		Name:        "Tool Statistics",
		Description: "Usage stats for each tool — how many times it's been called, how fast it responded, and how often errors occurred",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		stats := metricsCollector.GetToolStats()
		jsonBytes, err := json.MarshalIndent(map[string]any{"tools": stats}, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "stats://tools",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})

	srv.AddResource(&mcp.Resource{
		URI:         "stats://sessions",
		Name:        "Active Sessions",
		Description: "Count of active research sessions",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		count := sessionManager.ActiveCount()
		jsonBytes, _ := json.Marshal(map[string]any{"activeSessions": count})
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "stats://sessions",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})

	srv.AddResource(&mcp.Resource{
		URI:         "stats://rate-limits",
		Name:        "Rate Limit Status",
		Description: "How many requests you can make and how many you have left today. Only applies when connecting over the network (not in local mode).",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		cfg := rateLimiter.Config()
		stats := rateLimiter.Stats("default")
		result := map[string]any{
			"config": map[string]any{
				"perMinutePerTenant": cfg.PerTenant,
				"globalPerSecond":    cfg.Global,
				"dailyPerTenant":     cfg.DailyQuota,
			},
			"defaultTenant": stats,
			"guidance":      "Rate limits apply when connecting over the network. In local mode, only the search service's own limits apply. Without authentication set up, all users share the same allowance. Admins can adjust limits using the RATE_LIMIT_PER_TENANT and DAILY_QUOTA_PER_TENANT settings.",
		}
		jsonBytes, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "stats://rate-limits",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})

	srv.AddResource(&mcp.Resource{
		URI:         "stats://providers",
		Name:        "Configured Providers",
		Description: "Search, patent, and academic providers currently configured and available for use",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		jsonBytes, _ := json.MarshalIndent(map[string]any{"providers": providers}, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "stats://providers",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})
}

// registerDiagnostics registers the operator-facing diagnostics:// Resources
// (#81): a bounded recent-errors view and a live provider/breaker health view.
// Both are read-only, redacted (errors pass through audit.MaskSecrets at insert
// in the metrics ring), and aggregate/operator data — never LLM content.
func registerDiagnostics(srv *mcp.Server, metricsCollector *metrics.Collector, health HealthProvider) {
	srv.AddResource(&mcp.Resource{
		URI:         "diagnostics://errors/recent",
		Name:        "Recent Errors",
		Description: "The most recent tool errors (bounded, newest first) — tool, error kind, provider, and a redacted cause. Operator/debug data for troubleshooting; never contains secrets, user queries, or full URLs. Scoped to your tenant when authenticated.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Tenant scope: an authenticated caller sees only their tenant's errors;
		// the unauthenticated/STDIO single-tenant case ("") sees all. The global
		// operator view is the auth-gated HTTP dashboard, not this Resource.
		tenantID := auth.TenantIDFromContext(ctx)
		errs := metricsCollector.RecentErrors(tenantID)
		body := map[string]any{
			"count":  len(errs),
			"errors": errs,
		}
		jsonBytes, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "diagnostics://errors/recent",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})

	srv.AddResource(&mcp.Resource{
		URI:         "diagnostics://health",
		Name:        "Provider Health",
		Description: "Live health of routed search providers: an overall status (healthy/degraded/unhealthy) and each provider's circuit-breaker state. Complements stats://providers (which lists configured providers) with current availability. Empty when multi-provider routing is not enabled.",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var snapshot any
		if health != nil {
			snapshot = health.Health()
		} else {
			// No Router (single-provider / no routing): nothing to observe.
			snapshot = map[string]any{"status": "healthy", "providers": []any{}}
		}
		jsonBytes, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "diagnostics://health",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})
}

func registerPrompts(srv *mcp.Server) {
	srv.AddPrompt(&mcp.Prompt{
		Name:        "comprehensive-research",
		Description: "Guide an AI assistant through a multi-step research process",
		Arguments: []*mcp.PromptArgument{
			{Name: "topic", Description: "Research topic", Required: true},
			{Name: "depth", Description: "Research depth: quick, standard, deep (default: standard)"},
			{Name: "lens", Description: "Optional search lens to restrict to trusted sources (autocompletes to the configured lenses)"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		topic := req.Params.Arguments["topic"]
		depth := req.Params.Arguments["depth"]
		if depth == "" {
			depth = "standard"
		}
		lens := req.Params.Arguments["lens"]

		steps := "3"
		if depth == "deep" {
			steps = "6"
		} else if depth == "quick" {
			steps = "2"
		}

		lensGuidance := ""
		if lens != "" {
			lensGuidance = "Restrict searches to the \"" + lens + "\" lens (trusted, domain-scoped sources) where it fits the question.\n"
		}

		prompt := "Research the topic: " + topic + "\n\n" +
			"Available tools: web_search (add a lens to restrict to trusted sources, or a claim to get per-result evidence), scrape_page, search_and_scrape, news_search, academic_search, citation_graph, patent_search, filing_search (SEC), legal_search (US case law), econ_search (FRED/World Bank), clinical_search (ClinicalTrials.gov), image_search.\n" +
			"Track progress with sequential_search (pass sessionId between calls); package results with research_export + format_bibliography.\n" +
			"Before relying on any source, verify it: verify_citation (one citation) or audit_bibliography (a whole reference list) — checks existence, retraction, dead links, and whether a source actually supports a claim.\n\n" +
			lensGuidance +
			"Research depth: " + depth + " (" + steps + " steps)\n\n" +
			"Guidance:\n" +
			"- Start broad, then go deeper based on what you find.\n" +
			"- If a tool returns zero results with a 'hints' object, follow its suggestedActions.\n" +
			"- If errors include retryable:true, respect retryAfterSeconds before retrying.\n" +
			"- Cross-reference findings across multiple sources for accuracy.\n" +
			"- Verify citations before presenting them; never cite a source you haven't confirmed exists.\n" +
			"- End with a summary including citations from scrape_page results.\n"

		return &mcp.GetPromptResult{
			Description: "Comprehensive research guide for: " + topic,
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: prompt},
				},
			},
		}, nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "fact-check",
		Description: "Verify a claim using multiple independent sources",
		Arguments: []*mcp.PromptArgument{
			{Name: "claim", Description: "The claim to verify", Required: true},
			{Name: "context", Description: "Additional context about the claim"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		claim := req.Params.Arguments["claim"]
		extra := req.Params.Arguments["context"]

		prompt := "Fact-check the following claim: \"" + claim + "\"\n\n"
		if extra != "" {
			prompt += "Context: " + extra + "\n\n"
		}
		prompt += "Available tools: web_search and search_and_scrape (both accept a claim parameter that returns the most claim-relevant sentences as evidence), news_search, scrape_page, academic_search.\n" +
			"To check a specific source you find: verify_citation confirms a DOI/URL/reference exists, matches a real record, isn't retracted, and still resolves — evidence, never a verdict.\n" +
			"Use sequential_search to track your verification steps.\n\n" +
			"Approach:\n" +
			"- Search for evidence both supporting and contradicting the claim; pass the claim to web_search/search_and_scrape to surface the relevant sentences.\n" +
			"- Evaluate source authority and recency.\n" +
			"- Run verify_citation on any source you intend to cite — a real-looking citation may be fabricated or retracted.\n" +
			"- If search_and_scrape returns status:'partial', check scrapeFailures for context.\n" +
			"- Report confidence level (high/medium/low) with reasoning and cited, verified sources.\n"

		return &mcp.GetPromptResult{
			Description: "Fact-check: " + claim,
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: prompt},
				},
			},
		}, nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "competitive-analysis",
		Description: "Research competitors in a given market",
		Arguments: []*mcp.PromptArgument{
			{Name: "company", Description: "Company to analyze", Required: true},
			{Name: "market", Description: "Market or industry context"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		company := req.Params.Arguments["company"]
		market := req.Params.Arguments["market"]

		prompt := "Conduct a competitive analysis for: " + company + "\n"
		if market != "" {
			prompt += "Market: " + market + "\n"
		}
		prompt += "\nAvailable tools: web_search, news_search, patent_search, filing_search (SEC EDGAR — 10-K/10-Q/8-K + XBRL financials), econ_search (FRED/World Bank macro data), search_and_scrape, scrape_page, academic_search.\n" +
			"Use sequential_search to track research across steps (preserves progress if context is lost).\n\n" +
			"Research areas to cover:\n" +
			"- Company information, recent developments, and market position\n" +
			"- Financial disclosures via filing_search (set facts=true for structured XBRL revenue/income/EPS)\n" +
			"- Patent portfolio and R&D direction (via patent_search with assignee parameter)\n" +
			"- News coverage and announcements\n" +
			"- Synthesize into strengths, weaknesses, opportunities, threats\n\n" +
			"If any tool returns zero results with hints, follow the suggestedActions to broaden or redirect your search.\n"

		return &mcp.GetPromptResult{
			Description: "Competitive analysis: " + company,
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: prompt},
				},
			},
		}, nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "literature-review",
		Description: "Systematic review of academic literature on a topic",
		Arguments: []*mcp.PromptArgument{
			{Name: "topic", Description: "Research topic", Required: true},
			{Name: "year_from", Description: "Start year for papers"},
			{Name: "year_to", Description: "End year for papers"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		topic := req.Params.Arguments["topic"]
		yearFrom := req.Params.Arguments["year_from"]
		yearTo := req.Params.Arguments["year_to"]

		prompt := "Conduct a systematic literature review on: " + topic + "\n\n"
		if yearFrom != "" || yearTo != "" {
			prompt += "Time range: " + yearFrom + " to " + yearTo + "\n\n"
		}
		prompt += "Available tools: academic_search, citation_graph (trace what a paper cites and what cites it), clinical_search (ClinicalTrials.gov), web_search, scrape_page, search_and_scrape.\n" +
			"Use sequential_search to track progress across iterations (sessions persist 4 hours and survive restarts).\n" +
			"Assemble the reference list with format_bibliography (APA/MLA/BibTeX/RIS/CSL-JSON — Zotero/EndNote/Mendeley-ready), then audit_bibliography over the whole list to flag any retracted, dead-linked, not-found, or mischaracterized citations before you submit.\n\n" +
			"Approach:\n" +
			"- Use academic_search with year filters and source parameters for targeted results; citation_graph to map influential/related work.\n" +
			"- Use scrape_page on paper URLs to get abstracts and key findings (returns APA/MLA citations).\n" +
			"- Identify major themes, methodologies, and gaps in the literature.\n" +
			"- Before finalizing, run audit_bibliography on your reference list — a systematic review must not cite a retracted or fabricated study.\n" +
			"- If academic_search returns a hints object, follow its suggestions to broaden coverage.\n"

		return &mcp.GetPromptResult{
			Description: "Literature review: " + topic,
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: prompt},
				},
			},
		}, nil
	})
}

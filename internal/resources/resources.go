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

// LensInfo describes a single curated search lens for the lenses://catalog
// resource. Populated from search.GetLensRegistry() in main.go and passed via
// DI to keep the resources package decoupled from the search package.
type LensInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DomainCount int    `json:"domainCount"`
	HasCX       bool   `json:"hasCX"`
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

func RegisterAll(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager session.Manager, rateLimiter *ratelimit.Limiter, providers []ProviderInfo, health HealthProvider, lenses []LensInfo) {
	registerResources(srv, metricsCollector, sessionManager, rateLimiter, providers, lenses)
	registerDiagnostics(srv, metricsCollector, health)
	registerPrompts(srv)
}

func registerResources(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager session.Manager, rateLimiter *ratelimit.Limiter, providers []ProviderInfo, lenses []LensInfo) {
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
		Description: "Every provider currently configured and available, with its capability type (web, patent, academic, filing, legal, econ, clinical, answer, structured)",
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

	srv.AddResource(&mcp.Resource{
		URI:         "lenses://catalog",
		Name:        "Search Lens Catalog",
		Description: "Available search lenses — curated domain sets for focused searches. Pass a lens name to web_search, academic_search, news_search, or image_search to restrict results to authoritative sources for that domain.",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		jsonBytes, _ := json.MarshalIndent(map[string]any{"lenses": lenses}, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "lenses://catalog",
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

func buildBrandStep1(company, depth string, includeTokens bool) string {
	tokensStr := "false"
	if includeTokens {
		tokensStr = "true"
	}
	return "Research the brand identity for: " + company + "\n\n" +
		"Step 1 — Gather brand data\n" +
		"Call brand_research with:\n" +
		"  url or company_name: " + company + "\n" +
		"  depth: " + depth + "\n" +
		"  include_design_tokens: " + tokensStr + "\n\n" +
		"The tool returns colors, logos, typography, tone_of_voice, social handles, and a coverage object.\n" +
		"Check coverage: if colors/logos/typography are \"none\", note the gap and work with what was found."
}

func buildBrandStep2(useCase string) string {
	switch useCase {
	case "landing_page":
		return "Step 2 — Apply brand identity to a landing page\n\n" +
			"Using the brand_research result:\n" +
			"- Primary CTA button: use colors.primary as background; ensure AA contrast with white text (luminance > 0.18 passes).\n" +
			"- Background: colors.background or white if absent.\n" +
			"- Hero image overlay: colors.primary at 60% opacity.\n" +
			"- Typography: heading.family for headlines; body.family for body copy. If heading is absent, use body for all.\n" +
			"- Logo placement: logos.primary URL in the top-left nav. If SVG available, prefer it over PNG for sharp scaling.\n" +
			"- Tone: match tone_of_voice.summary and attributes in all headline and subhead copy.\n\n" +
			"Produce:\n" +
			"1. Color palette table (name, hex, role, where used on page)\n" +
			"2. Typography spec (heading and body font, weights, size scale if available)\n" +
			"3. Sample headline + subhead that reflects the brand tone\n" +
			"4. Component guidance: hero, nav, CTA, footer — what color/font/spacing each uses"
	case "email":
		return "Step 2 — Apply brand identity to an email template\n\n" +
			"Using the brand_research result:\n" +
			"- Header background: colors.primary. Header logo: logos.primary URL.\n" +
			"- Body background: colors.background or #ffffff. Body text: colors.text or #222222.\n" +
			"- CTA button: colors.primary background, white label.\n" +
			"- Font stack: body.family with web-safe fallbacks (Arial, sans-serif).\n" +
			"- Tone: tone_of_voice.summary guides subject line and preheader copy.\n\n" +
			"Produce:\n" +
			"1. Email color spec (header, body, CTA, footer background + text colors)\n" +
			"2. Font stack (primary + fallbacks)\n" +
			"3. Sample subject line + preheader in brand tone\n" +
			"4. HTML inline-style snippet for the header block"
	case "social_post":
		return "Step 2 — Apply brand identity to social content\n\n" +
			"Using the brand_research result:\n" +
			"- Background fill: colors.primary or colors.accent for graphics.\n" +
			"- Logo mark: logos.icon URL for small placements; logos.primary for full-width.\n" +
			"- Tone: tone_of_voice.attributes drive caption voice (formal/conversational/bold/etc).\n" +
			"- Dos and don'ts: if tone_of_voice.dos_and_donts is present, include it verbatim.\n\n" +
			"Produce:\n" +
			"1. Visual identity notes for the post graphic (colors, logo, typography)\n" +
			"2. Three sample captions — Twitter/X, LinkedIn, Instagram — in brand tone\n" +
			"3. Suggested hashtags derived from identity.industry and brand attributes"
	case "video_brief":
		return "Step 2 — Apply brand identity to a video production brief\n\n" +
			"Using the brand_research result:\n" +
			"- Color grade / lower-third colors: colors.primary + colors.secondary.\n" +
			"- Logo bug: logos.icon URL; placement: lower-right at 8% width.\n" +
			"- Motion titles: typography.heading.family if available.\n" +
			"- Voiceover tone: tone_of_voice.summary + attributes.\n" +
			"- Background music mood: derived from tone_of_voice attributes (e.g. \"innovative + professional\" → \"corporate-uplifting\").\n\n" +
			"Produce:\n" +
			"1. Brand color values for motion graphics and lower thirds\n" +
			"2. Logo usage spec (size, placement, clearspace)\n" +
			"3. Typography spec for title cards and lower thirds\n" +
			"4. Voiceover tone direction (2–3 sentences)\n" +
			"5. Music mood descriptor"
	case "design_tokens":
		return "Step 2 — Export brand identity as design tokens\n\n" +
			"brand_research was called with include_design_tokens: true.\n" +
			"The result includes a design_tokens object in W3C DTCG format ($value/$type per token).\n\n" +
			"Produce:\n" +
			"1. The design_tokens object formatted as a JSON code block ready to paste into Style Dictionary, Tokens Studio, or Figma Variables.\n" +
			"2. A short mapping table: token name → role (e.g. color.brand → primary CTA).\n" +
			"3. Any gaps (tokens that could not be derived because source data was absent)."
	default: // full_guidelines
		return "Step 2 — Produce comprehensive brand guidelines\n\n" +
			"Using the brand_research result, produce a structured brand guidelines document:\n\n" +
			"## Brand Identity\n" +
			"- Company name, tagline, description (from identity fields)\n" +
			"- Industry and founding context if available\n\n" +
			"## Color System\n" +
			"- Primary, secondary, accent, background, text colors with hex values\n" +
			"- Full palette table (name, hex, role)\n" +
			"- Usage rules: where each color appears\n\n" +
			"## Logo & Icon\n" +
			"- Logo URLs and formats (primary, dark variant if available, icon/favicon)\n" +
			"- Usage guidance: preferred format, minimum size, clearspace\n\n" +
			"## Typography\n" +
			"- Heading and body typefaces with weights\n" +
			"- Google Fonts URL if applicable\n" +
			"- Type scale if available\n\n" +
			"## Tone of Voice\n" +
			"- Summary of brand voice\n" +
			"- Attributes list\n" +
			"- Dos and don'ts if available\n\n" +
			"## Design Tokens\n" +
			"- Only if design_tokens was returned: W3C DTCG JSON code block\n\n" +
			"## Coverage Summary\n" +
			"- Reflect the coverage object (full/partial/none per dimension)\n" +
			"- Note any gaps and suggest how they might be filled"
	}
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

	srv.AddPrompt(&mcp.Prompt{
		Name:        "brand-guidelines",
		Description: "Research a company's brand identity and produce use-case-specific brand-compliant guidance. Calls brand_research, interprets colors/logos/typography/tone, and returns actionable creative direction.",
		Arguments: []*mcp.PromptArgument{
			{Name: "company", Description: "Company name or domain to research (e.g. 'kaltura.com' or 'Kaltura')", Required: true},
			{Name: "use_case", Description: "Target output: landing_page | email | social_post | video_brief | design_tokens | full_guidelines (default: full_guidelines)"},
			{Name: "depth", Description: "Research depth passed to brand_research: quick | standard | full (default: standard)"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		company := req.Params.Arguments["company"]
		useCase := req.Params.Arguments["use_case"]
		depth := req.Params.Arguments["depth"]

		if useCase == "" {
			useCase = "full_guidelines"
		}
		if depth == "" {
			depth = "standard"
		}

		includeTokens := useCase == "design_tokens"

		step1 := buildBrandStep1(company, depth, includeTokens)
		step2 := buildBrandStep2(useCase)

		return &mcp.GetPromptResult{
			Description: "Brand guidelines (" + useCase + "): " + company,
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: step1 + "\n\n" + step2}},
			},
		}, nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "company-recon",
		Description: "Multi-phase OSINT recon: certificate transparency, DNS/infrastructure, archive mining, analytics correlation, and business intelligence for a target company or domain. Returns a cited, confidence-tiered intelligence report.",
		Arguments: []*mcp.PromptArgument{
			{Name: "target", Description: "Company name, domain, or both — e.g. 'Acme Corp acme.com'", Required: true},
			{Name: "depth", Description: "quick (phases 1+6+8) | standard (phases 1-4+6-9) | deep (all 9 phases) — default: standard"},
			{Name: "focus", Description: "sales_intel | security | due_diligence | brand_protection — adjusts phase ordering and emphasis"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		target := req.Params.Arguments["target"]
		depth := req.Params.Arguments["depth"]
		focus := req.Params.Arguments["focus"]
		if depth == "" {
			depth = "standard"
		}
		prompt := buildCompanyReconPrompt(target, depth, focus)
		return &mcp.GetPromptResult{
			Description: "Company OSINT recon: " + target,
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: prompt}},
			},
		}, nil
	})
}

// buildCompanyReconPrompt constructs the multi-phase OSINT recon prompt for a
// target company or domain. Phases are filtered by depth; focus adjusts ordering
// and emphasis in the instructions.
func buildCompanyReconPrompt(target, depth, focus string) string {
	phaseSet := companyReconPhaseSet(depth)
	focusGuidance := companyReconFocusGuidance(focus)

	p := "Run a multi-phase OSINT intelligence recon on: " + target + "\n" +
		"Depth: " + depth + " | Focus: " + focusGuidance + "\n\n" +
		"Available tools: web_search, scrape_page, search_and_scrape, news_search, filing_search, research_export.\n" +
		"Use the osint lens (lens: osint) for web_search calls that target OSINT data sources.\n\n"

	if phaseSet["phase1"] {
		p += "=== Phase 1 — Company Profiling ===\n" +
			"web_search: \"" + target + " about founded CEO headquarters\"\n" +
			"search_and_scrape: company homepage, LinkedIn, Crunchbase\n" +
			"news_search: recent news about the company (last 90 days)\n" +
			"Goal: establish company identity, leadership, HQ, core products, and recent developments.\n\n"
	}

	if phaseSet["phase2"] {
		p += "=== Phase 2 — Certificate Transparency ===\n" +
			"scrape_page: https://crt.sh/?q=%25.{domain}&output=json  (replace {domain} with the target domain)\n" +
			"Parse the JSON array: extract name_value fields, deduplicate subdomains, note wildcard SANs.\n" +
			"Note: crt.sh is a free, public CT log aggregator — no API key required.\n\n"
	}

	if phaseSet["phase3"] {
		p += "=== Phase 3 — DNS / Infrastructure ===\n" +
			"web_search lens=osint: site:securitytrails.com \"{domain}\" DNS history\n" +
			"web_search lens=osint: site:censys.io \"{domain}\"\n" +
			"scrape_page: https://hackertarget.com/find-dns-host-records/?q={domain}\n" +
			"Goal: map IP blocks, ASN, name servers, historical DNS changes, and cloud providers in use.\n\n"
	}

	if phaseSet["phase4"] {
		p += "=== Phase 4 — Archive Mining ===\n" +
			"scrape_page: https://web.archive.org/cdx/search/cdx?url={domain}/*&output=json&collapse=urlkey&limit=500&fl=original,timestamp,statuscode\n" +
			"Analyze the returned URL list for patterns: login pages (/login, /signin, /auth), API endpoints (/api/, /v1/, /graphql), admin paths (/admin, /dashboard), JS bundles, staging subdomains.\n" +
			"Note: Wayback CDX API is free and does not require authentication.\n\n"
	}

	if phaseSet["phase5"] {
		p += "=== Phase 5 — Code / Config Search ===\n" +
			"web_search lens=osint: site:github.com \"{domain}\"\n" +
			"web_search: \"{domain}\" filetype:yaml OR filetype:json site:github.com\n" +
			"Goal: find SDK usage, third-party integrations, config leaks, and developer tooling references in public repos.\n" +
			"Note: results are limited to indexed/public GitHub pages — GitHub Code Search (if separately available) gives higher recall.\n\n"
	}

	if phaseSet["phase6"] {
		p += "=== Phase 6 — Web / Content Discovery ===\n" +
			"search_and_scrape: \"{domain}\" inurl:login\n" +
			"search_and_scrape: site:{domain} -www\n" +
			"web_search lens=osint: \"{domain}\" archive OR leaked OR exposed\n" +
			"Goal: surface exposed login surfaces, forgotten subdomains, and any indexed sensitive paths.\n\n"
	}

	if phaseSet["phase7"] {
		p += "=== Phase 7 — Analytics / Tracker Correlation ===\n" +
			"First, find the target's analytics IDs by scraping their homepage or using web_search for their GTM/UA tags.\n" +
			"For UA-XXXXXX or GTM-XXXXXX IDs found:\n" +
			"  scrape_page: https://hackertarget.com/reverse-analytics-search/?q={analytics_id}\n" +
			"  web_search: \"{analytics_id}\" site:publicwww.com\n" +
			"IMPORTANT: GA4 IDs (G-XXXXXX) are NOT correlatable via reverse-analytics lookup — only Universal Analytics (UA-XXXXXX) and Google Tag Manager (GTM-XXXXXX) IDs work for finding co-deployed sites.\n" +
			"Goal: identify other domains sharing the same analytics account — signals subsidiaries, partner networks, or acquired properties.\n\n"
	}

	if phaseSet["phase8"] {
		p += "=== Phase 8 — Business Intelligence ===\n" +
			"web_search: \"" + target + " customers case studies\"\n" +
			"web_search: \"" + target + "\" site:g2.com OR site:trustradius.com\n" +
			"filing_search: search for SEC 10-K/10-Q filings if the company is publicly traded\n" +
			"news_search: \"" + target + "\" acquisitions funding partnerships\n" +
			"Goal: identify customers, revenue signals, strategic partnerships, and corporate structure.\n\n"
	}

	if phaseSet["phase9"] {
		p += "=== Phase 9 — Confidence Scoring + Report ===\n" +
			"For each discovered customer, partner, or subsidiary, assign a confidence tier:\n" +
			"  CONFIRMED — press release, case study, or official SEC filing\n" +
			"  STRONG    — multiple independent credible sources\n" +
			"  MODERATE  — single credible source, not independently confirmed\n" +
			"  WEAK      — inferred from analytics/embed correlation only; not independently verified\n\n" +
			"Call research_export to consolidate all findings into a structured report.\n" +
			"The report must include:\n" +
			"  - Company profile summary\n" +
			"  - Discovered infrastructure (subdomains, IPs, ASN)\n" +
			"  - Certificate transparency findings\n" +
			"  - Archive URL patterns of interest\n" +
			"  - Business intelligence (customers, partners, filings)\n" +
			"  - Confidence-tiered findings table\n" +
			"  - Known limitations for this run (e.g. GA4 correlation gap, Shodan/Censys depth without API keys)\n\n"
	}

	p += "Known limitations:\n" +
		"- GA4 (G-XXXXXX) analytics IDs cannot be reverse-correlated — only UA-XXXXXX and GTM-XXXXXX work\n" +
		"- Live JavaScript inspection requires a browser (Playwright MCP if available); fall back to static source-code search\n" +
		"- Shodan and BuiltWith depth is limited without API keys — infrastructure data comes from web-searchable pages only\n" +
		"- GitHub Code Search gives higher recall than web_search on github.com; use it if separately available\n"

	return p
}

// companyReconPhaseSet returns which phases are active for the given depth.
func companyReconPhaseSet(depth string) map[string]bool {
	switch depth {
	case "quick":
		return map[string]bool{
			"phase1": true,
			"phase6": true,
			"phase8": true,
			"phase9": true,
		}
	case "deep":
		return map[string]bool{
			"phase1": true, "phase2": true, "phase3": true, "phase4": true,
			"phase5": true, "phase6": true, "phase7": true, "phase8": true,
			"phase9": true,
		}
	default: // standard
		return map[string]bool{
			"phase1": true, "phase2": true, "phase3": true, "phase4": true,
			"phase6": true, "phase7": true, "phase8": true, "phase9": true,
		}
	}
}

// companyReconFocusGuidance returns a short label and emphasis note for the focus.
func companyReconFocusGuidance(focus string) string {
	switch focus {
	case "sales_intel":
		return "sales_intel — prioritise customer discovery (Phase 8), analytics correlation (Phase 7), and business intelligence"
	case "security":
		return "security — prioritise certificate transparency (Phase 2), DNS/infrastructure (Phase 3), archive mining (Phase 4), and code/config search (Phase 5)"
	case "due_diligence":
		return "due_diligence — prioritise company profiling (Phase 1), business intelligence (Phase 8), SEC filings, and corporate structure"
	case "brand_protection":
		return "brand_protection — prioritise certificate transparency (Phase 2) for look-alike domains, web/content discovery (Phase 6), and analytics correlation (Phase 7)"
	default:
		return "general — balanced coverage across all active phases"
	}
}

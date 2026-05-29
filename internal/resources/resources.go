package resources

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// ProviderInfo describes a configured search provider for the stats resource.
type ProviderInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func RegisterAll(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager *session.Manager, rateLimiter *ratelimit.Limiter, providers []ProviderInfo) {
	registerResources(srv, metricsCollector, sessionManager, rateLimiter, providers)
	registerPrompts(srv)
}

func registerResources(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager *session.Manager, rateLimiter *ratelimit.Limiter, providers []ProviderInfo) {
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

func registerPrompts(srv *mcp.Server) {
	srv.AddPrompt(&mcp.Prompt{
		Name:        "comprehensive-research",
		Description: "Guide an AI assistant through a multi-step research process",
		Arguments: []*mcp.PromptArgument{
			{Name: "topic", Description: "Research topic", Required: true},
			{Name: "depth", Description: "Research depth: quick, standard, deep (default: standard)"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		topic := req.Params.Arguments["topic"]
		depth := req.Params.Arguments["depth"]
		if depth == "" {
			depth = "standard"
		}

		steps := "3"
		if depth == "deep" {
			steps = "6"
		} else if depth == "quick" {
			steps = "2"
		}

		prompt := "Research the topic: " + topic + "\n\n" +
			"Available tools: web_search, scrape_page, search_and_scrape, news_search, academic_search, patent_search, image_search.\n" +
			"Use sequential_search to track progress across steps (pass sessionId between calls).\n\n" +
			"Research depth: " + depth + " (" + steps + " steps)\n\n" +
			"Guidance:\n" +
			"- Start broad, then go deeper based on what you find.\n" +
			"- If a tool returns zero results with a 'hints' object, follow its suggestedActions.\n" +
			"- If errors include retryable:true, respect retryAfterSeconds before retrying.\n" +
			"- Cross-reference findings across multiple sources for accuracy.\n" +
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
		prompt += "Available tools: web_search, news_search, search_and_scrape, scrape_page, academic_search.\n" +
			"Use sequential_search to track your verification steps.\n\n" +
			"Approach:\n" +
			"- Search for evidence both supporting and contradicting the claim.\n" +
			"- Evaluate source authority and recency.\n" +
			"- If search_and_scrape returns status:'partial', check scrapeFailures for context.\n" +
			"- Report confidence level (high/medium/low) with reasoning and cited sources.\n"

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
		prompt += "\nAvailable tools: web_search, news_search, patent_search, search_and_scrape, scrape_page, academic_search.\n" +
			"Use sequential_search to track research across steps (preserves progress if context is lost).\n\n" +
			"Research areas to cover:\n" +
			"- Company information, recent developments, and market position\n" +
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
		prompt += "Available tools: academic_search, web_search, scrape_page, search_and_scrape.\n" +
			"Use sequential_search to track progress across iterations (sessions persist 4 hours and survive restarts).\n\n" +
			"Approach:\n" +
			"- Use academic_search with year filters and source parameters for targeted results.\n" +
			"- Use scrape_page on paper URLs to get abstracts and key findings (returns APA/MLA citations).\n" +
			"- Identify major themes, methodologies, and gaps in the literature.\n" +
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

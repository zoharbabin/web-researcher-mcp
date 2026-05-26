package resources

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func RegisterAll(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager *session.Manager, rateLimiter *ratelimit.Limiter) {
	registerResources(srv, metricsCollector, sessionManager, rateLimiter)
	registerPrompts(srv)
}

func registerResources(srv *mcp.Server, metricsCollector *metrics.Collector, sessionManager *session.Manager, rateLimiter *ratelimit.Limiter) {
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
			"Follow this systematic process (" + steps + " steps):\n\n" +
			"1. BROAD SEARCH: Use web_search to find overview information.\n" +
			"2. IDENTIFY SOURCES: From the results, identify the most authoritative sources.\n" +
			"3. DEEP DIVE: Use scrape_page on the top 3-5 sources to extract detailed content.\n"

		if depth != "quick" {
			prompt += "4. CROSS-REFERENCE: Compare findings across sources for consistency.\n" +
				"5. IDENTIFY GAPS: Note any knowledge gaps or contradictions.\n"
		}
		if depth == "deep" {
			prompt += "6. ACADEMIC: Use academic_search for peer-reviewed sources on the topic.\n"
		}

		prompt += "\nSummarize findings with citations. Use sequential_search to track your research progress."

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
		prompt += "Process:\n" +
			"1. Search for evidence supporting the claim\n" +
			"2. Search for evidence contradicting the claim\n" +
			"3. Evaluate source authority and recency\n" +
			"4. Report confidence level (high/medium/low) with reasoning\n\n" +
			"Use web_search and scrape_page to gather evidence from multiple independent sources."

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
		prompt += "\nResearch plan:\n" +
			"1. Use web_search to find company information and recent news\n" +
			"2. Use news_search to find recent developments and announcements\n" +
			"3. Use patent_search to analyze their patent portfolio and R&D focus\n" +
			"4. Synthesize findings into strengths, weaknesses, opportunities, threats\n\n" +
			"Use search_and_scrape for deeper analysis of key sources."

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
		prompt += "Process:\n" +
			"1. Use academic_search to find relevant papers\n" +
			"2. Use scrape_page to get abstracts and key findings\n" +
			"3. Identify major themes, methodologies, and findings\n" +
			"4. Note gaps in the literature and future research directions\n" +
			"5. Provide properly formatted citations\n\n" +
			"Use sequential_search to track progress across multiple search iterations."

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

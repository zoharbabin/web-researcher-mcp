package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// maxRefinementRounds bounds the auto-executed refinement searches for
// depth="thorough" (acceptance criterion: <=3). Each round is one web search.
const maxRefinementRounds = 3

// applyDepth augments the response per the requested iteration-assist depth.
// quick (default/unknown) is a no-op. standard adds coverage analysis +
// refinement-query suggestions. thorough additionally runs up to
// maxRefinementRounds web searches for those suggestions and attaches the
// merged, provenance-tagged results. It never synthesizes an answer — only
// surfaces richer metadata and (for thorough) raw results the caller decides
// how to use.
func applyDepth(ctx context.Context, deps Dependencies, input sequentialSearchInput, idx *session.SessionIndex, output map[string]any) {
	depth := strings.ToLower(strings.TrimSpace(input.Depth))
	if depth != "standard" && depth != "thorough" {
		return // quick / unknown → unchanged behavior
	}

	coverage := content.AnalyzeCoverage(sourcesToCoverage(idx.Sources))
	suggestions := refinementQueries(idx, coverage)

	output["depth"] = depth
	output["coverage"] = coverage
	if len(suggestions) > 0 {
		output["refinementQueries"] = suggestions
	}

	if depth != "thorough" || len(suggestions) == 0 || deps.Search == nil {
		return
	}

	// Use idx.ID, not input.SessionID: on the session-creating call (step 1)
	// input.SessionID is empty, but auto-discovered sources must still be tracked
	// onto the session that was just created.
	sessionID := idx.ID

	rounds := suggestions
	if len(rounds) > maxRefinementRounds {
		rounds = rounds[:maxRefinementRounds]
		output["refinementNote"] = fmt.Sprintf("Auto-ran the first %d of %d suggested refinement queries (bounded).", maxRefinementRounds, len(suggestions))
	}

	refined := make([]map[string]any, 0, len(rounds))
	for _, q := range rounds {
		results, err := deps.Search.Web(ctx, search.WebSearchParams{Query: q, NumResults: 5})
		entry := map[string]any{"query": q}
		if err != nil {
			entry["error"] = "search failed"
			trackOutcome(ctx, deps, sessionID, deps.Search.Name(), false, "upstream_error", "")
		} else {
			entry["results"] = searchResultsToMaps(results)
			entry["resultCount"] = len(results)
			trackSources(ctx, deps, sessionID, searchResultsToSources(results))
			trackOutcome(ctx, deps, sessionID, deps.Search.Name(), len(results) > 0, "", "")
		}
		refined = append(refined, entry)
	}
	output["refinementResults"] = refined
}

// sourcesToCoverage reduces recorded session sources to coverage inputs.
func sourcesToCoverage(sources []session.ResearchSource) []content.CoverageInput {
	out := make([]content.CoverageInput, 0, len(sources))
	for _, s := range sources {
		out = append(out, content.CoverageInput{URL: s.URL, Type: s.Relevance})
	}
	return out
}

// refinementQueries derives suggested follow-up search queries from the research
// goal, the active knowledge gaps, and the coverage gaps. Deterministic and
// de-duplicated; capped so the list stays actionable.
func refinementQueries(idx *session.SessionIndex, cov content.Coverage) []string {
	goal := strings.TrimSpace(idx.ResearchGoal)
	var out []string
	seen := map[string]bool{}
	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, q)
	}

	// Knowledge gaps the caller flagged are the strongest refinement signal.
	for _, g := range idx.ActiveGaps {
		if goal != "" {
			add(goal + " " + g.Description)
		} else {
			add(g.Description)
		}
	}

	// Coverage-derived nudges: diversify away from an over-represented domain.
	if cov.DominantDomain != "" && goal != "" {
		add(goal + " -site:" + cov.DominantDomain)
	}

	// Type-balance nudge: if everything is one type, suggest a complementary lens.
	if goal != "" && len(cov.SourceTypes) == 1 {
		for t := range cov.SourceTypes {
			switch t {
			case "academic":
				add(goal + " latest news")
			case "news":
				add(goal + " research paper")
			default:
				add(goal + " peer-reviewed study")
			}
		}
	}

	if len(out) > maxRefinementRounds*2 {
		out = out[:maxRefinementRounds*2]
	}
	return out
}

// searchResultsToMaps renders web results as plain JSON objects for the
// provenance-tagged refinement payload.
func searchResultsToMaps(results []search.SearchResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Snippet,
		})
	}
	return out
}

type sequentialSearchInput struct {
	SearchStep         string   `json:"searchStep" jsonschema:"Summary of what was researched or discovered in this step. Be descriptive to build a useful research trail.,required"`
	StepNumber         int      `json:"stepNumber" jsonschema:"Current step number (start at 1 for a new session). Must increment sequentially.,required"`
	NextStepNeeded     bool     `json:"nextStepNeeded" jsonschema:"Set true if more research steps will follow; false to mark the session complete.,required"`
	TotalStepsEstimate int      `json:"totalStepsEstimate,omitempty" jsonschema:"Your estimate of total steps needed. Update as scope becomes clearer."`
	SessionID          string   `json:"sessionId,omitempty" jsonschema:"Session ID returned from the first call. Required for steps 2+. Omit to start a new session."`
	IsRevision         bool     `json:"isRevision,omitempty" jsonschema:"Set true if this step revises a previous step's findings."`
	RevisesStep        int      `json:"revisesStep,omitempty" jsonschema:"The step number being revised (required if isRevision is true)."`
	BranchFromStep     int      `json:"branchFromStep,omitempty" jsonschema:"Step number to branch from, for exploring alternative research directions."`
	BranchID           string   `json:"branchId,omitempty" jsonschema:"Identifier for this research branch (e.g. 'technical-approach' vs 'business-angle')."`
	KnowledgeGap       string   `json:"knowledgeGap,omitempty" jsonschema:"A specific gap or unanswered question identified during this step that needs further investigation."`
	ResearchGoal       string   `json:"researchGoal,omitempty" jsonschema:"The question or goal driving this research. Set on step 1; ignored on later steps."`
	Reasoning          string   `json:"reasoning,omitempty" jsonschema:"Why you chose this search direction over alternatives."`
	Confidence         string   `json:"confidence,omitempty" jsonschema:"Confidence in this step's findings: high, medium, or low."`
	RejectedApproaches []string `json:"rejectedApproaches,omitempty" jsonschema:"Approaches considered but rejected, with brief reasons."`
	SessionSummary     string   `json:"sessionSummary,omitempty" jsonschema:"Running summary of research so far. Update periodically for better session recovery."`
	ResponseMode       string   `json:"responseMode,omitempty" jsonschema:"Force response format: full or summary. Default: auto (full for 8 or fewer steps, summary for more)."`
	Depth              string   `json:"depth,omitempty" jsonschema:"Iteration assist level: quick (default — record the step and return), standard (also analyze coverage of sources gathered so far and suggest refinement queries; you decide whether to act), or thorough (also auto-run up to 3 suggested refinement searches and return their merged, provenance-tagged results). Never synthesizes an answer."`
}

func registerSequentialSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "sequential_search",
		Description:  "Keep track of a multi-step research project. Use this alongside web_search or search_and_scrape to record what you've found at each step, note unanswered questions, and explore alternative angles (branching). Start a new session with stepNumber=1, then pass the returned sessionId for each follow-up step. Mark the session complete by setting nextStepNeeded=false. Sessions stay active for 4 hours between steps and persist across restarts. Use get_research_session to recover a session after context loss.",
		Annotations:  readOnlyAnnotations(false, false),
		OutputSchema: sequentialSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input sequentialSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.SearchStep == "" {
			return toolError("searchStep is required"), nil, nil
		}

		// A new session is created only on step 1. Steps 2+ MUST carry the
		// sessionId returned by step 1; a missing sessionId there is almost
		// always a caller that lost its session mid-research (e.g. after context
		// loss). Silently forking a fresh session would orphan the real research
		// trail, so guide the caller to recover or restart instead.
		if input.SessionID == "" && input.StepNumber > 1 {
			return toolError("missing sessionId for stepNumber > 1. Pass the sessionId returned by step 1, recover it with get_research_session, or start a new session by setting stepNumber=1."), nil, nil
		}

		tenantID := auth.TenantIDFromContext(ctx)
		userID := auth.UserIDFromContext(ctx)

		var idx *session.SessionIndex
		var err error

		if input.SessionID == "" || input.StepNumber == 1 {
			idx, err = deps.Sessions.Create(tenantID, userID)
			if err != nil {
				return toolError(fmt.Sprintf("failed to create session: %v", err)), nil, nil
			}

			goal := input.ResearchGoal
			if goal == "" && len(input.SearchStep) > 0 {
				goal = input.SearchStep
				if len(goal) > 200 {
					goal = goal[:200]
				}
			}
			if goal != "" {
				_ = deps.Sessions.SetResearchGoal(tenantID, userID, idx.ID, goal)
			}
		}

		sessionID := input.SessionID
		if idx != nil {
			sessionID = idx.ID
		}

		step := session.ResearchStep{
			StepNumber:         input.StepNumber,
			Description:        input.SearchStep,
			Reasoning:          input.Reasoning,
			Confidence:         input.Confidence,
			RejectedApproaches: input.RejectedApproaches,
			IsRevision:         input.IsRevision,
			RevisesStep:        input.RevisesStep,
			BranchID:           input.BranchID,
			Timestamp:          time.Now().Format(time.RFC3339),
		}

		var gap *session.KnowledgeGap
		if input.KnowledgeGap != "" {
			gap = &session.KnowledgeGap{
				Description: input.KnowledgeGap,
				FoundInStep: input.StepNumber,
			}
		}

		idx, err = deps.Sessions.AppendStep(tenantID, userID, sessionID, step, gap, input.SessionSummary)
		if err != nil {
			// Typed recovery: a not-found session (expired, evicted, or — in a
			// multi-pod HTTP deployment — held by a different instance) returns a
			// structured session_not_found error carrying the last known step, so
			// the client can decide to resume or restart deterministically.
			var notFound *session.SessionNotFoundError
			if errors.As(err, &notFound) {
				return sessionNotFoundError(notFound.LastKnownStep), nil, nil
			}
			if errors.Is(err, session.ErrSessionNotFound) || errors.Is(err, session.ErrSessionExpired) {
				return sessionNotFoundError(input.StepNumber - 1), nil, nil
			}
			return toolError(fmt.Sprintf("Could not record this research step: %v. Start a new session by setting stepNumber=1 without a sessionId.", err)), nil, nil
		}

		output := buildSequentialResponse(idx, input)

		// Iterative-depth assist (#67). quick (default) is a no-op — byte-for-byte
		// the prior behavior. standard/thorough add coverage analysis + refinement
		// suggestions; thorough also auto-runs the suggestions. Never synthesizes.
		applyDepth(ctx, deps, input, idx, output)

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("sequential_search", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "sequential_search", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}

func buildSequentialResponse(idx *session.SessionIndex, input sequentialSearchInput) map[string]any {
	mode := input.ResponseMode
	if mode == "" {
		if idx.StepCount <= 8 {
			mode = "full"
		} else {
			mode = "summary"
		}
	}

	output := map[string]any{
		"sessionId":          idx.ID,
		"responseMode":       mode,
		"researchGoal":       idx.ResearchGoal,
		"currentStep":        input.StepNumber,
		"totalStepsEstimate": input.TotalStepsEstimate,
		"isComplete":         !input.NextStepNeeded,
		"startedAt":          idx.CreatedAt.Format(time.RFC3339),
		// The echoed `sources` carry external-origin titles/URLs; mark the
		// envelope untrusted so replayed source metadata isn't treated as
		// trusted. (Model-authored reasoning text is the host's own output.)
		"trust": untrustedContentTrust,
	}

	if idx.Warning != "" {
		output["warning"] = idx.Warning
		output["isComplete"] = true
	}

	if !input.NextStepNeeded {
		output["completedAt"] = time.Now().Format(time.RFC3339)
	}

	switch mode {
	case "full":
		output["steps"] = idx.StepIndex
		output["lastSteps"] = idx.LastSteps
		output["gaps"] = idx.ActiveGaps
		output["sources"] = idx.Sources
	case "summary":
		output["summary"] = idx.Summary
		output["stepIndex"] = idx.StepIndex
		output["lastSteps"] = idx.LastSteps
		output["gaps"] = idx.ActiveGaps
		output["sources"] = idx.Sources
	}

	return output
}

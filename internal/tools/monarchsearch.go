package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// monarch_search (#318) is a structured-domain tool over the Monarch Initiative
// biomedical knowledge graph: one tool, five operations (semsim, entity,
// associations, compare, annotate) selected by the required `operation` field.
// Keyless, so AvailableMonarchProviders always builds it and the tool is always
// registered. Read-only, openWorld; output carries the untrusted-content trust
// marker. The annotate operation forwards free text to a public third-party API
// with no BAA — callers must not submit identifiable patient data.

type monarchSearchInput struct {
	Operation     string   `json:"operation" jsonschema:"Required. One of: semsim (phenotype-to-disease/gene similarity search), entity (look up an entity by free text or by ID), associations (traverse typed knowledge-graph edges), compare (compare two phenotype profiles directly), annotate (ground a short clinical text to HPO terms). Do not submit identifiable patient data in the annotate text."`
	Phenotypes    []string `json:"phenotypes,omitempty" jsonschema:"semsim/compare: list of HPO term IDs, e.g. [\"HP:0001166\",\"HP:0001083\"]. Maximum 20 terms per query."`
	Group         string   `json:"group,omitempty" jsonschema:"semsim: termset group to search against. One of: Human Genes, Mouse Genes, Rat Genes, Zebrafish Genes, C. Elegans Genes, Human Diseases. Defaults to Human Diseases."`
	CompareTo     []string `json:"compareTo,omitempty" jsonschema:"compare: the second list of HPO term IDs to compare the phenotypes list against."`
	Query         string   `json:"query,omitempty" jsonschema:"entity: free-text search term, e.g. \"Marfan syndrome\"."`
	EntityID      string   `json:"entityId,omitempty" jsonschema:"entity/associations: an entity CURIE, e.g. MONDO:0007947, HGNC:3603, HP:0001166. Must match ^[A-Za-z0-9._-]+:[A-Za-z0-9._-]+$."`
	AssocSubject  string   `json:"assocSubject,omitempty" jsonschema:"associations: subject-side entity CURIE to filter edges by."`
	AssocObject   string   `json:"assocObject,omitempty" jsonschema:"associations: object-side entity CURIE to filter edges by."`
	AssocCategory string   `json:"category,omitempty" jsonschema:"associations: Biolink association category enum, e.g. biolink:CausalGeneToDiseaseAssociation. Maps to the API 'category' query parameter."`
	Text          string   `json:"text,omitempty" jsonschema:"annotate: short clinical text to ground to HPO terms. Hard limit 2000 characters. Never include patient-identifiable data."`
	NumResults    int      `json:"numResults,omitempty" jsonschema:"Maximum results to return. Default 20, max 200 (the API caps association pages at 200)."`
	Provider      string   `json:"provider,omitempty" jsonschema:"Force a specific Monarch provider: monarch. Errors if not configured."`
	SessionID     string   `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded for recovery after context loss."`
}

const monarchMaxPhenotypes = 20
const monarchMaxTextLen = 2000

func registerMonarchSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "monarch_search",
		Description:  "Query the Monarch Initiative biomedical knowledge graph: rank diseases and genes by phenotype similarity (semsim), look up disease/gene/phenotype entities, and traverse gene-disease-phenotype associations. For published literature on a condition combine with academic_search; for active interventional trials use clinical_search. Do not submit identifiable patient data in the annotate operation.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: monarchSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input monarchSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		params, errResult := buildMonarchParams(input)
		if errResult != nil {
			return errResult, nil, nil
		}

		searcher, providerName, errResult := resolveMonarchSearcher(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}
		if searcher == nil {
			return unconfiguredProviderError("monarch_search", search.SupportedMonarchProviders), nil, nil
		}

		cacheKey := searchCacheKey("monarch", params.Operation, params.Group,
			strings.Join(params.Phenotypes, ","), strings.Join(params.CompareTo, ","),
			params.Query, params.EntityID, params.AssocSubject, params.AssocObject,
			params.AssocCategory, params.Text, params.NumResults, providerName)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			recordToolCall(deps, "monarch_search", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "monarch_search", time.Since(start), nil, "")
			return cachedResultWithMeta(cached, meta), nil, nil
		}

		results, err := searcher.Search(ctx, params)
		if err != nil {
			errCode := "upstream_error"
			if isRateLimitError(err) {
				errCode = "rate_limited"
			}
			recordToolCall(deps, "monarch_search", time.Since(start), err, errCode, false)
			auditToolCallQuery(ctx, deps, "monarch_search", time.Since(start), err, errCode, monarchQueryForAudit(input), map[string]any{"provider": providerName, "operation": params.Operation})
			return upstreamErrorResponse("monarch knowledge graph search", err), nil, nil
		}

		items := make([]map[string]any, 0, len(results))
		for _, r := range results {
			if params.Operation == "annotate" && r.Text != "" {
				r.Text = deps.Content.SanitizeText(r.Text)
			}
			items = append(items, monarchResultToMap(r))
		}

		output := map[string]any{
			"operation":   params.Operation,
			"resultCount": len(items),
			"results":     items,
			"provider":    providerName,
			"trust":       untrustedContentTrust,
		}
		if len(items) == 0 {
			filters := map[string]string{"operation": params.Operation}
			if params.EntityID != "" {
				filters["entityId"] = params.EntityID
			}
			output["hints"] = buildZeroResultHints(providerName, filters, nil)
		}

		jsonBytes, _ := json.Marshal(output)
		if len(items) > 0 {
			deps.Cache.Set(ctx, cacheKey, jsonBytes, 6*time.Hour)
		}
		recordToolCall(deps, "monarch_search", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "monarch_search", time.Since(start), nil, "", monarchQueryForAudit(input), map[string]any{"provider": providerName, "operation": params.Operation})

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, monarchResultsToSources(results))
		}

		return structuredResult(jsonBytes), nil, nil
	})
}

// monarchQueryForAudit picks the most representative free-text field for the
// audit log's query-length tracking (gated by AUDIT_INCLUDE_REQUEST_BODY).
func monarchQueryForAudit(input monarchSearchInput) string {
	switch {
	case input.Text != "":
		return input.Text
	case input.Query != "":
		return input.Query
	case input.EntityID != "":
		return input.EntityID
	default:
		return strings.Join(input.Phenotypes, ",")
	}
}

// buildMonarchParams validates the operation discriminator and its per-operation
// required fields, converts monarchSearchInput to search.MonarchSearchParams, and
// enforces the CURIE path-injection guard and the phenotype/text caps before any
// HTTP call is made.
func buildMonarchParams(input monarchSearchInput) (search.MonarchSearchParams, *mcp.CallToolResult) {
	num := input.NumResults
	if num <= 0 {
		num = 20
	}
	if num > 200 {
		num = 200
	}

	params := search.MonarchSearchParams{
		Operation:     input.Operation,
		Phenotypes:    input.Phenotypes,
		Group:         input.Group,
		CompareTo:     input.CompareTo,
		Query:         input.Query,
		EntityID:      input.EntityID,
		AssocSubject:  input.AssocSubject,
		AssocObject:   input.AssocObject,
		AssocCategory: input.AssocCategory,
		Text:          input.Text,
		NumResults:    num,
	}

	switch input.Operation {
	case "semsim":
		if len(input.Phenotypes) == 0 {
			return params, toolError("semsim requires at least one HPO term in phenotypes")
		}
		if len(input.Phenotypes) > monarchMaxPhenotypes {
			return params, toolError("phenotypes: maximum 20 HPO terms per query")
		}
		if input.Group != "" && !search.ValidSemsimGroup(input.Group) {
			return params, toolError(fmt.Sprintf("unknown group %q; must be one of: Human Genes, Mouse Genes, Rat Genes, Zebrafish Genes, C. Elegans Genes, Human Diseases", input.Group))
		}
	case "entity":
		if input.Query == "" && input.EntityID == "" {
			return params, toolError("entity requires either query or entityId")
		}
		if input.EntityID != "" && !search.ValidCURIE(input.EntityID) {
			return params, toolError("entityId must be a valid CURIE, e.g. MONDO:0007947")
		}
	case "associations":
		if input.EntityID == "" && input.AssocSubject == "" && input.AssocObject == "" {
			return params, toolError("associations requires one of entityId, assocSubject, or assocObject")
		}
		if input.EntityID != "" && !search.ValidCURIE(input.EntityID) {
			return params, toolError("entityId must be a valid CURIE, e.g. MONDO:0007947")
		}
	case "compare":
		if len(input.Phenotypes) == 0 || len(input.CompareTo) == 0 {
			return params, toolError("compare requires both phenotypes and compareTo")
		}
		if len(input.Phenotypes) > monarchMaxPhenotypes || len(input.CompareTo) > monarchMaxPhenotypes {
			return params, toolError("phenotypes: maximum 20 HPO terms per query")
		}
	case "annotate":
		if input.Text == "" {
			return params, toolError("annotate requires text")
		}
		if len([]rune(input.Text)) > monarchMaxTextLen {
			return params, toolError("text: maximum 2000 characters")
		}
	case "":
		return params, toolError("operation is required: one of semsim, entity, associations, compare, annotate")
	default:
		return params, toolError(fmt.Sprintf("unknown operation %q; must be one of: semsim, entity, associations, compare, annotate", input.Operation))
	}

	return params, nil
}

// resolveMonarchSearcher selects a MonarchProvider. Returns (nil, "", nil) when no
// provider is configured; a structured error for an unknown/unconfigured name.
func resolveMonarchSearcher(deps Dependencies, providerName string) (search.MonarchSearcher, string, *mcp.CallToolResult) {
	if providerName != "" {
		if p, ok := deps.MonarchProviders[providerName]; ok {
			return p, providerName, nil
		}
		for _, n := range search.SupportedMonarchProviders {
			if n == providerName {
				return nil, "", structuredError(
					fmt.Sprintf("Monarch provider %q is not configured.", providerName),
					ToolError{Kind: ErrKindConfig, Retryable: false, SuggestedAction: ActionCheckAPIKey, Provider: providerName})
			}
		}
		return nil, "", structuredError(
			fmt.Sprintf("Unknown Monarch provider %q. Supported: %v.", providerName, search.SupportedMonarchProviders),
			ToolError{Kind: ErrKindConfig, Retryable: false, SuggestedAction: ActionTryDifferentProvider, Alternatives: search.SupportedMonarchProviders})
	}
	for _, name := range search.SupportedMonarchProviders {
		if p, ok := deps.MonarchProviders[name]; ok {
			return p, name, nil
		}
	}
	return nil, "", nil
}

func monarchResultToMap(r search.MonarchResult) map[string]any {
	m := map[string]any{"source": r.Source}
	if r.ID != "" {
		m["id"] = r.ID
	}
	if r.Label != "" {
		m["label"] = r.Label
	}
	if r.Category != "" {
		m["category"] = r.Category
	}
	if r.Score != 0 {
		m["score"] = r.Score
	}
	if r.AncestorID != "" {
		m["ancestorId"] = r.AncestorID
	}
	if r.AncestorLabel != "" {
		m["ancestorLabel"] = r.AncestorLabel
	}
	if r.Description != "" {
		m["description"] = r.Description
	}
	if len(r.CrossReferences) > 0 {
		m["crossReferences"] = r.CrossReferences
	}
	if r.SubjectID != "" {
		m["subjectId"] = r.SubjectID
	}
	if r.SubjectLabel != "" {
		m["subjectLabel"] = r.SubjectLabel
	}
	if r.ObjectID != "" {
		m["objectId"] = r.ObjectID
	}
	if r.ObjectLabel != "" {
		m["objectLabel"] = r.ObjectLabel
	}
	if r.PrimaryKnowledgeSource != "" {
		m["primaryKnowledgeSource"] = r.PrimaryKnowledgeSource
	}
	if r.Text != "" {
		m["text"] = r.Text
	}
	return m
}

func monarchResultsToSources(results []search.MonarchResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
		if r.ID == "" {
			continue
		}
		sources = append(sources, session.ResearchSource{URL: "https://monarchinitiative.org/" + r.ID, Title: r.Label, Relevance: "biomedical knowledge graph entity"})
	}
	return sources
}

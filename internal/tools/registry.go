package tools

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/memory"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
	"github.com/zoharbabin/web-researcher-mcp/internal/useranalytics"
	"github.com/zoharbabin/web-researcher-mcp/internal/workspace"
)

type Dependencies struct {
	Cache             cache.Cache
	Search            search.Provider
	SearchProviders   map[string]search.Provider
	PatentProviders   map[string]search.PatentProvider
	AcademicProviders map[string]search.AcademicProvider
	// Structured-domain providers (v1.19.0). Each map is empty unless the
	// provider is configured, in which case its tool registers. FilingProviders
	// (SEC EDGAR), CaseProviders (CourtListener), EconProviders (FRED).
	FilingProviders map[string]search.FilingProvider
	CaseProviders   map[string]search.CaseProvider
	EconProviders   map[string]search.EconProvider
	// AnswerProviders / StructuredProviders back the provider-independent
	// `answer` and `structured_search` tools. Any provider implementing the
	// capability appears here (Exa today). Empty ⇒ the tool is not registered.
	AnswerProviders     map[string]search.AnswerProvider
	StructuredProviders map[string]search.StructuredProvider
	// OAResolver enriches DOI-bearing academic_search results with open-access
	// PDF links (Unpaywall, #45). nil ⇒ enrichment is skipped (no-op). Best-effort:
	// never fails a search.
	OAResolver search.OAResolver
	// RetractionResolver flags DOI-bearing academic/citation results with their
	// Crossref integrity status (#156). nil ⇒ skipped (no-op). Best-effort:
	// never fails a search. Also powers verify_citation's retraction check.
	RetractionResolver search.RetractionResolver
	// LinkVerifier checks source-URL liveness + Wayback archive fallback for the
	// opt-in verify_links flag (#157) and verify_citation. nil ⇒ verification is
	// skipped (no-op). Best-effort + bounded; never fails a tool call.
	LinkVerifier *scraper.LinkVerifier
	Scraper      *scraper.Pipeline
	Content      *content.Processor
	Sessions     session.Manager
	Metrics      *metrics.Collector
	Auditor      audit.Auditor
	Logger       *slog.Logger
	Features     Features
	// Consent records/verifies/honors consent for regulated features (#89).
	// Defaults to a Noop (grants nothing) when unset, so guarded processing is a
	// clean no-op until a regulated feature wires it in.
	Consent consent.Manager
	// UserAnalytics records consent-gated per-user usage (#92). Defaults to a
	// Noop (collects nothing). The get_my_analytics tool is registered only when
	// a non-Noop recorder is present.
	UserAnalytics useranalytics.Recorder
	// Memory is the consent-gated long-term cross-session memory store (#88).
	// Defaults to a Noop. The memory_save/memory_recall tools are registered
	// only when a non-Noop store is present.
	Memory memory.Store
	// Workspaces is the opt-in shared-workspace data plane (#96). Defaults to a
	// Noop (no membership, no data). The workspace_contribute/workspace_read
	// tools are registered only when a non-Noop store is present.
	Workspaces workspace.Store
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
	registerResearchExport(srv, deps)
	registerFormatBibliography(srv, deps)
	// verify_citation (#158) — always registered: it degrades gracefully when a
	// resolver is absent (the retraction resolver + link verifier are always
	// constructed; the academic match is best-effort).
	registerVerifyCitation(srv, deps)

	// citation_graph (#47) — registered only when a citation-capable academic
	// provider (semanticscholar or openalex) is configured.
	if hasCitationProvider(deps) {
		registerCitationGraph(srv, deps)
	}

	// Structured-domain tools (v1.19.0) — each registers only when its provider
	// map is non-empty. filing_search needs EDGAR_CONTACT_EMAIL (or OPENALEX_EMAIL)
	// and econ_search needs FRED_API_KEY, so both stay off by default. legal_search
	// is the exception: CourtListener works keyless, so AvailableCaseProviders
	// always builds it and legal_search is always registered.
	if len(deps.FilingProviders) > 0 {
		registerFilingSearch(srv, deps)
	}
	if len(deps.CaseProviders) > 0 {
		registerLegalSearch(srv, deps)
	}
	if len(deps.EconProviders) > 0 {
		registerEconSearch(srv, deps)
	}

	// Synthesis tools — provider-independent (like academic/patent search).
	// Each registers only when at least one provider offers the capability, so
	// the default tool surface is unchanged until such a provider is configured.
	if len(deps.AnswerProviders) > 0 {
		registerAnswer(srv, deps)
	}
	if len(deps.StructuredProviders) > 0 {
		registerStructuredSearch(srv, deps)
	}

	// Regulated, opt-in tools — registered only when their feature is wired in
	// (a non-Noop dependency present), so the default tool surface is unchanged.
	if _, isNoop := deps.UserAnalytics.(*useranalytics.Noop); deps.UserAnalytics != nil && !isNoop {
		registerGetMyAnalytics(srv, deps)
	}
	if _, isNoop := deps.Memory.(*memory.Noop); deps.Memory != nil && !isNoop {
		registerMemorySave(srv, deps)
		registerMemoryRecall(srv, deps)
	}
	if _, isNoop := deps.Workspaces.(*workspace.Noop); deps.Workspaces != nil && !isNoop {
		registerWorkspaceContribute(srv, deps)
		registerWorkspaceRead(srv, deps)
	}
}

// hasCitationProvider reports whether any configured academic provider supports
// citation-graph traversal (the CitationSearcher capability) — gates the
// citation_graph tool's registration.
func hasCitationProvider(deps Dependencies) bool {
	for _, name := range []string{"semanticscholar", "openalex"} {
		if ap, ok := deps.AcademicProviders[name]; ok {
			if _, ok := ap.(search.CitationSearcher); ok {
				return true
			}
		}
	}
	return false
}

func boolPtr(b bool) *bool { return &b }

// writeAnnotations is for the rare tool that MUTATES server-side state (e.g.
// memory_save). ReadOnlyHint is false (it writes), but DestructiveHint is also
// false: it appends/updates, never deletes (deletion is the separate #85
// erasure endpoint, never a flag on a tool). Not open-world (local state).
func writeAnnotations(idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: boolPtr(false),
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolPtr(false),
	}
}

func readOnlyAnnotations(idempotent bool, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: boolPtr(false),
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolPtr(openWorld),
	}
}

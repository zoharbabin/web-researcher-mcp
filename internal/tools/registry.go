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
	// TrialProviders back clinical_search (ClinicalTrials.gov, #165). Keyless, so
	// AvailableTrialProviders always builds it and clinical_search is always
	// registered. Empty ⇒ the tool is not registered.
	TrialProviders map[string]search.TrialProvider
	// AwesomeListProviders back awesome_list_search (ecosyste.ms, #375). Keyless,
	// so AvailableAwesomeListProviders always builds it and awesome_list_search
	// is always registered. Empty ⇒ the tool is not registered.
	AwesomeListProviders map[string]search.AwesomeListProvider
	// MonarchProviders back monarch_search (Monarch Initiative biomedical
	// knowledge graph, #318). Keyless, so AvailableMonarchProviders always builds
	// it and monarch_search is always registered. Empty ⇒ the tool is not registered.
	MonarchProviders map[string]search.MonarchProvider
	// LocalProviders back local_search (#259). Brave is the sole provider today;
	// requires BRAVE_API_KEY. Empty ⇒ the tool is not registered.
	LocalProviders map[string]search.LocalProvider
	// ContextProviders back the Brave LLM Context endpoint (#257). When the
	// resolved search provider for a search_and_scrape call implements
	// ContextSearcher, the tool attempts server-side context assembly first (a
	// single API call instead of N page scrapes), falling through to normal
	// scraping on failure or empty result. Requires a Brave Data for AI plan.
	// Empty ⇒ the ContextSearcher path is never attempted.
	ContextProviders map[string]search.ContextProvider
	// OAResolver enriches DOI-bearing academic_search results with open-access
	// PDF links (Unpaywall, #45). nil ⇒ enrichment is skipped (no-op). Best-effort:
	// never fails a search.
	OAResolver search.OAResolver
	// RetractionResolver flags DOI-bearing academic/citation results with their
	// Crossref integrity status (#156). nil ⇒ skipped (no-op). Best-effort:
	// never fails a search. Also powers verify_citation's retraction check.
	RetractionResolver search.RetractionResolver
	// DOIRegistry is the authoritative cross-registrar DOI existence check (#226):
	// the doi.org handle API confirms a DOI is registered with ANY agency, so a
	// real arXiv/DataCite DOI (which Crossref and OpenAlex don't index) still reads
	// as existing while a fabricated DOI reads as not-found. nil ⇒ skipped (no-op).
	// Best-effort: a transport failure leaves existence unknown, never asserts it.
	DOIRegistry search.DOIRegistry
	// WikidataOwnershipResolver enriches self-promotion detection with corporate
	// ownership: when lexical matching fails, a Wikidata P749 lookup checks whether
	// the brand's corporate parent is distinct from the recommending domain (#248).
	// nil → ownership check skipped (no-op). Best-effort: a lookup failure leaves
	// the signal absent, never fails the audit.
	WikidataOwnershipResolver search.OwnershipResolver
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
	// BrandFetchAPIKey enables Tier 1 BrandFetch Brand API + Context API
	// enrichment in brand_research (Bearer auth). It only fills fields the
	// always-on no-key tiers (homepage meta/structured-data + brand-page
	// probe + optional web search) didn't already find — empty → tool still
	// registers and runs those no-key tiers unconditionally.
	BrandFetchAPIKey string
	// BrandFetchClientID enables the company_name → domain resolution step in
	// brand_research via BrandFetch's Brand Search API, which authenticates with
	// a client ID query param rather than the Bearer-token BrandFetchAPIKey used
	// by the Brand API. Empty → that resolution step is skipped (falls back to
	// deps.Search.Web()).
	BrandFetchClientID string
	// OpenSyllabusAPIKey / OpenSyllabusAPIURL gate syllabus_search. Both must be
	// set (a research agreement with Open Syllabus is required) or the tool
	// does not register.
	OpenSyllabusAPIKey string
	OpenSyllabusAPIURL string
	// PENAmericaAirtableToken gates gag_order_search. Empty ⇒ tool not registered.
	PENAmericaAirtableToken string
	// CTLogResolver backs company_recon's Certificate Transparency phase
	// (crt.sh, #323). Keyless, so main.go always constructs it — non-nil in
	// production. A nil value in tests degrades that phase to a soft skip.
	CTLogResolver search.CTLogResolver
	// ArchiveResolver backs company_recon's Wayback CDX phase (#323). Keyless,
	// so main.go always constructs it — non-nil in production. A nil value in
	// tests degrades that phase to a soft skip.
	ArchiveResolver search.ArchiveResolver
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
	// resource_link backing store (#181): the read side of large-payload links.
	// Registered first so the research://artifact/{id} template exists before any
	// tool returns a link to it. No-op when no cache is configured.
	registerArtifactResource(srv, deps)
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
	// audit_bibliography — the corpus-level companion to verify_citation. Reads a
	// whole bibliography in (CSL-JSON/RIS/BibTeX, an explicit list, or a session)
	// and runs existence/retraction/dead-link over every entry. Composes the same
	// resolvers; always registered (degrades gracefully like verify_citation).
	registerAuditBibliography(srv, deps)
	// archive_source — the trust suite's only WRITE tool: captures a fresh
	// Internet Archive snapshot via Save Page Now so a cited source stays
	// verifiable. Always registered; degrades to status:"unavailable" when no link
	// verifier is configured.
	registerArchiveSource(srv, deps)
	// paper_fulltext (#269) — collapses academic_search + scrape_page into one
	// call for a DOI/paper-ID/URL. Always registered; degrades to a doi.org
	// redirect or the input URL verbatim when no Semantic Scholar provider is
	// configured.
	registerPaperFulltext(srv, deps)
	// verify_recommendation — audits AI recommendations (listicles, product
	// lists) for anti-sloptimization signals: self-promotion, conflicts of interest,
	// domain reputation, dead links. Always registered as part of the trust suite.
	registerVerifyRecommendation(srv, deps)

	// citation_graph (#47) — registered only when a citation-capable academic
	// provider (semanticscholar or openalex) is configured.
	if hasCitationProvider(deps) {
		registerCitationGraph(srv, deps)
	}

	// Structured-domain tools (v1.19.0) — each registers only when its provider
	// map is non-empty. filing_search needs EDGAR_CONTACT_EMAIL (or OPENALEX_EMAIL),
	// so it stays off by default. legal_search and econ_search both have a keyless
	// provider that AvailableCaseProviders / AvailableEconProviders always build —
	// CourtListener (case law) and World Bank (global indicators, #166) — so both
	// tools are always registered; FRED adds US macro series to econ_search when
	// FRED_API_KEY is set.
	if len(deps.FilingProviders) > 0 {
		registerFilingSearch(srv, deps)
	}
	if len(deps.CaseProviders) > 0 {
		registerLegalSearch(srv, deps)
	}
	if len(deps.EconProviders) > 0 {
		registerEconSearch(srv, deps)
	}
	// clinical_search (#165) — ClinicalTrials.gov is keyless, so
	// AvailableTrialProviders always builds it and the tool is always registered.
	if len(deps.TrialProviders) > 0 {
		registerClinicalSearch(srv, deps)
	}
	// awesome_list_search (#375) — ecosyste.ms is keyless, so
	// AvailableAwesomeListProviders always builds it and the tool is always
	// registered.
	if len(deps.AwesomeListProviders) > 0 {
		registerAwesomeListSearch(srv, deps)
	}
	// local_search (#259) — Brave Local Search API; requires BRAVE_API_KEY.
	if len(deps.LocalProviders) > 0 {
		registerLocal(srv, deps)
	}
	// monarch_search (#318) — Monarch Initiative API is keyless, so
	// AvailableMonarchProviders always builds it and the tool is always registered.
	if len(deps.MonarchProviders) > 0 {
		registerMonarchSearch(srv, deps)
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
	// brand_research — always registered; the homepage meta/structured-data
	// extraction + brand-page probe + optional web search tiers run
	// unconditionally without BRANDFETCH_API_KEY/BRANDFETCH_CLIENT_ID.
	registerBrandResearch(srv, deps)

	// syllabus_search (#352) — requires a research agreement with Open
	// Syllabus; registers only when both the key and base URL are set.
	if deps.OpenSyllabusAPIKey != "" && deps.OpenSyllabusAPIURL != "" {
		registerSyllabusSearch(srv, deps)
	}
	// gag_order_search (#352) — PEN America's educational gag order tracker
	// via Airtable; registers only when a token is set.
	if deps.PENAmericaAirtableToken != "" {
		registerGagOrderSearch(srv, deps)
	}
	// company_recon (#323) — always registered; every phase's data source
	// (crt.sh, Wayback CDX, homepage probing, web search) is keyless. Individual
	// phases soft-skip when their resolver dependency is nil (e.g. in a minimal
	// test harness), never failing the whole tool call.
	registerCompanyRecon(srv, deps)
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

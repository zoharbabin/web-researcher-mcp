package search

import (
	"context"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// ProviderMeta describes a domain provider's coverage and capabilities.
// Used internally for intelligent routing — not exposed to MCP clients.
type ProviderMeta struct {
	Regions      []string // ISO country codes (e.g. "US", "EP", "WO") or "*" for worldwide
	Capabilities []string // provider-specific tags: "search", "biblio", "fulltext", "citations", "family", "scholarly"
	RateClass    string   // "free", "metered", "unlimited"
	Description  string   // human-readable, used in error messages
}

// MatchesRegion returns true if this provider covers the given region.
// Empty region or "all" matches any provider. "*" in provider regions matches any query.
func (m ProviderMeta) MatchesRegion(region string) bool {
	if region == "" || strings.EqualFold(region, "all") {
		return true
	}
	for _, r := range m.Regions {
		if r == "*" || strings.EqualFold(r, region) {
			return true
		}
	}
	return false
}

// HasCapability returns true if the provider supports the given capability tag.
func (m ProviderMeta) HasCapability(cap string) bool {
	for _, c := range m.Capabilities {
		if strings.EqualFold(c, cap) {
			return true
		}
	}
	return false
}

// PatentProvider is a specialized provider for patent search.
// Unlike Provider, it does not support Web/Images/News — only structured patent queries.
type PatentProvider interface {
	PatentSearcher
	Name() string
	Metadata() ProviderMeta
}

// SupportedPatentProviders lists all patent-specific provider names.
var SupportedPatentProviders = []string{"searchapi", "epo", "lens", "uspto"}

// NewPatentProviderByName creates a patent provider by name if credentials are configured.
func NewPatentProviderByName(name string, cfg PatentProviderConfig, deps Deps) PatentProvider {
	switch name {
	case "uspto":
		if cfg.USPTOAPIKey != "" {
			return NewUSPTOProvider(cfg.USPTOAPIKey, deps)
		}
	case "epo":
		if cfg.EPOConsumerKey != "" && cfg.EPOConsumerSecret != "" {
			return NewEPOProvider(cfg.EPOConsumerKey, cfg.EPOConsumerSecret, deps)
		}
	case "lens":
		if cfg.LensAPIToken != "" {
			return NewLensProvider(cfg.LensAPIToken, deps)
		}
	case "searchapi":
		if cfg.SearchAPIKey != "" {
			return &searchAPIPatentAdapter{provider: NewSearchAPIProvider(cfg.SearchAPIKey, deps)}
		}
	}
	return nil
}

// searchAPIPatentAdapter wraps SearchAPIProvider to satisfy the PatentProvider interface.
type searchAPIPatentAdapter struct {
	provider *SearchAPIProvider
}

func (a *searchAPIPatentAdapter) Name() string { return "searchapi" }

func (a *searchAPIPatentAdapter) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biblio"},
		RateClass:    "metered",
		Description:  "SearchAPI — Google Patents via SerpAPI (structured results)",
	}
}

func (a *searchAPIPatentAdapter) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	return a.provider.Patents(ctx, params)
}

// PatentProviderConfig holds credentials for patent-specific providers.
type PatentProviderConfig struct {
	USPTOAPIKey       string
	EPOConsumerKey    string
	EPOConsumerSecret string
	LensAPIToken      string
	SearchAPIKey      string
}

// AcademicSearcher is an optional interface for structured academic paper search.
type AcademicSearcher interface {
	Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error)
}

// AcademicProvider is a specialized provider for academic/scholarly search.
type AcademicProvider interface {
	AcademicSearcher
	Name() string
	Metadata() ProviderMeta
}

// DOIResolver is the optional capability of fetching the EXACT work for a DOI via
// a direct entity lookup (e.g. OpenAlex /works/doi:{doi}) rather than a fuzzy
// full-text search. verify_citation uses it so its matchedRecord is always the
// cited work or nothing — a relevance-ranked DOI search returns near-neighbors
// whose top hit is a different paper, which must never be shown as this DOI's
// record. Returns (nil, nil) when the DOI has no record (not an error).
type DOIResolver interface {
	ResolveByDOI(ctx context.Context, doi string) (*AcademicResult, error)
}

// CitationSearcher is the optional capability of traversing a paper's citation
// graph — works that cite it (forward) and works it cites (backward). Backs the
// citation_graph tool (#47). Semantic Scholar enriches edges with intent +
// influence; OpenAlex provides counts-only edges as a fallback. seedID is a DOI
// or a provider-native work ID. Both methods return single-hop neighborhoods
// bounded by numResults (no recursive traversal — that's the caller's to
// orchestrate).
type CitationSearcher interface {
	Citations(ctx context.Context, seedID string, numResults int) ([]AcademicResult, error)
	References(ctx context.Context, seedID string, numResults int) ([]AcademicResult, error)
	// SupportsInfluenceSignal reports whether this provider populates
	// AcademicResult.IsInfluential on the edges it returns (Semantic Scholar
	// does; OpenAlex — counts-only edges — does not, #655). citation_graph's
	// influential_only filter must no-op (pass results through unfiltered)
	// when this is false, rather than discard every result on the mistaken
	// assumption that an unset IsInfluential means "not influential."
	SupportsInfluenceSignal() bool
}

// PaperFetcher fetches full paper metadata by DOI or Semantic Scholar paper ID.
// The returned AcademicResult always includes PDFUrl when an open-access PDF is
// known. Backs the paper_fulltext tool (#269). Returns (nil, nil) when the
// identifier has no record (not an error).
type PaperFetcher interface {
	FetchPaper(ctx context.Context, id string) (*AcademicResult, error)
}

// FullTextFetcher is an optional capability for providers that return the full
// plain-text body of a paper by provider-internal ID (e.g. ScholarAPI's
// /text/{id}, #266). Type-assert at the tool layer when full-text content is
// needed without scraping the publisher page. FetchText returns ("", nil) when
// the ID has no record (not an error); FetchTexts omits IDs with no text from
// the returned map rather than failing the whole batch.
type FullTextFetcher interface {
	FetchText(ctx context.Context, id string) (string, error)
	FetchTexts(ctx context.Context, ids []string) (map[string]string, error)
}

// AcademicSearchParams defines parameters for scholarly paper search.
type AcademicSearchParams struct {
	Query      string
	YearFrom   int
	YearTo     int
	Source     string // hint: "arxiv", "pubmed" — provider-specific filtering
	NumResults int
	OpenAccess bool
	SortBy     string // "relevance" (default) or "date"
	// FullText requests provider-side full-text enrichment when supported (e.g.
	// PubMed PMC efetch). Best-effort: providers that don't support it ignore it.
	FullText bool
}

// AcademicResult represents a scholarly paper from an academic search provider.
type AcademicResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	DOI           string   `json:"doi,omitempty"`
	Authors       []string `json:"authors,omitempty"`
	Journal       string   `json:"journal,omitempty"`
	Year          int      `json:"year,omitempty"`
	Abstract      string   `json:"abstract,omitempty"`
	CitationCount int      `json:"citationCount,omitempty"`
	Source        string   `json:"source"`
	OpenAccess    bool     `json:"openAccess,omitempty"`
	PDFUrl        string   `json:"pdfUrl,omitempty"`
	// TLDR is an AI-generated one-sentence summary (Semantic Scholar). Attributed
	// as AI-generated in tool output. Empty when the provider doesn't supply one.
	TLDR string `json:"tldr,omitempty"`
	// IsInfluential / CitationIntents annotate a result when it is a citation edge
	// (citation_graph tool, #47) and the edge provider supplies the signal
	// (Semantic Scholar). Omitted for plain search results.
	IsInfluential   bool     `json:"isInfluential,omitempty"`
	CitationIntents []string `json:"citationIntents,omitempty"` // e.g. background|methodology|result
	// Retraction is the integrity status for a DOI-bearing result (#156), filled
	// best-effort by EnrichRetraction from Crossref's merged Retraction Watch +
	// publisher data. nil/omitted when clean or unresolved — never a guess.
	Retraction *RetractionStatus `json:"retractionStatus,omitempty"`
	// IsInDoaj is true when OpenAlex reports the publishing journal is listed
	// in the Directory of Open Access Journals (DOAJ) — a peer-reviewed OA
	// quality signal. OpenAlex-only; omitted for all other providers.
	IsInDoaj bool `json:"isInDoaj,omitempty"`
	// FullText is the extracted full text of a PMC open-access article, populated
	// only when AcademicSearchParams.FullText is true and a PMCID is available.
	// Empty when the provider does not support full-text or the article is not in PMC.
	FullText string `json:"fullText,omitempty"`
	// HasText indicates the provider can supply the full plain-text body via
	// FullTextFetcher (#266). ScholarAPI-only signal — omitted for every other
	// provider, which never populate it.
	HasText bool `json:"hasText,omitempty"`
	// HasPDF indicates the provider has a raw PDF available for this result.
	// ScholarAPI-only signal — not a URL, just an availability flag; fetching
	// the PDF itself is out of scope (no provider method for it).
	HasPDF bool `json:"hasPdf,omitempty"`
	// LowConfidenceDomain is a defense-in-depth signal (#509) for upstream index
	// noise — a title-matching record OpenAlex (or another provider) returns
	// from a host outside the recognized publisher/preprint-server allowlist
	// (content.IsAcademicHost), combined with a citation count implausibly low
	// for how well-known the title sounds. Set by academic_search at the tool
	// layer (search providers never set it themselves), never by a provider
	// package. False/omitted is the default and carries no signal either way —
	// it does NOT mean "verified genuine," only "didn't trip this heuristic."
	LowConfidenceDomain bool `json:"lowConfidenceDomain,omitempty"`
}

// RetractionStatus is the operator/model-facing integrity signal for a scholarly
// DOI (#156). It is evidence, not a verdict: it reports what Crossref records and
// the model decides how to hedge. Omitted entirely when an item is clean.
type RetractionStatus struct {
	// Retracted is true for a formal retraction/withdrawal/removal. An
	// expression_of_concern is NOT a retraction (Retracted stays false; Kind
	// carries the nuance); corrections are likewise not retractions.
	Retracted bool `json:"retracted"`
	// Kind is the coarse integrity category: retraction | expression_of_concern |
	// correction. (withdrawal/removal/partial_retraction map to "retraction".)
	Kind string `json:"kind"`
	// Date is the notice date (YYYY-MM-DD) when Crossref supplies one.
	Date string `json:"date,omitempty"`
	// NoticeDOI is the DOI of the retraction/correction notice (where to read why).
	NoticeDOI string `json:"noticeDoi,omitempty"`
	// Source is the provenance: "retraction-watch" or "publisher".
	Source string `json:"source,omitempty"`
}

// Integrity-kind constants — the closed vocabulary callers switch on.
const (
	RetractionKindRetraction = "retraction"
	RetractionKindConcern    = "expression_of_concern"
	RetractionKindCorrection = "correction"
)

// AcademicProviderConfig holds credentials for academic-specific providers.
type AcademicProviderConfig struct {
	OpenAlexEmail         string
	CrossRefEmail         string
	ExaAPIKey             string // Exa (neural) — academic via the research-paper category
	SemanticScholarAPIKey string // Semantic Scholar — optional; works keyless at a lower shared rate
	PubMedAPIKey          string // PubMed E-utilities — optional; keyless by default, a key raises the rate
	PubMedEmail           string // PubMed — optional NCBI contact (tool/email params), recommended not required
	COREAPIKey            string // CORE.ac.uk — optional; keyless by default at a lower shared rate, a key raises the rate
	// ScholarAPIKey — scholarapi.net, a paid full-text academic search API
	// (#266). Deliberately excluded from SupportedAcademicProviders (see below);
	// reachable only via explicit provider=scholarapi.
	ScholarAPIKey string
}

// SupportedAcademicProviders lists all academic provider names eligible for
// auto-routing (the Router's fallback ladder and academic_search's Strategy 3
// direct-iteration). openalex and crossref are authoritative bibliographic
// databases; pubmed is the biomedical authority (NCBI E-utilities, keyless);
// semanticscholar adds AI-enrichment (TLDR, citation intent/influence); core is
// the largest OA full-text aggregator (keyless); exa is a neural-web alternate
// (research-paper category) — listed last so it sorts after them when no
// explicit routing is configured.
//
// scholarapi is deliberately NOT listed here (#266): it is a 10-credit-per-call
// metered API, and auto-routing would burn credits on every fallback pass. It
// is constructed by AvailableAcademicProviders below (so provider=scholarapi
// resolves) but never appears in this slice, so it is never tried automatically.
var SupportedAcademicProviders = []string{"openalex", "crossref", "pubmed", "semanticscholar", "core", "exa"}

// NewAcademicProviderByName creates an academic provider by name if configured.
// Semantic Scholar is constructed even without an API key (it works at a lower
// shared public rate); the key, when present, raises that limit.
func NewAcademicProviderByName(name string, cfg AcademicProviderConfig, deps Deps) AcademicProvider {
	switch name {
	case "openalex":
		if cfg.OpenAlexEmail != "" {
			return NewOpenAlexProvider(cfg.OpenAlexEmail, deps)
		}
	case "crossref":
		if cfg.CrossRefEmail != "" {
			return NewCrossRefProvider(cfg.CrossRefEmail, deps)
		}
	case "pubmed":
		// Keyless by default (NCBI allows ~3 req/s without a key); a key raises it.
		return NewPubMedProvider(cfg.PubMedAPIKey, cfg.PubMedEmail, deps)
	case "semanticscholar":
		return NewSemanticScholarProvider(cfg.SemanticScholarAPIKey, deps)
	case "core":
		// Keyless by default at a lower shared rate; a key raises it.
		return NewCOREProvider(cfg.COREAPIKey, deps)
	case "exa":
		if cfg.ExaAPIKey != "" {
			return NewExaProvider(cfg.ExaAPIKey, deps)
		}
	case "scholarapi":
		if cfg.ScholarAPIKey != "" {
			return NewScholarAPIProvider(cfg.ScholarAPIKey, deps)
		}
	}
	return nil
}

// AcademicProvidersExplicitOnly lists provider names that AvailableAcademicProviders
// constructs (so they land in the returned map and are reachable via a direct
// provider=<name> request) but that SupportedAcademicProviders excludes from
// auto-routing. scholarapi is the only member today (#266).
var AcademicProvidersExplicitOnly = []string{"scholarapi"}

// AvailableAcademicProviders returns all academic providers that can be
// constructed: every auto-routed name in SupportedAcademicProviders, plus every
// explicit-only name in AcademicProvidersExplicitOnly. Explicit-only providers
// still land in the map (so a direct provider=<name> lookup finds them and a
// Router built from this map can serve them via AcademicProviderByName); they
// are simply never selected by the Router's or academic_search's automatic
// fallback ladders, which iterate SupportedAcademicProviders only.
func AvailableAcademicProviders(cfg AcademicProviderConfig, deps Deps) map[string]AcademicProvider {
	providers := make(map[string]AcademicProvider)
	names := append(append([]string{}, SupportedAcademicProviders...), AcademicProvidersExplicitOnly...)
	for _, name := range names {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(deps.Circuit),
		}
		if p := NewAcademicProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

// AvailablePatentProviders returns all patent providers that can be constructed from config.
// Each provider gets its own circuit breaker for isolation — a failure in one provider
// does not block fallback to another.
func AvailablePatentProviders(cfg PatentProviderConfig, deps Deps) map[string]PatentProvider {
	providers := make(map[string]PatentProvider)
	for _, name := range SupportedPatentProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(deps.Circuit),
		}
		if p := NewPatentProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

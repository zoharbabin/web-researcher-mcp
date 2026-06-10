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
}

// SupportedAcademicProviders lists all academic provider names. openalex and
// crossref are authoritative bibliographic databases; pubmed is the biomedical
// authority (NCBI E-utilities, keyless); semanticscholar adds AI-enrichment
// (TLDR, citation intent/influence); exa is a neural-web alternate (research-paper
// category) — listed last so it sorts after them when no explicit routing is
// configured.
var SupportedAcademicProviders = []string{"openalex", "crossref", "pubmed", "semanticscholar", "exa"}

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
	case "exa":
		if cfg.ExaAPIKey != "" {
			return NewExaProvider(cfg.ExaAPIKey, deps)
		}
	}
	return nil
}

// AvailableAcademicProviders returns all academic providers that can be constructed.
func AvailableAcademicProviders(cfg AcademicProviderConfig, deps Deps) map[string]AcademicProvider {
	providers := make(map[string]AcademicProvider)
	for _, name := range SupportedAcademicProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
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
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewPatentProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

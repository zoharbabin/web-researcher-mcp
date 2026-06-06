package search

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// This file defines the three structured-domain research capabilities added in
// the Research Capability Expansion (v1.19.0): filings (SEC EDGAR), case law
// (CourtListener), and economic data (FRED). Each follows the exact shape of the
// existing PatentSearcher/AcademicSearcher capability pairs — a `…Searcher`
// (the method) + a `…Provider` (Searcher + Name + Metadata) — with a parallel
// Supported… list, New…ByName factory, and Available… constructor. The tool
// layer depends only on these interfaces, so providers stay swappable.

// ─────────────────────────── Filings (SEC EDGAR) ───────────────────────────

// FilingSearcher finds SEC filings. seed="" for full-text search; otherwise a
// company name/ticker/CIK to resolve.
type FilingSearcher interface {
	Filings(ctx context.Context, params FilingSearchParams) ([]FilingResult, error)
}

// FilingProvider is a named, described FilingSearcher.
type FilingProvider interface {
	FilingSearcher
	Name() string
	Metadata() ProviderMeta
}

// FilingSearchParams drives a filing search. Query is a company name, ticker, or
// CIK (or free-text when FormType is empty). Facts requests structured XBRL
// company facts instead of a filing list.
type FilingSearchParams struct {
	Query      string
	FormType   string // e.g. "10-K", "10-Q", "8-K"; "" = any
	Ticker     string // direct ticker override
	DateFrom   string // YYYY-MM-DD
	DateTo     string // YYYY-MM-DD
	Facts      bool   // return XBRL company facts (revenue/income/EPS), not a filing list
	NumResults int
}

// FilingResult is one SEC filing (or, in facts mode, one XBRL fact). Numeric
// XBRL values are passed through exactly as filed (no rounding).
type FilingResult struct {
	Company     string `json:"company"`
	CIK         string `json:"cik,omitempty"`
	FormType    string `json:"formType,omitempty"`
	FilingDate  string `json:"filingDate,omitempty"`
	PeriodOf    string `json:"periodOfReport,omitempty"`
	Accession   string `json:"accession,omitempty"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	// Fact fields (Facts mode only): a single XBRL company fact, verbatim.
	Concept string  `json:"concept,omitempty"`
	Unit    string  `json:"unit,omitempty"`
	Value   float64 `json:"value,omitempty"`
	Source  string  `json:"source"`
}

// FilingProviderConfig holds credentials/contact for filing providers.
type FilingProviderConfig struct {
	EDGARUserAgent string // SEC requires a descriptive UA: "app/version (contact email)"
}

// SupportedFilingProviders is the source of truth for filing provider names.
var SupportedFilingProviders = []string{"edgar"}

// NewFilingProviderByName constructs a filing provider, or nil when its required
// config is absent (so the provider is simply skipped — no dead config).
func NewFilingProviderByName(name string, cfg FilingProviderConfig, deps Deps) FilingProvider {
	switch name {
	case "edgar":
		// EDGAR needs a contact UA; without one SEC blocks requests, so skip.
		if cfg.EDGARUserAgent != "" {
			return NewEDGARProvider(cfg.EDGARUserAgent, deps)
		}
	}
	return nil
}

// AvailableFilingProviders builds the configured filing providers, each with its
// own circuit breaker (parity with AvailablePatentProviders).
func AvailableFilingProviders(cfg FilingProviderConfig, deps Deps) map[string]FilingProvider {
	providers := make(map[string]FilingProvider)
	for _, name := range SupportedFilingProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewFilingProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

// ──────────────────────── Case law (CourtListener) ─────────────────────────

// CaseSearcher finds court opinions.
type CaseSearcher interface {
	Cases(ctx context.Context, params CaseSearchParams) ([]CaseResult, error)
}

// CaseProvider is a named, described CaseSearcher.
type CaseProvider interface {
	CaseSearcher
	Name() string
	Metadata() ProviderMeta
}

// CaseSearchParams drives a case-law search.
type CaseSearchParams struct {
	Query        string // legal topic, case name, or statutory reference (required)
	Jurisdiction string // court id, e.g. "scotus", "ca9", "ny"
	DateFrom     string // YYYY-MM-DD (decision date)
	DateTo       string // YYYY-MM-DD
	NumResults   int
}

// CaseResult is one court opinion's metadata. Full opinion text is fetched via a
// follow-up scrape_page on URL.
type CaseResult struct {
	CaseName      string `json:"caseName"`
	Citation      string `json:"citation,omitempty"`
	Court         string `json:"court,omitempty"`
	CourtID       string `json:"courtId,omitempty"`
	DateFiled     string `json:"dateFiled,omitempty"`
	DocketNumber  string `json:"docketNumber,omitempty"`
	CitationCount int    `json:"citationCount,omitempty"`
	URL           string `json:"url"`
	Source        string `json:"source"`
}

// CaseProviderConfig holds case-law provider auth.
type CaseProviderConfig struct {
	CourtListenerToken string // optional; raises the rate limit. Provider works keyless.
}

// SupportedCaseProviders is the source of truth for case provider names.
var SupportedCaseProviders = []string{"courtlistener"}

// NewCaseProviderByName constructs a case provider. CourtListener works without a
// token (lower rate), so it is always available; the token just raises limits.
func NewCaseProviderByName(name string, cfg CaseProviderConfig, deps Deps) CaseProvider {
	switch name {
	case "courtlistener":
		return NewCourtListenerProvider(cfg.CourtListenerToken, deps)
	}
	return nil
}

// AvailableCaseProviders builds the configured case providers, each with its own
// circuit breaker.
func AvailableCaseProviders(cfg CaseProviderConfig, deps Deps) map[string]CaseProvider {
	providers := make(map[string]CaseProvider)
	for _, name := range SupportedCaseProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewCaseProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

// ───────────────────────── Economic data (FRED) ────────────────────────────

// EconSearcher finds economic series and their observations.
type EconSearcher interface {
	Econ(ctx context.Context, params EconSearchParams) ([]EconResult, error)
}

// EconProvider is a named, described EconSearcher.
type EconProvider interface {
	EconSearcher
	Name() string
	Metadata() ProviderMeta
}

// EconSearchParams drives an economic-data lookup. When SeriesID is set, the
// provider returns that series' observations; otherwise it searches series by
// keyword (Query).
type EconSearchParams struct {
	Query      string // keyword series search (when SeriesID is empty)
	SeriesID   string // e.g. "GDP", "CPIAUCSL", "UNRATE" → return observations
	DateFrom   string // YYYY-MM-DD
	DateTo     string // YYYY-MM-DD
	Frequency  string // optional: d, w, m, q, a
	Units      string // optional FRED units transform (e.g. "pch")
	NumResults int
}

// EconResult is either a series (search mode) or one observation (series mode).
// Numeric values are passed through exactly as returned (no rounding).
type EconResult struct {
	SeriesID    string  `json:"seriesId,omitempty"`
	Title       string  `json:"title,omitempty"`
	Units       string  `json:"units,omitempty"`
	Frequency   string  `json:"frequency,omitempty"`
	LastUpdated string  `json:"lastUpdated,omitempty"`
	Notes       string  `json:"notes,omitempty"`
	Date        string  `json:"date,omitempty"`  // observation date (series mode)
	Value       float64 `json:"value,omitempty"` // observation value (series mode)
	HasValue    bool    `json:"-"`               // distinguishes a real 0.0 from "missing"
	Source      string  `json:"source"`
}

// EconProviderConfig holds economic-data provider auth.
type EconProviderConfig struct {
	FREDAPIKey string
}

// SupportedEconProviders is the source of truth for econ provider names.
var SupportedEconProviders = []string{"fred"}

// NewEconProviderByName constructs an econ provider, or nil when its key is
// absent (provider skipped — no dead config).
func NewEconProviderByName(name string, cfg EconProviderConfig, deps Deps) EconProvider {
	switch name {
	case "fred":
		if cfg.FREDAPIKey != "" {
			return NewFREDProvider(cfg.FREDAPIKey, deps)
		}
	}
	return nil
}

// AvailableEconProviders builds the configured econ providers, each with its own
// circuit breaker.
func AvailableEconProviders(cfg EconProviderConfig, deps Deps) map[string]EconProvider {
	providers := make(map[string]EconProvider)
	for _, name := range SupportedEconProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewEconProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

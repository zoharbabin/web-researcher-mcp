package search

import (
	"context"
	"encoding/json"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

// This file defines the provider-INDEPENDENT synthesis capabilities — grounded
// answers and structured extraction — behind generic interfaces, exactly like
// AcademicSearcher/PatentSearcher. Any provider (Exa today, Perplexity or
// another tomorrow) implements these; the `answer` and `structured_search`
// tools never name a vendor. Adding a provider = one switch case +  one list
// entry here, with zero tool-layer changes.

// AnswerParams is a grounded-answer request: a question to synthesize an answer
// for, against the live web.
type AnswerParams struct {
	Query string
}

// Citation is one source backing a grounded answer or structured result.
type Citation struct {
	Title         string `json:"title,omitempty"`
	URL           string `json:"url"`
	PublishedDate string `json:"publishedDate,omitempty"`
}

// AnswerResult is a synthesized answer plus the sources it cites. Provider names
// which backend produced it (provenance); CostUSD is the per-call cost estimate
// for metered providers (0 for free ones).
type AnswerResult struct {
	Answer    string     `json:"answer"`
	Citations []Citation `json:"citations"`
	Provider  string     `json:"provider"`
	CostUSD   float64    `json:"costUsd,omitempty"`
}

// StructuredParams is a structured-extraction request. Category optionally
// focuses the search on an entity/content type; Schema is an optional JSON
// Schema describing the fields to extract per result. The set of valid
// categories and any schema limits are provider-specific and validated by the
// provider (NOT here), so this stays vendor-neutral.
type StructuredParams struct {
	Query      string
	Category   string
	NumResults int
	Schema     json.RawMessage
}

// StructuredItem is one enriched result. Summary is provider-extracted data
// (schema-conforming JSON when a schema was supplied, else a text summary);
// Entities carries provider-specific structured entities when available.
type StructuredItem struct {
	Title         string          `json:"title,omitempty"`
	URL           string          `json:"url"`
	PublishedDate string          `json:"publishedDate,omitempty"`
	Author        string          `json:"author,omitempty"`
	Summary       json.RawMessage `json:"summary,omitempty"`
	Highlights    []string        `json:"highlights,omitempty"`
	Entities      json.RawMessage `json:"entities,omitempty"`
}

// StructuredResult is the structured-search output. Provider names the backend;
// CostUSD is the per-call cost estimate for metered providers.
type StructuredResult struct {
	Results  []StructuredItem `json:"results"`
	Provider string           `json:"provider"`
	CostUSD  float64          `json:"costUsd,omitempty"`
}

// AnswerSearcher is the capability of synthesizing a grounded answer with
// citations from a query. Providers opt in by implementing it.
type AnswerSearcher interface {
	Answer(ctx context.Context, params AnswerParams) (*AnswerResult, error)
}

// StructuredSearcher is the capability of returning per-result structured data
// (optionally schema-extracted, optionally entity-typed) for a query.
type StructuredSearcher interface {
	StructuredSearch(ctx context.Context, params StructuredParams) (*StructuredResult, error)
}

// InvalidParamsError signals a request that a provider rejected at validation
// time (e.g. an unsupported category or an out-of-spec schema) — a permanent,
// non-retryable client error that never reached the network. It is part of the
// generic synthesis contract: any provider may return it, and the tool layer
// maps it to a validation tool-error. Deliberately distinct from upstream API
// errors so it does not trip the circuit breaker or suggest a retry.
type InvalidParamsError struct {
	Provider string
	Message  string
}

func (e *InvalidParamsError) Error() string { return e.Message }

// AnswerProvider / StructuredProvider are named, describable providers of the
// respective capability — the shape the registry/maps hold (mirrors
// AcademicProvider/PatentProvider).
type AnswerProvider interface {
	AnswerSearcher
	Name() string
	Metadata() ProviderMeta
}

type StructuredProvider interface {
	StructuredSearcher
	Name() string
	Metadata() ProviderMeta
}

// SupportedAnswerProviders / SupportedStructuredProviders list every provider
// name that can serve the capability. A new provider is added here (and to the
// factory below) — the tools discover it automatically.
var (
	SupportedAnswerProviders     = []string{"exa"}
	SupportedStructuredProviders = []string{"exa"}
)

// NewAnswerProviderByName constructs an answer provider by name if its
// credentials are configured, else nil.
func NewAnswerProviderByName(name string, cfg config.SearchConfig, deps Deps) AnswerProvider {
	switch name {
	case "exa":
		if cfg.ExaAPIKey != "" {
			return NewExaProvider(cfg.ExaAPIKey, deps)
		}
	}
	return nil
}

// NewStructuredProviderByName constructs a structured-search provider by name if
// its credentials are configured, else nil.
func NewStructuredProviderByName(name string, cfg config.SearchConfig, deps Deps) StructuredProvider {
	switch name {
	case "exa":
		if cfg.ExaAPIKey != "" {
			return NewExaProvider(cfg.ExaAPIKey, deps)
		}
	}
	return nil
}

// AvailableAnswerProviders returns all answer providers that can be constructed
// from config, each with its own circuit breaker for isolation.
func AvailableAnswerProviders(cfg config.SearchConfig, deps Deps) map[string]AnswerProvider {
	providers := make(map[string]AnswerProvider)
	for _, name := range SupportedAnswerProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewAnswerProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

// AvailableStructuredProviders returns all structured-search providers that can
// be constructed from config, each with its own circuit breaker.
func AvailableStructuredProviders(cfg config.SearchConfig, deps Deps) map[string]StructuredProvider {
	providers := make(map[string]StructuredProvider)
	for _, name := range SupportedStructuredProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewStructuredProviderByName(name, cfg, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

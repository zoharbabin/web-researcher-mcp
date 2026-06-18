package search

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// This file defines the ContextSearcher/ContextProvider capability: a
// provenance-rich LLM grounding context assembled server-side (Brave's
// /res/v1/llm/context endpoint). It follows the same shape as LocalSearcher /
// AnswerSearcher: interface + provider wrapper + SupportedContextProviders list
// + factory functions. The search_and_scrape tool layer depends only on
// ContextSearcher (via a type assertion), so adding a new provider here requires
// no tool-layer change.

// ContextSearcher assembles a grounding context — a pre-structured text block
// with per-snippet source provenance — designed for RAG/grounding workflows.
// Providers opt in by implementing this interface.
type ContextSearcher interface {
	Context(ctx context.Context, params ContextParams) (*ContextResult, error)
}

// ContextProvider is a named, described ContextSearcher.
type ContextProvider interface {
	ContextSearcher
	Name() string
	Metadata() ProviderMeta
}

// ContextParams drives a grounding-context request.
type ContextParams struct {
	Query     string
	MaxTokens int    // max tokens in the assembled context; default 8192
	Threshold string // "strict", "balanced" (default), or "lenient"
	Country   string // ISO 3166-1 alpha-2 country code
	Language  string // BCP 47 language code (e.g. "en")
}

// ContextSnippet is one source excerpt contributing to the assembled context,
// carrying full provenance (title, URL, age, text).
type ContextSnippet struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Age    string `json:"age,omitempty"`
	Text   string `json:"text"`
	Source string `json:"source"`
}

// ContextResult is the assembled grounding context plus per-snippet provenance.
// Context is the full text suitable for direct injection into a prompt; Snippets
// is the ordered list of excerpts that compose it (for citation and attribution).
type ContextResult struct {
	Context  string           `json:"context"`  // assembled grounding text
	Snippets []ContextSnippet `json:"snippets"` // per-snippet provenance
	Source   string           `json:"source"`   // provider name
}

// SupportedContextProviders is the source of truth for context-capable provider names.
var SupportedContextProviders = []string{"brave"}

// NewContextProviderByName constructs a context provider by name, or nil when
// its required credential is absent (provider skipped — no dead config).
func NewContextProviderByName(name string, braveKey string, deps Deps) ContextProvider {
	switch name {
	case "brave":
		if braveKey != "" {
			return NewBraveProvider(braveKey, BraveConfig{}, deps)
		}
	}
	return nil
}

// AvailableContextProviders builds the configured context providers, each with
// its own circuit breaker for isolation (parity with AvailableLocalProviders).
func AvailableContextProviders(braveKey string, deps Deps) map[string]ContextProvider {
	providers := make(map[string]ContextProvider)
	for _, name := range SupportedContextProviders {
		provDeps := Deps{
			HTTPClient: deps.HTTPClient,
			Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
		}
		if p := NewContextProviderByName(name, braveKey, provDeps); p != nil {
			providers[name] = p
		}
	}
	return providers
}

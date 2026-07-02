package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Exa (exa.ai) is a neural/semantic search API exposing a single rich /search
// endpoint plus grounded /answer and decoupled /contents. This file implements
// every Exa surface the server uses, all sharing one breaker-wrapped, x-api-key
// authenticated HTTP core:
//
//   - search.Provider           (Web, News, Images)  — web_search / news_search
//   - search.AcademicProvider    (Scholarly)          — academic_search via category:"research paper"
//   - search.AnswerProvider      (Answer)             — the provider-independent `answer` tool
//   - search.StructuredProvider  (StructuredSearch)   — the provider-independent `structured_search` tool
//
// The synthesis interfaces are vendor-neutral (synthesis.go); Exa is just the
// first implementer. A future provider (e.g. Perplexity) implements the same
// interfaces and the tools pick it up with no tool-layer change.
//
// All facts encoded here were live-verified against api.exa.ai (2026-06-04):
//   - auth is the `x-api-key` header (NOT Authorization: Bearer — docs list both).
//   - content fields MUST nest under "contents" ({"contents":{"highlights":true}});
//     top-level text/highlights/summary returns HTTP 400.
//   - structured extraction is `contents.summary.schema` (a JSON Schema) → each
//     results[].summary comes back as a JSON STRING matching it (there is no
//     top-level output/grounding block — that was a stale-doc claim).
//   - news = category:"news" + startPublishedDate → dated results.
//   - entities[] is a PER-RESULT field, present only for category:"company".
const (
	exaBaseURL = "https://api.exa.ai"

	// exaSearchType is fixed to "auto" so Exa picks neural-vs-keyword per query.
	// We deliberately do NOT expose the deep/deep-reasoning tiers ($12–15/1k,
	// multi-second latency) as a knob: an LLM caller cannot judge when the extra
	// cost is warranted, and "auto" is the balanced, predictable-cost default.
	exaSearchType = "auto"

	// exaMaxResults bounds numResults. Exa's per-call bundle includes the first
	// 10 results' text+highlights at the base price; beyond that costs more per
	// result, so 10 is both the value sweet spot and the tool-layer cap.
	exaMaxResults = 10
)

// ExaProvider talks to the Exa REST API. It is constructed once per process with
// its own circuit breaker (via Deps), exactly like the other providers.
type ExaProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

// NewExaProvider creates an Exa provider. The key is sent as the x-api-key header
// on every request and is never logged.
func NewExaProvider(apiKey string, deps Deps) *ExaProvider {
	return &ExaProvider{apiKey: apiKey, baseURL: exaBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (used in testing).
func (e *ExaProvider) SetBaseURL(base string) { e.baseURL = base }

func (e *ExaProvider) Name() string { return "exa" }

// Metadata lets the academic Router describe Exa among scholarly providers. Exa
// is a neural web index, not a curated bibliographic database, so it is an
// alt/fallback to openalex/crossref rather than an authoritative source.
func (e *ExaProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "semantic", "scholarly"},
		RateClass:    "metered",
		Description:  "Exa — neural/semantic web search (paid per call); academic via the research-paper category",
	}
}

// Web runs a neural web search. site:/lens operators appended by buildQuery are
// passed through verbatim (Exa parses them). Snippet comes from highlights[0].
func (e *ExaProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doWebSearch(ctx, params)
		return er
	})
	return results, err
}

// Images returns empty without error: Exa has no image-search endpoint. Matches
// the DuckDuckGo/Tavily convention — returning an error here would trip the
// per-provider circuit breaker and break Router image fallback. No breaker call
// and no HTTP request are made.
func (e *ExaProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

// News runs a recency-filtered search via category:"news" + startPublishedDate.
func (e *ExaProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doNewsSearch(ctx, params)
		return er
	})
	return results, err
}

// Scholarly runs an academic search via category:"research paper" (Phase 2).
// Exa returns neural web matches for papers; author/year/DOI are frequently
// absent (it is not a structured bibliographic DB), so this is best used as a
// routing fallback to openalex/crossref, not a replacement.
func (e *ExaProvider) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	var results []AcademicResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doScholarly(ctx, params)
		return er
	})
	return results, err
}

func (e *ExaProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	body := map[string]any{
		"query":      buildQuery(params),
		"type":       exaSearchType,
		"numResults": clamp(params.NumResults, 1, exaMaxResults),
		"contents":   map[string]any{"highlights": true}, // cheap snippet source; MUST nest under "contents"
	}

	var resp exaSearchResponse
	if err := e.doRequest(ctx, "/search", body, &resp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, SearchResult{
			Title:       r.Title, // may be empty — passed through as-is
			URL:         r.URL,
			Snippet:     r.snippet(),
			DisplayLink: extractDisplayLink(r.URL),
			PublishedAt: normalizePublishedAt(r.PublishedDate, time.Now()),
		})
	}
	return results, nil
}

func (e *ExaProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	body := map[string]any{
		"query":      params.Query,
		"type":       exaSearchType,
		"category":   "news",
		"numResults": clamp(params.NumResults, 1, exaMaxResults),
		"contents":   map[string]any{"highlights": true},
	}
	if sd := freshnessToStartDate(params.Freshness); sd != "" {
		body["startPublishedDate"] = sd
	}

	var resp exaSearchResponse
	if err := e.doRequest(ctx, "/search", body, &resp); err != nil {
		return nil, err
	}

	results := make([]NewsResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, NewsResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      extractDisplayLink(r.URL),                         // Exa has no separate source field; host is the honest source
			PublishedAt: normalizePublishedAt(r.PublishedDate, time.Now()), // ISO-normalized; empty when absent/unparseable → dropped by omitempty
			Snippet:     r.snippet(),
		})
	}
	return results, nil
}

func (e *ExaProvider) doScholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	body := map[string]any{
		"query":      params.Query,
		"type":       exaSearchType,
		"category":   "research paper",
		"numResults": clamp(params.NumResults, 1, exaMaxResults),
		"contents":   map[string]any{"highlights": true},
	}
	// Map the year window to Exa's publish-date filters (ISO-8601 date-times).
	if params.YearFrom > 0 {
		body["startPublishedDate"] = fmt.Sprintf("%04d-01-01T00:00:00.000Z", params.YearFrom)
	}
	if params.YearTo > 0 {
		body["endPublishedDate"] = fmt.Sprintf("%04d-12-31T23:59:59.999Z", params.YearTo)
	}

	var resp exaSearchResponse
	if err := e.doRequest(ctx, "/search", body, &resp); err != nil {
		return nil, err
	}

	results := make([]AcademicResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Title == "" {
			continue
		}
		res := AcademicResult{
			Title:    r.Title,
			URL:      r.URL,
			Abstract: truncateText(r.snippet(), 500),
			Source:   "exa",
			Year:     publishYear(r.PublishedDate),
		}
		if r.Author != "" {
			res.Authors = []string{r.Author}
		}
		results = append(results, res)
	}
	return results, nil
}

// --- Generic synthesis capabilities (AnswerSearcher / StructuredSearcher) ----
//
// Exa implements the provider-independent AnswerSearcher and StructuredSearcher
// interfaces (synthesis.go). The `answer` and `structured_search` tools talk to
// those interfaces only; Exa is just the first implementer.

// Answer returns a grounded natural-language answer with citations via /answer.
func (e *ExaProvider) Answer(ctx context.Context, params AnswerParams) (*AnswerResult, error) {
	var out *AnswerResult
	err := e.deps.Breaker.Execute(func() error {
		body := map[string]any{"query": params.Query}
		var resp exaAnswerResponse
		if er := e.doRequest(ctx, "/answer", body, &resp); er != nil {
			return er
		}
		res := &AnswerResult{Answer: resp.Answer, Provider: "exa", CostUSD: resp.CostDollars.Total}
		for _, c := range resp.Citations {
			res.Citations = append(res.Citations, Citation(c))
		}
		out = res
		return nil
	})
	return out, err
}

// StructuredSearch runs /search with an optional category and an optional
// summary JSON Schema, surfacing per-result entities[] when present. It
// validates Exa's own constraints (category vocabulary, summary-schema limits)
// up front and returns an InvalidParamsError (not retryable, no breaker trip)
// so a malformed request never burns a paid call on a guaranteed 400.
func (e *ExaProvider) StructuredSearch(ctx context.Context, params StructuredParams) (*StructuredResult, error) {
	if msg := e.validateStructured(params); msg != "" {
		return nil, &InvalidParamsError{Provider: "exa", Message: msg}
	}
	var out *StructuredResult
	err := e.deps.Breaker.Execute(func() error {
		contents := map[string]any{"highlights": true}
		if len(params.Schema) > 0 {
			// A schema turns the summary into structured JSON conforming to it.
			contents["summary"] = map[string]any{"schema": params.Schema}
		} else {
			contents["summary"] = true
		}
		body := map[string]any{
			"query":      params.Query,
			"type":       exaSearchType,
			"numResults": clamp(params.NumResults, 1, exaMaxResults),
			"contents":   contents,
		}
		if params.Category != "" {
			body["category"] = params.Category
		}

		var resp exaSearchResponse
		if er := e.doRequest(ctx, "/search", body, &resp); er != nil {
			return er
		}
		res := &StructuredResult{Provider: "exa", CostUSD: resp.CostDollars.Total}
		for _, r := range resp.Results {
			item := StructuredItem{
				Title:         r.Title,
				URL:           r.URL,
				PublishedDate: r.PublishedDate,
				Author:        r.Author,
				Highlights:    r.Highlights,
				Summary:       jsonOrString(r.Summary),
				Entities:      r.Entities,
			}
			res.Results = append(res.Results, item)
		}
		out = res
		return nil
	})
	return out, err
}

// ExaCategories returns the structured-search categories Exa supports (live-
// verified to return HTTP 200). Empty string (no category) is always allowed and
// is not listed. Exposed so a caller can surface valid options without
// hard-coding Exa's vocabulary.
func ExaCategories() []string {
	return []string{"company", "people", "research paper", "news", "pdf", "github", "financial report", "personal site"}
}

// validateStructured enforces Exa's own request constraints before any paid
// call: a known category (or none), and a summary schema within Exa's documented
// limits (root object, ≤10 properties, nesting depth ≤2, primitive array items).
// Returns "" when valid, else a human-readable reason. This vendor-specific
// knowledge lives here, in the provider — the generic tool stays vendor-neutral.
func (e *ExaProvider) validateStructured(params StructuredParams) string {
	if params.Category != "" {
		ok := false
		for _, c := range ExaCategories() {
			if c == params.Category {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Sprintf("invalid category %q for provider exa; allowed: %s (or omit)",
				params.Category, strings.Join(ExaCategories(), ", "))
		}
	}
	if len(params.Schema) == 0 {
		return ""
	}
	var schema map[string]any
	if err := json.Unmarshal(params.Schema, &schema); err != nil {
		return "schema is not valid JSON: " + err.Error()
	}
	if t, _ := schema["type"].(string); t != "object" {
		return `schema root must have "type": "object"`
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return `schema must declare a non-empty "properties" object`
	}
	if len(props) > 10 {
		return fmt.Sprintf("schema has %d properties; exa allows at most 10", len(props))
	}
	for name, p := range props {
		pm, ok := p.(map[string]any)
		if !ok {
			return fmt.Sprintf("property %q must be a schema object", name)
		}
		switch pm["type"] {
		case "object":
			return fmt.Sprintf("property %q is a nested object; exa schemas allow nesting depth at most 2 (use a flat object)", name)
		case "array":
			items, _ := pm["items"].(map[string]any)
			if it, _ := items["type"].(string); it == "object" || it == "array" {
				return fmt.Sprintf("property %q is an array of %s; exa requires array items to be primitive", name, it)
			}
		}
	}
	return ""
}

// --- Shared HTTP core -------------------------------------------------------

// doRequest POSTs a JSON payload to an Exa endpoint with x-api-key auth and
// decodes the response into out. Error classification matches the other
// providers: 429 → an error containing "rate limited" (so tools.isRateLimitError
// picks it up); any other >=400 → a descriptive upstream error. The Exa error
// envelope ({error, tag}) is surfaced in the message; 402 (NO_MORE_CREDITS) is
// included verbatim so the LLM sees the out-of-credits condition.
func (e *ExaProvider) doRequest(ctx context.Context, path string, payload map[string]any, out any) error {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", e.apiKey) // Exa auth; never logged

	resp, err := e.deps.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return fmt.Errorf("exa API rate limited")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("exa API error %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return fmt.Errorf("exa: failed to read response: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("exa: failed to parse response: %w", err)
	}
	return nil
}

// --- Response shapes (live-verified field names) ----------------------------

type exaSearchResponse struct {
	Results     []exaResult    `json:"results"`
	CostDollars exaCostDollars `json:"costDollars"`
}

type exaResult struct {
	Title         string          `json:"title"` // may be ""
	URL           string          `json:"url"`
	PublishedDate string          `json:"publishedDate"` // may be ""/null
	Author        string          `json:"author"`        // may be ""/null
	Highlights    []string        `json:"highlights"`
	Summary       string          `json:"summary"`  // a JSON string when a summary schema was supplied
	Entities      json.RawMessage `json:"entities"` // present only for category:"company"
}

// snippet returns the best available short text for a result: the first
// highlight, falling back to the summary.
func (r exaResult) snippet() string {
	if len(r.Highlights) > 0 && r.Highlights[0] != "" {
		return r.Highlights[0]
	}
	return r.Summary
}

type exaAnswerResponse struct {
	Answer      string         `json:"answer"`
	Citations   []exaCitation  `json:"citations"`
	CostDollars exaCostDollars `json:"costDollars"`
}

type exaCitation struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	PublishedDate string `json:"publishedDate"`
}

// exaCostDollars captures Exa's per-call cost estimate (total in USD). It is an
// estimate, not an invoice, and is surfaced into audit metadata as cost_usd.
type exaCostDollars struct {
	Total float64 `json:"total"`
}

// --- Helpers ----------------------------------------------------------------

// freshnessToStartDate maps the project's freshness vocabulary to an absolute
// ISO-8601 startPublishedDate (Exa has no relative-window param). "hour" has no
// sub-day granularity in this filter so it collapses to the past day. Unknown
// values return "" (no date filter applied).
func freshnessToStartDate(freshness string) string {
	var d time.Duration
	switch freshness {
	case "hour", "day":
		d = 24 * time.Hour
	case "week":
		d = 7 * 24 * time.Hour
	case "month":
		d = 30 * 24 * time.Hour
	case "year":
		d = 365 * 24 * time.Hour
	default:
		return ""
	}
	return time.Now().UTC().Add(-d).Format("2006-01-02T15:04:05.000Z")
}

// publishYear extracts the 4-digit year from an ISO-8601 date, or 0 if absent.
// Exa returns full RFC3339 timestamps for most results but date-only strings
// ("2017-06-12" or bare "2017") for some research-paper results, so it falls
// back through progressively looser parses rather than dropping the year.
func publishYear(iso string) int {
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.Year()
	}
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Year()
	}
	if len(iso) >= 4 {
		if y, err := strconv.Atoi(iso[:4]); err == nil && y > 1000 {
			return y
		}
	}
	return 0
}

// jsonOrString normalizes Exa's summary string for embedding. When a summary
// schema was supplied, Exa returns a JSON object/array string — embedded
// verbatim so the caller gets structured data. Otherwise (plain-text summary,
// or a bare JSON scalar like "null"/"123" that only coincidentally parses) it is
// encoded as a JSON string, preserving the "object-or-string" output contract.
// Empty → nil (dropped by omitempty).
func jsonOrString(summary string) json.RawMessage {
	if summary == "" {
		return nil
	}
	if trimmed := strings.TrimSpace(summary); len(trimmed) > 0 &&
		(trimmed[0] == '{' || trimmed[0] == '[') && json.Valid([]byte(summary)) {
		return json.RawMessage(summary)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil
	}
	return encoded
}

var (
	_ Provider           = (*ExaProvider)(nil)
	_ AcademicProvider   = (*ExaProvider)(nil)
	_ AnswerProvider     = (*ExaProvider)(nil)
	_ StructuredProvider = (*ExaProvider)(nil)
)

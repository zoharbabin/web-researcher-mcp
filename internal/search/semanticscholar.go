package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// SemanticScholarProvider searches the Semantic Scholar Academic Graph (Allen
// AI): 200M+ papers with AI-enrichment metadata no other academic API offers —
// TLDR one-sentence summaries, citation-intent labels, and "highly influential"
// citation flags. It implements AcademicProvider (search) plus citation-edge
// lookups (Citations/References) consumed by the citation_graph tool (#47).
//
// Rate limit: the keyless public tier is a low SHARED rate (~1 req/s across all
// unauthenticated callers); an API key raises it. To stay polite and avoid 429
// storms we serialize every request behind a process-wide mutex with a minimum
// spacing, on top of the per-provider circuit breaker. Verified live (2026-06-04):
// search → data[]{paperId,externalIds{DOI,ArXiv},title,venue,year,citationCount,
// isOpenAccess,openAccessPdf{url},tldr{text},authors,abstract}; citations →
// data[]{isInfluential,intents[],citingPaper{...}}.
const (
	semanticScholarBaseURL = "https://api.semanticscholar.org/graph/v1"
	// s2SearchFields / s2PaperFields are the field masks; S2 returns only fields
	// explicitly requested, so these define our result shape.
	s2SearchFields = "title,abstract,year,authors,externalIds,openAccessPdf,citationCount,tldr,isOpenAccess,venue"
	s2EdgeFields   = "intents,isInfluential,title,year,authors,externalIds,openAccessPdf,citationCount,abstract,venue"
	// s2MinSpacing is the minimum gap between outbound S2 requests (politeness for
	// the shared keyless tier). A key raises the server-side limit but we keep the
	// spacing modest regardless to be a good citizen.
	s2MinSpacing = 1100 * time.Millisecond
)

// s2Throttle serializes S2 requests process-wide with a minimum spacing. Shared
// across all SemanticScholarProvider instances because the rate limit is global
// to the API key (or the shared keyless pool), not per-instance. `next` is the
// earliest time the next request may fire; each caller atomically reserves and
// advances it, then waits OUTSIDE the lock so a slow/cancelled request never
// blocks the queue behind it.
var s2Throttle struct {
	mu   sync.Mutex
	next time.Time
}

// s2Wait reserves this request's spaced slot and waits for it, honoring ctx.
// The mutex is held only long enough to claim the slot (O(1)); the wait itself
// is lock-free, so a cancelled request leaves the queue immediately and never
// stalls other S2 callers. Returns ctx.Err() if the context ends during the wait.
func s2Wait(ctx context.Context) error {
	s2Throttle.mu.Lock()
	now := time.Now()
	slot := s2Throttle.next
	if slot.Before(now) {
		slot = now
	}
	s2Throttle.next = slot.Add(s2MinSpacing)
	s2Throttle.mu.Unlock()

	d := time.Until(slot)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SemanticScholarProvider implements AcademicProvider + citation-edge lookups.
type SemanticScholarProvider struct {
	apiKey  string // optional; raises the rate limit when set, sent as x-api-key
	baseURL string
	deps    Deps
}

// NewSemanticScholarProvider constructs the provider. An empty apiKey is valid
// (keyless public tier); the key, when present, is sent as x-api-key and is
// never logged.
func NewSemanticScholarProvider(apiKey string, deps Deps) *SemanticScholarProvider {
	return &SemanticScholarProvider{apiKey: apiKey, baseURL: semanticScholarBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (used in testing).
func (s *SemanticScholarProvider) SetBaseURL(base string) { s.baseURL = base }

func (s *SemanticScholarProvider) Name() string { return "semanticscholar" }

func (s *SemanticScholarProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biblio", "citations", "semantic", "tldr"},
		RateClass:    "free",
		Description:  "Semantic Scholar — 200M+ papers with TLDR summaries, citation intent, and influence signals",
	}
}

func (s *SemanticScholarProvider) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	var results []AcademicResult
	err := s.deps.Breaker.Execute(func() error {
		var er error
		results, er = s.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (s *SemanticScholarProvider) doSearch(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	q := url.Values{}
	q.Set("query", params.Query)
	q.Set("limit", fmt.Sprintf("%d", clamp(params.NumResults, 1, 25)))
	q.Set("fields", s2SearchFields)
	if params.YearFrom > 0 || params.YearTo > 0 {
		q.Set("year", s2YearRange(params.YearFrom, params.YearTo))
	}
	if params.OpenAccess {
		q.Set("openAccessPdf", "") // S2: presence of this param restricts to OA papers
	}

	var resp s2SearchResponse
	if err := s.doRequest(ctx, "/paper/search?"+q.Encode(), &resp); err != nil {
		return nil, err
	}

	results := make([]AcademicResult, 0, len(resp.Data))
	for _, p := range resp.Data {
		if p.Title == "" {
			continue
		}
		results = append(results, p.toAcademicResult())
	}
	return results, nil
}

// Citations returns papers that CITE the seed paper (forward edges), annotated
// with citation intent + influence. seedID is a DOI or a Semantic Scholar id.
func (s *SemanticScholarProvider) Citations(ctx context.Context, seedID string, numResults int) ([]AcademicResult, error) {
	return s.edges(ctx, s2PaperPath(seedID)+"/citations", numResults, true)
}

// References returns papers the seed paper CITES (backward edges).
func (s *SemanticScholarProvider) References(ctx context.Context, seedID string, numResults int) ([]AcademicResult, error) {
	return s.edges(ctx, s2PaperPath(seedID)+"/references", numResults, false)
}

func (s *SemanticScholarProvider) edges(ctx context.Context, path string, numResults int, forward bool) ([]AcademicResult, error) {
	var out []AcademicResult
	err := s.deps.Breaker.Execute(func() error {
		q := url.Values{}
		q.Set("fields", s2EdgeFields)
		q.Set("limit", fmt.Sprintf("%d", clamp(numResults, 1, 25)))

		var resp s2EdgeResponse
		if er := s.doRequest(ctx, path+"?"+q.Encode(), &resp); er != nil {
			return er
		}
		for _, e := range resp.Data {
			paper := e.CitingPaper
			if !forward {
				paper = e.CitedPaper
			}
			if paper == nil || paper.Title == "" {
				continue
			}
			r := paper.toAcademicResult()
			r.IsInfluential = e.IsInfluential
			r.CitationIntents = e.Intents
			out = append(out, r)
		}
		return nil
	})
	return out, err
}

func (s *SemanticScholarProvider) doRequest(ctx context.Context, path string, out any) error {
	// Politeness spacing for the shared/keyless rate limit; honors ctx so a
	// cancelled request abandons the queue instead of stalling other callers.
	if err := s2Wait(ctx); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if s.apiKey != "" {
		req.Header.Set("x-api-key", s.apiKey) // never logged
	}

	resp, err := s.deps.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return fmt.Errorf("semanticscholar: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == 404 {
		return fmt.Errorf("semanticscholar: paper not found")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("semanticscholar: API error %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return fmt.Errorf("semanticscholar: failed to read response: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("semanticscholar: failed to parse response: %w", err)
	}
	return nil
}

// --- response shapes (live-verified field names) ----------------------------

type s2SearchResponse struct {
	Data []s2Paper `json:"data"`
}

type s2EdgeResponse struct {
	Data []struct {
		IsInfluential bool     `json:"isInfluential"`
		Intents       []string `json:"intents"`
		CitingPaper   *s2Paper `json:"citingPaper"`
		CitedPaper    *s2Paper `json:"citedPaper"`
	} `json:"data"`
}

type s2Paper struct {
	PaperID     string `json:"paperId"`
	Title       string `json:"title"`
	Abstract    string `json:"abstract"`
	Year        int    `json:"year"`
	Venue       string `json:"venue"`
	CitationCt  int    `json:"citationCount"`
	IsOpenAcc   bool   `json:"isOpenAccess"`
	ExternalIDs struct {
		DOI   string `json:"DOI"`
		ArXiv string `json:"ArXiv"`
	} `json:"externalIds"`
	OpenAccessPDF *struct {
		URL string `json:"url"`
	} `json:"openAccessPdf"`
	TLDR *struct {
		Text string `json:"text"`
	} `json:"tldr"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
}

func (p s2Paper) toAcademicResult() AcademicResult {
	r := AcademicResult{
		Title:         p.Title,
		DOI:           p.ExternalIDs.DOI,
		Journal:       p.Venue,
		Year:          p.Year,
		Abstract:      truncateText(p.Abstract, 500),
		CitationCount: p.CitationCt,
		Source:        "semanticscholar",
		OpenAccess:    p.IsOpenAcc,
	}
	if p.TLDR != nil {
		r.TLDR = p.TLDR.Text
	}
	if p.OpenAccessPDF != nil {
		r.PDFUrl = p.OpenAccessPDF.URL
	}
	for _, a := range p.Authors {
		if a.Name != "" {
			r.Authors = append(r.Authors, a.Name)
		}
	}
	// Prefer DOI URL, then the S2 paper page.
	switch {
	case r.DOI != "":
		r.URL = "https://doi.org/" + r.DOI
	case p.ExternalIDs.ArXiv != "":
		r.URL = "https://arxiv.org/abs/" + p.ExternalIDs.ArXiv
	case p.PaperID != "":
		r.URL = "https://www.semanticscholar.org/paper/" + p.PaperID
	}
	return r
}

// --- helpers ----------------------------------------------------------------

// s2PaperPath builds the /paper/{id} path segment, prefixing a DOI so S2 resolves
// it (DOI:10.x). A bare Semantic Scholar id or other supported id is passed
// through unchanged.
func s2PaperPath(seedID string) string {
	seedID = strings.TrimSpace(seedID)
	if isDOI(seedID) {
		return "/paper/DOI:" + seedID
	}
	return "/paper/" + url.PathEscape(seedID)
}

// s2YearRange maps a from/to window to S2's "year" filter syntax: "2020-2024",
// "2020-" (from only), or "-2024" (to only).
func s2YearRange(from, to int) string {
	switch {
	case from > 0 && to > 0:
		return fmt.Sprintf("%d-%d", from, to)
	case from > 0:
		return fmt.Sprintf("%d-", from)
	case to > 0:
		return fmt.Sprintf("-%d", to)
	default:
		return ""
	}
}

// isDOI reports whether s looks like a DOI (starts with "10." and contains a slash).
func isDOI(s string) bool {
	return strings.HasPrefix(s, "10.") && strings.Contains(s, "/")
}

var (
	_ AcademicProvider = (*SemanticScholarProvider)(nil)
	_ CitationSearcher = (*SemanticScholarProvider)(nil)
)

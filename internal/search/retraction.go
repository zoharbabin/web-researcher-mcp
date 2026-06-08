package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Retraction integrity enrichment (#156). Crossref absorbed the Retraction Watch
// database in 2024/2025, so a single keyless call to the production REST API
// returns both Retraction-Watch and publisher (CrossMark) notices, merged.
//
// Like Unpaywall (see unpaywall.go), this is an ENRICHMENT layer over the
// academic pipeline — NOT a search provider. After academic_search /
// citation_graph return DOI-bearing results, each DOI is optionally checked and
// a RetractionStatus attached. Best-effort by contract: any failure (404, 429,
// 5xx, parse) leaves the result un-flagged and never fails the search.
//
// Canonical mechanism (verified live 2026-06-08, per Crossref's own guidance):
// query GET /works/{doi} and read message.updated-by — the array of notices
// that update THIS work. (update-to is the reverse direction on the notice
// itself; relation is unrelated.) A clean DOI has no updated-by key at all.
const crossrefWorksBaseURL = "https://api.crossref.org/works"

// RetractionResolver resolves a DOI to its retraction/correction status.
// Implemented by CrossrefRetractionResolver; an interface so the academic tools
// hold a nil-able dependency and tests can substitute a fake.
type RetractionResolver interface {
	// Resolve returns the integrity status for a DOI. found=false (nil error)
	// means "no notice / not resolvable" — callers treat that as a clean no-op,
	// never an error.
	Resolve(ctx context.Context, doi string) (status *RetractionStatus, found bool, err error)
	Name() string
}

// CrossrefRetractionResolver is the Crossref implementation of RetractionResolver.
type CrossrefRetractionResolver struct {
	mailto  string // polite-pool identification (optional but recommended)
	baseURL string
	deps    Deps
}

// NewCrossrefRetractionResolver constructs the resolver. The Crossref works API
// is keyless, so this is always non-nil; mailto (reusing the OpenAlex/CrossRef
// email convention) lands requests in the faster, more reliable polite pool.
func NewCrossrefRetractionResolver(mailto string, deps Deps) *CrossrefRetractionResolver {
	return &CrossrefRetractionResolver{mailto: strings.TrimSpace(mailto), baseURL: crossrefWorksBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (testing).
func (c *CrossrefRetractionResolver) SetBaseURL(base string) { c.baseURL = base }

func (c *CrossrefRetractionResolver) Name() string { return "crossref-retraction" }

func (c *CrossrefRetractionResolver) Resolve(ctx context.Context, doi string) (*RetractionStatus, bool, error) {
	doi = normalizeDOI(doi)
	if doi == "" {
		return nil, false, nil
	}

	var status *RetractionStatus
	var found bool
	err := c.deps.Breaker.Execute(func() error {
		reqURL := fmt.Sprintf("%s/%s", c.baseURL, doi)
		if c.mailto != "" {
			reqURL += "?mailto=" + url.QueryEscape(c.mailto)
		}
		req, er := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if er != nil {
			return er
		}
		req.Header.Set("Accept", "application/json")
		// Polite-pool User-Agent with mailto (Crossref convention).
		if c.mailto != "" {
			req.Header.Set("User-Agent", "web-researcher-mcp (mailto:"+c.mailto+")")
		}

		resp, er := c.deps.HTTPClient.Do(req)
		if er != nil {
			return er
		}
		defer resp.Body.Close()

		switch {
		case resp.StatusCode == 404:
			// Unknown/malformed DOI — legitimate "no info", not an error.
			return nil
		case resp.StatusCode == 429:
			return fmt.Errorf("crossref: rate limited")
		case resp.StatusCode >= 400:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("crossref: API error %d: %s", resp.StatusCode, string(body))
		}

		data, er := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		if er != nil {
			return er
		}
		var cr crossrefWorkResponse
		if er := json.Unmarshal(data, &cr); er != nil {
			return fmt.Errorf("crossref: parse: %w", er)
		}
		found = true
		status = integrityFromUpdates(cr.Message.UpdatedBy)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return status, found, nil
}

// integrityFromUpdates reduces the updated-by array to a single status, taking
// the most severe notice (retraction > expression_of_concern > correction).
// Returns nil when there is no integrity-relevant notice (a clean work, or only
// new-version/unknown updates). Dedup is implicit: a duplicate retraction from a
// second source can't change the chosen severity or fields meaningfully.
func integrityFromUpdates(updates []crossrefUpdate) *RetractionStatus {
	const (
		sevNone = iota
		sevCorrection
		sevConcern
		sevRetraction
	)
	best := sevNone
	var chosen *RetractionStatus
	for i := range updates {
		u := &updates[i]
		kind, sev := classifyUpdateType(u.Type)
		if sev == sevNone || sev < best {
			continue
		}
		best = sev
		chosen = &RetractionStatus{
			Retracted: sev == sevRetraction,
			Kind:      kind,
			Date:      u.Updated.date(),
			NoticeDOI: u.DOI,
			Source:    u.Source,
		}
	}
	return chosen
}

// classifyUpdateType maps a Crossref update `type` to our coarse kind + a
// severity rank. Unknown/version types return ("", sevNone) so they're ignored.
func classifyUpdateType(t string) (kind string, severity int) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "retraction", "partial_retraction", "withdrawal", "removal":
		return RetractionKindRetraction, 3
	case "expression_of_concern":
		return RetractionKindConcern, 2
	case "correction", "corrigendum", "erratum", "addendum", "clarification":
		return RetractionKindCorrection, 1
	default:
		return "", 0
	}
}

// normalizeDOI strips a doi.org URL prefix and trims; returns "" for empty.
func normalizeDOI(doi string) string {
	doi = strings.TrimSpace(doi)
	doi = strings.TrimPrefix(doi, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")
	doi = strings.TrimPrefix(doi, "doi:")
	return strings.TrimSpace(doi)
}

type crossrefWorkResponse struct {
	Message struct {
		UpdatedBy []crossrefUpdate `json:"updated-by"`
	} `json:"message"`
}

type crossrefUpdate struct {
	DOI     string             `json:"DOI"`
	Type    string             `json:"type"`
	Label   string             `json:"label"`
	Source  string             `json:"source"`
	Updated crossrefUpdateDate `json:"updated"`
}

type crossrefUpdateDate struct {
	DateParts [][]int `json:"date-parts"`
	DateTime  string  `json:"date-time"`
}

// date returns YYYY-MM-DD, preferring the ISO date-time, falling back to
// date-parts (which may be year-only or year+month). Returns "" when absent.
func (d crossrefUpdateDate) date() string {
	if len(d.DateTime) >= 10 {
		return d.DateTime[:10]
	}
	if len(d.DateParts) == 0 || len(d.DateParts[0]) == 0 {
		return ""
	}
	p := d.DateParts[0]
	switch len(p) {
	case 1:
		return fmt.Sprintf("%04d", p[0])
	case 2:
		return fmt.Sprintf("%04d-%02d", p[0], p[1])
	default:
		return fmt.Sprintf("%04d-%02d-%02d", p[0], p[1], p[2])
	}
}

// EnrichRetraction flags DOI-bearing results with their Crossref integrity
// status. Best-effort and nil-safe (a nil resolver is a no-op; a per-DOI failure
// leaves that result un-flagged and never aborts the batch). It never overwrites
// an already-set status. Bounded: only results with a DOI and no existing status
// are checked. Mirrors EnrichOpenAccess (unpaywall.go) by design.
func EnrichRetraction(ctx context.Context, resolver RetractionResolver, results []AcademicResult) []AcademicResult {
	if resolver == nil {
		return results
	}
	for i := range results {
		r := &results[i]
		if r.DOI == "" || r.Retraction != nil {
			continue
		}
		status, found, err := resolver.Resolve(ctx, r.DOI)
		if err != nil || !found || status == nil {
			continue // best-effort: leave un-flagged
		}
		r.Retraction = status
	}
	return results
}

var _ RetractionResolver = (*CrossrefRetractionResolver)(nil)

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const coreMaxBody = 2 << 20 // 2 MiB, see issue #267 Performance rule

// COREProvider implements AcademicProvider using the CORE.ac.uk v3 API — the
// largest aggregator of open-access research outputs (300M+ works across
// global repositories). Works keyless at a lower shared rate; supply an API
// key (free registration at core.ac.uk) for higher limits. Does not implement
// DOIResolver or CitationSearcher: CORE v3 has no deterministic DOI-entity
// endpoint and no citation graph (see issue #267).
type COREProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

// NewCOREProvider creates the provider. apiKey is optional — CORE works
// keyless at a lower shared rate; a key raises the limit.
func NewCOREProvider(apiKey string, deps Deps) *COREProvider {
	return &COREProvider{
		apiKey:  apiKey,
		baseURL: "https://api.core.ac.uk/v3",
		deps:    deps,
	}
}

// SetBaseURL overrides the API base URL (testing).
func (p *COREProvider) SetBaseURL(base string) { p.baseURL = base }

func (p *COREProvider) Name() string { return "core" }

func (p *COREProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"academic", "fulltext"},
		RateClass:    "free",
		Description:  "CORE.ac.uk — 300M+ open-access research outputs with full text, aggregated from repositories worldwide",
	}
}

func (p *COREProvider) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	var results []AcademicResult
	err := p.deps.Breaker.Execute(func() error {
		var e error
		results, e = p.doSearch(ctx, params)
		return e
	})
	return results, err
}

func (p *COREProvider) doSearch(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	q := strings.TrimSpace(params.Query)
	if q == "" {
		return nil, nil // honor empty: no query, no results (no fallback)
	}

	if params.YearFrom > 0 && params.YearTo > 0 {
		q = fmt.Sprintf("%s AND yearPublished>=%d AND yearPublished<=%d", q, params.YearFrom, params.YearTo)
	} else if params.YearFrom > 0 {
		q = fmt.Sprintf("%s AND yearPublished>=%d", q, params.YearFrom)
	} else if params.YearTo > 0 {
		q = fmt.Sprintf("%s AND yearPublished<=%d", q, params.YearTo)
	}

	qs := url.Values{}
	qs.Set("q", q)
	qs.Set("limit", strconv.Itoa(clamp(params.NumResults, 1, 25)))
	qs.Set("fields", "title,abstract,fullText,doi,authors,yearPublished,downloadUrl")
	if params.SortBy == "date" {
		qs.Set("sort", "yearPublished:desc")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/search/works?"+qs.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("core: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("core: API error %d: %s", resp.StatusCode, string(b))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, coreMaxBody))
	if err != nil {
		return nil, fmt.Errorf("core: read body: %w", err)
	}

	var out coreSearchResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("core: parse response: %w", err)
	}
	return coreWorksToResults(out.Results), nil
}

// coreSearchResponse models the CORE v3 /search/works response envelope.
type coreSearchResponse struct {
	Results []coreWork `json:"results"`
}

type coreWork struct {
	Title         string       `json:"title"`
	Abstract      string       `json:"abstract"`
	FullText      string       `json:"fullText"`
	DOI           string       `json:"doi"`
	Authors       []coreAuthor `json:"authors"`
	YearPublished int          `json:"yearPublished"`
	DownloadURL   string       `json:"downloadUrl"`
}

type coreAuthor struct {
	Name string `json:"name"`
}

// coreWorksToResults maps CORE works to AcademicResult, skipping any work with
// neither a DOI nor a download URL (nothing to link the result to).
func coreWorksToResults(works []coreWork) []AcademicResult {
	out := make([]AcademicResult, 0, len(works))
	for _, w := range works {
		doi := strings.TrimSpace(w.DOI)
		downloadURL := strings.TrimSpace(w.DownloadURL)

		resultURL := downloadURL
		if doi != "" {
			resultURL = "https://doi.org/" + doi
		}
		if resultURL == "" {
			continue // nothing to link this result to
		}

		abstract := strings.TrimSpace(w.Abstract)
		if abstract == "" && w.FullText != "" {
			abstract = truncateText(strings.TrimSpace(w.FullText), 500)
		} else if abstract != "" {
			abstract = truncateText(abstract, 500)
		}

		authors := make([]string, 0, len(w.Authors))
		for _, a := range w.Authors {
			if name := strings.TrimSpace(a.Name); name != "" {
				authors = append(authors, name)
			}
		}

		out = append(out, AcademicResult{
			Title:      truncateText(strings.TrimSpace(w.Title), 300),
			URL:        resultURL,
			DOI:        doi,
			Authors:    authors,
			Year:       w.YearPublished,
			Abstract:   abstract,
			Source:     "core",
			OpenAccess: true, // all CORE content is OA by definition
			PDFUrl:     downloadURL,
		})
	}
	return out
}

var _ AcademicProvider = (*COREProvider)(nil)

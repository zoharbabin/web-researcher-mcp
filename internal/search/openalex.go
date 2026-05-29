package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// OpenAlexProvider searches the OpenAlex API for scholarly works.
// Coverage: 287M+ works across all academic disciplines (CC0 data).
type OpenAlexProvider struct {
	email   string
	baseURL string
	deps    Deps
}

// NewOpenAlexProvider creates an OpenAlex provider using the given email for polite pool access.
func NewOpenAlexProvider(email string, deps Deps) *OpenAlexProvider {
	return &OpenAlexProvider{
		email:   email,
		baseURL: "https://api.openalex.org",
		deps:    deps,
	}
}

func (p *OpenAlexProvider) Name() string { return "openalex" }

func (p *OpenAlexProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biblio", "citations", "semantic"},
		RateClass:    "free",
		Description:  "OpenAlex — open scholarly metadata, 287M+ works across all disciplines",
	}
}

func (p *OpenAlexProvider) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	var results []AcademicResult
	err := p.deps.Breaker.Execute(func() error {
		var er error
		results, er = p.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (p *OpenAlexProvider) doSearch(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	num := clamp(params.NumResults, 1, 25)

	q := url.Values{}
	q.Set("search", params.Query)
	q.Set("per_page", fmt.Sprintf("%d", num))
	q.Set("mailto", p.email)

	var filters []string
	if params.YearFrom > 0 {
		filters = append(filters, fmt.Sprintf("from_publication_date:%d-01-01", params.YearFrom))
	}
	if params.YearTo > 0 {
		filters = append(filters, fmt.Sprintf("to_publication_date:%d-12-31", params.YearTo))
	}
	if params.OpenAccess {
		filters = append(filters, "open_access.is_oa:true")
	}
	if sourceID := openAlexSourceID(params.Source); sourceID != "" {
		filters = append(filters, "primary_location.source.id:"+sourceID)
	}
	if len(filters) > 0 {
		q.Set("filter", strings.Join(filters, ","))
	}

	if params.SortBy == "date" {
		q.Set("sort", "publication_date:desc")
	}

	reqURL := p.baseURL + "/works?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openalex: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("openalex: rate limited")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("openalex: API error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("openalex: failed to read response: %w", err)
	}

	return parseOpenAlexResponse(body)
}

// SetBaseURL overrides API base URL (testing).
func (p *OpenAlexProvider) SetBaseURL(base string) { p.baseURL = base }

type openAlexResponse struct {
	Results []openAlexWork `json:"results"`
}

type openAlexWork struct {
	Title                  string                    `json:"display_name"`
	DOI                    string                    `json:"doi"`
	PublicationYear        int                       `json:"publication_year"`
	CitedByCount           int                       `json:"cited_by_count"`
	Authorships            []openAlexAuthorship      `json:"authorships"`
	PrimaryLocation        *openAlexLocation         `json:"primary_location"`
	OpenAccess             openAlexOA                `json:"open_access"`
	AbstractInvertedIndex  map[string][]int          `json:"abstract_inverted_index"`
}

type openAlexAuthorship struct {
	Author openAlexAuthor `json:"author"`
}

type openAlexAuthor struct {
	DisplayName string `json:"display_name"`
}

type openAlexLocation struct {
	Source *openAlexSource `json:"source"`
}

type openAlexSource struct {
	DisplayName string `json:"display_name"`
}

type openAlexOA struct {
	IsOA  bool   `json:"is_oa"`
	OAUrl string `json:"oa_url"`
}

func parseOpenAlexResponse(data []byte) ([]AcademicResult, error) {
	var resp openAlexResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("openalex: failed to parse response: %w", err)
	}

	results := make([]AcademicResult, 0, len(resp.Results))
	for _, work := range resp.Results {
		if work.Title == "" {
			continue
		}

		result := AcademicResult{
			Title:         work.Title,
			Year:          work.PublicationYear,
			CitationCount: work.CitedByCount,
			OpenAccess:    work.OpenAccess.IsOA,
			Source:        "openalex",
		}

		if work.DOI != "" {
			result.DOI = strings.TrimPrefix(work.DOI, "https://doi.org/")
			result.URL = work.DOI
		}

		if work.OpenAccess.OAUrl != "" {
			result.PDFUrl = work.OpenAccess.OAUrl
		}

		if work.PrimaryLocation != nil && work.PrimaryLocation.Source != nil {
			result.Journal = work.PrimaryLocation.Source.DisplayName
		}

		for _, a := range work.Authorships {
			if a.Author.DisplayName != "" {
				result.Authors = append(result.Authors, a.Author.DisplayName)
			}
		}

		result.Abstract = reconstructAbstract(work.AbstractInvertedIndex)
		result.Abstract = truncateText(result.Abstract, 500)

		if result.URL == "" && result.DOI != "" {
			result.URL = "https://doi.org/" + result.DOI
		}

		results = append(results, result)
	}

	return results, nil
}

// openAlexSourceID maps user-friendly source names to OpenAlex source IDs.
func openAlexSourceID(source string) string {
	switch strings.ToLower(source) {
	case "arxiv":
		return "S4306400194"
	case "pubmed":
		return "S4306525036"
	case "ieee":
		return "S202467917"
	case "nature":
		return "S137773608"
	case "springer":
		return "S70931966"
	default:
		return ""
	}
}

// reconstructAbstract rebuilds abstract text from OpenAlex's inverted index format.
// The inverted index maps each word to its position(s) in the text.
func reconstructAbstract(index map[string][]int) string {
	if len(index) == 0 {
		return ""
	}

	type wordPos struct {
		word string
		pos  int
	}

	var pairs []wordPos
	for word, positions := range index {
		for _, pos := range positions {
			pairs = append(pairs, wordPos{word: word, pos: pos})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].pos < pairs[j].pos
	})

	words := make([]string, len(pairs))
	for i, p := range pairs {
		words[i] = p.word
	}

	return strings.Join(words, " ")
}

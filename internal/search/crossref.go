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

// CrossRefProvider searches the CrossRef REST API for scholarly works.
// Coverage: 140M+ DOI-registered works, 99.94% uptime, authoritative metadata.
type CrossRefProvider struct {
	email   string
	baseURL string
	deps    Deps
}

// NewCrossRefProvider creates a CrossRef provider using the given email for polite pool access.
func NewCrossRefProvider(email string, deps Deps) *CrossRefProvider {
	return &CrossRefProvider{
		email:   email,
		baseURL: "https://api.crossref.org",
		deps:    deps,
	}
}

func (p *CrossRefProvider) Name() string { return "crossref" }

func (p *CrossRefProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biblio", "citations"},
		RateClass:    "free",
		Description:  "CrossRef — authoritative DOI metadata, 140M+ works, 99.94% uptime",
	}
}

func (p *CrossRefProvider) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	var results []AcademicResult
	err := p.deps.Breaker.Execute(func() error {
		var er error
		results, er = p.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (p *CrossRefProvider) doSearch(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	num := clamp(params.NumResults, 1, 25)

	q := url.Values{}
	q.Set("query", params.Query)
	q.Set("rows", fmt.Sprintf("%d", num))
	q.Set("mailto", p.email)

	var filters []string
	if params.YearFrom > 0 {
		filters = append(filters, fmt.Sprintf("from-pub-date:%d", params.YearFrom))
	}
	if params.YearTo > 0 {
		filters = append(filters, fmt.Sprintf("until-pub-date:%d", params.YearTo))
	}
	if len(filters) > 0 {
		q.Set("filter", strings.Join(filters, ","))
	}

	reqURL := p.baseURL + "/works?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("web-researcher-mcp/1.0 (mailto:%s)", p.email))

	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crossref: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("crossref: rate limited")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("crossref: API error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("crossref: failed to read response: %w", err)
	}

	return parseCrossRefResponse(body)
}

// SetBaseURL overrides API base URL (testing).
func (p *CrossRefProvider) SetBaseURL(base string) { p.baseURL = base }

type crossRefResponse struct {
	Message crossRefMessage `json:"message"`
}

type crossRefMessage struct {
	Items []crossRefItem `json:"items"`
}

type crossRefItem struct {
	DOI            string              `json:"DOI"`
	Title          []string            `json:"title"`
	Author         []crossRefAuthor    `json:"author"`
	ContainerTitle []string            `json:"container-title"`
	Published      *crossRefDateParts  `json:"published"`
	IssuedDate     *crossRefDateParts  `json:"issued"`
	ReferencedBy   int                 `json:"is-referenced-by-count"`
	Link           []crossRefLink      `json:"link"`
	Abstract       string              `json:"abstract"`
}

type crossRefAuthor struct {
	Given  string `json:"given"`
	Family string `json:"family"`
	Name   string `json:"name"`
}

type crossRefDateParts struct {
	DateParts [][]int `json:"date-parts"`
}

type crossRefLink struct {
	URL         string `json:"URL"`
	ContentType string `json:"content-type"`
}

func parseCrossRefResponse(data []byte) ([]AcademicResult, error) {
	var resp crossRefResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("crossref: failed to parse response: %w", err)
	}

	results := make([]AcademicResult, 0, len(resp.Message.Items))
	for _, item := range resp.Message.Items {
		if len(item.Title) == 0 || item.Title[0] == "" {
			continue
		}

		result := AcademicResult{
			Title:         item.Title[0],
			DOI:           item.DOI,
			CitationCount: item.ReferencedBy,
			Source:        "crossref",
		}

		if item.DOI != "" {
			result.URL = "https://doi.org/" + item.DOI
		}

		if len(item.ContainerTitle) > 0 {
			result.Journal = item.ContainerTitle[0]
		}

		// Extract publication year
		dateParts := item.Published
		if dateParts == nil {
			dateParts = item.IssuedDate
		}
		if dateParts != nil && len(dateParts.DateParts) > 0 && len(dateParts.DateParts[0]) > 0 {
			result.Year = dateParts.DateParts[0][0]
		}

		for _, a := range item.Author {
			name := formatCrossRefAuthor(a)
			if name != "" {
				result.Authors = append(result.Authors, name)
			}
		}

		// Find PDF link
		for _, link := range item.Link {
			if strings.Contains(link.ContentType, "pdf") && link.URL != "" {
				result.PDFUrl = link.URL
				break
			}
		}

		if item.Abstract != "" {
			result.Abstract = truncateText(cleanCrossRefAbstract(item.Abstract), 500)
		}

		results = append(results, result)
	}

	return results, nil
}

func formatCrossRefAuthor(a crossRefAuthor) string {
	if a.Name != "" {
		return a.Name
	}
	parts := []string{}
	if a.Given != "" {
		parts = append(parts, a.Given)
	}
	if a.Family != "" {
		parts = append(parts, a.Family)
	}
	return strings.Join(parts, " ")
}

// cleanCrossRefAbstract strips JATS XML tags from CrossRef abstracts.
func cleanCrossRefAbstract(text string) string {
	text = strings.ReplaceAll(text, "<jats:p>", "")
	text = strings.ReplaceAll(text, "</jats:p>", " ")
	text = strings.ReplaceAll(text, "<jats:title>", "")
	text = strings.ReplaceAll(text, "</jats:title>", " ")
	text = strings.ReplaceAll(text, "<jats:italic>", "")
	text = strings.ReplaceAll(text, "</jats:italic>", "")
	text = strings.ReplaceAll(text, "<jats:bold>", "")
	text = strings.ReplaceAll(text, "</jats:bold>", "")
	text = strings.ReplaceAll(text, "<jats:sup>", "")
	text = strings.ReplaceAll(text, "</jats:sup>", "")
	text = strings.ReplaceAll(text, "<jats:sub>", "")
	text = strings.ReplaceAll(text, "</jats:sub>", "")
	text = strings.TrimSpace(text)
	// Collapse multiple spaces
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return text
}

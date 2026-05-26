package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type PatentResult struct {
	Title    string `json:"title"`
	Number   string `json:"number"`
	URL      string `json:"url"`
	Abstract string `json:"abstract"`
	Assignee string `json:"assignee"`
	Inventor string `json:"inventor,omitempty"`
	Filed    string `json:"filed"`
	Granted  string `json:"granted,omitempty"`
	PDF      string `json:"pdf,omitempty"`
	Status   string `json:"status,omitempty"`
}

type PatentSearchParams struct {
	Query        string
	Assignee     string
	Inventor     string
	CPCCode      string
	PatentOffice string
	YearFrom     int
	YearTo       int
	NumResults   int
}

// BuildGooglePatentsURL constructs a Google Patents search URL with native parameters.
// Useful for generating a link the user can open in a browser.
func BuildGooglePatentsURL(params PatentSearchParams) string {
	u := url.URL{
		Scheme: "https",
		Host:   "patents.google.com",
		Path:   "/",
	}

	q := u.Query()
	q.Set("q", params.Query)

	if params.Assignee != "" {
		q.Set("assignee", params.Assignee)
	}
	if params.Inventor != "" {
		q.Set("inventor", params.Inventor)
	}
	if params.CPCCode != "" {
		q.Set("q", params.Query+" cpc:"+params.CPCCode)
	}
	if params.PatentOffice != "" && params.PatentOffice != "all" {
		q.Set("country", params.PatentOffice)
	}
	if params.YearFrom > 0 {
		q.Set("after", fmt.Sprintf("priority:%d0101", params.YearFrom))
	}
	if params.YearTo > 0 {
		q.Set("before", fmt.Sprintf("priority:%d1231", params.YearTo))
	}

	num := params.NumResults
	if num <= 0 {
		num = 10
	}
	q.Set("num", fmt.Sprintf("%d", num))
	q.Set("oq", params.Query)

	u.RawQuery = q.Encode()
	return u.String()
}

// ScrapeGooglePatents is no longer used for search discovery (Google Patents SPA
// doesn't render in headless). Instead, the patent tool uses ScrapePatentDetail
// to enrich individual patent pages after finding patent numbers via web search.
func (p *Pipeline) ScrapeGooglePatents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	return nil, fmt.Errorf("google patents search page requires JavaScript rendering; use web search to discover patent numbers then ScrapePatentDetail for enrichment")
}

// ScrapePatentDetail fetches a single Google Patents detail page and extracts
// structured data using microdata (itemprop) attributes and meta tags.
// Individual patent pages are server-rendered — no JavaScript needed.
func (p *Pipeline) ScrapePatentDetail(ctx context.Context, patentNumber string) (*PatentResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	patentURL := "https://patents.google.com/patent/" + patentNumber + "/en"

	client := newStealthClient(p.config.AllowPrivateIPs)
	req, err := http.NewRequestWithContext(ctx, "GET", patentURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("patent detail fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("patent detail returned HTTP %d for %s", resp.StatusCode, patentNumber)
	}

	reader, err := decompressBody(resp)
	if err != nil {
		return nil, err
	}
	if closer, ok := reader.(io.Closer); ok && closer != resp.Body {
		defer closer.Close()
	}

	body, err := io.ReadAll(io.LimitReader(reader, 3*1024*1024))
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	return parsePatentDetailPage(doc, patentNumber, patentURL), nil
}

func parsePatentDetailPage(doc *goquery.Document, number, patentURL string) *PatentResult {
	result := &PatentResult{
		Number: number,
		URL:    patentURL,
	}

	// Title from DC.title meta tag
	if title, exists := doc.Find(`meta[name="DC.title"]`).Attr("content"); exists {
		result.Title = strings.TrimSpace(title)
	}

	// Abstract from DC.description meta tag or itemprop="abstract"
	if abstract, exists := doc.Find(`meta[name="DC.description"]`).Attr("content"); exists {
		result.Abstract = strings.TrimSpace(abstract)
		if len(result.Abstract) > 500 {
			result.Abstract = result.Abstract[:500] + "..."
		}
	}
	if result.Abstract == "" {
		abstractSection := doc.Find(`section[itemprop="abstract"]`)
		if abstractSection.Length() > 0 {
			result.Abstract = strings.TrimSpace(abstractSection.Find("div.abstract").Text())
			if len(result.Abstract) > 500 {
				result.Abstract = result.Abstract[:500] + "..."
			}
		}
	}

	// Assignee from itemprop
	assigneeOriginal := doc.Find(`dd[itemprop="assigneeOriginal"]`)
	if assigneeOriginal.Length() > 0 {
		result.Assignee = strings.TrimSpace(assigneeOriginal.First().Text())
	}
	if result.Assignee == "" {
		assigneeCurrent := doc.Find(`dd[itemprop="assigneeCurrent"]`)
		if assigneeCurrent.Length() > 0 {
			result.Assignee = strings.TrimSpace(assigneeCurrent.First().Text())
		}
	}

	// Filing date
	filingDate := doc.Find(`dd[itemprop="filingDate"]`)
	if filingDate.Length() > 0 {
		result.Filed = strings.TrimSpace(filingDate.First().Text())
	}

	// Grant date from events
	doc.Find(`dd[itemprop="events"]`).Each(func(_ int, s *goquery.Selection) {
		eventType := s.Find(`span[itemprop="type"]`).Text()
		if strings.Contains(strings.ToLower(eventType), "grant") {
			eventDate := s.Find(`time[itemprop="date"]`).Text()
			if eventDate != "" {
				result.Granted = strings.TrimSpace(eventDate)
			}
		}
	})

	return result
}

var patentNumberRegex = regexp.MustCompile(`(?:^|/patent/)([A-Z]{2}\d[\dA-Z]+)`)

func ExtractPatentNumberFromURL(urlStr string) string {
	matches := patentNumberRegex.FindStringSubmatch(urlStr)
	if len(matches) >= 2 {
		num := matches[1]
		if idx := strings.Index(num, "/"); idx > 0 {
			num = num[:idx]
		}
		return num
	}

	parts := strings.Split(urlStr, "/patent/")
	if len(parts) >= 2 {
		number := parts[1]
		if idx := strings.Index(number, "/"); idx > 0 {
			number = number[:idx]
		}
		if idx := strings.Index(number, "?"); idx > 0 {
			number = number[:idx]
		}
		return number
	}
	return ""
}

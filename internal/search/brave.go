package search

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Compile-time assertions: BraveProvider satisfies Provider, LocalProvider,
// and ContextProvider so a single construction serves all capability families.
var _ Provider = (*BraveProvider)(nil)
var _ LocalProvider = (*BraveProvider)(nil)
var _ ContextProvider = (*BraveProvider)(nil)

// BraveConfig holds Brave-specific provider configuration knobs.
type BraveConfig struct {
	// ExtraSnippets requests up to 5 additional text snippets per web result
	// (extra_snippets=1). Disabled by default because it increases response size
	// significantly. Enable with BRAVE_EXTRA_SNIPPETS=true.
	ExtraSnippets bool
}

type BraveProvider struct {
	apiKey string
	config BraveConfig
	deps   Deps
}

func NewBraveProvider(apiKey string, cfg BraveConfig, deps Deps) *BraveProvider {
	return &BraveProvider{apiKey: apiKey, config: cfg, deps: deps}
}

func (b *BraveProvider) Name() string { return "brave" }

func (b *BraveProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := b.deps.Breaker.Execute(func() error {
		var e error
		results, e = b.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (b *BraveProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	var results []ImageResult
	err := b.deps.Breaker.Execute(func() error {
		var e error
		results, e = b.doImageSearch(ctx, params)
		return e
	})
	return results, err
}

func (b *BraveProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := b.deps.Breaker.Execute(func() error {
		var e error
		results, e = b.doNewsSearch(ctx, params)
		return e
	})
	return results, err
}

func (b *BraveProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", buildQuery(params))
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 20)))

	if params.Offset > 0 {
		q.Set("offset", strconv.Itoa(params.Offset))
	}
	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Language != "" {
		q.Set("search_lang", params.Language)
	}
	if params.TimeRange != "" {
		if fs := mapBraveFreshness(params.TimeRange); fs != "" {
			q.Set("freshness", fs)
		}
	}
	if params.Safe != "" && params.Safe != "off" {
		q.Set("safesearch", "moderate")
	}
	if b.config.ExtraSnippets {
		q.Set("extra_snippets", "1")
	}
	if params.ResultFilter != "" {
		q.Set("result_filter", params.ResultFilter)
	}
	if params.GoggleURL != "" {
		q.Set("goggles_id", params.GoggleURL)
	}

	apiURL := "https://api.search.brave.com/res/v1/web/search?" + q.Encode()
	body, err := b.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp braveWebResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	var results []SearchResult
	for _, r := range resp.Web.Results {
		results = append(results, SearchResult{
			Title:         r.Title,
			URL:           r.URL,
			Snippet:       r.Description,
			DisplayLink:   r.URL,
			ExtraSnippets: r.ExtraSnippets,
		})
	}
	return results, nil
}

func (b *BraveProvider) doImageSearch(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Size != "" {
		q.Set("size", params.Size)
	}
	if params.Type != "" {
		q.Set("type", params.Type)
	}

	apiURL := "https://api.search.brave.com/res/v1/images/search?" + q.Encode()
	body, err := b.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp braveImageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse brave image response: %w", err)
	}

	var results []ImageResult
	for _, r := range resp.Results {
		results = append(results, ImageResult{
			Title:         r.Title,
			Link:          r.URL,
			ThumbnailLink: r.Thumbnail.Src,
			DisplayLink:   r.Source,
		})
	}
	return results, nil
}

func (b *BraveProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 20)))

	if params.Freshness != "" {
		if fs := mapBraveFreshness(params.Freshness); fs != "" {
			q.Set("freshness", fs)
		}
	}

	apiURL := "https://api.search.brave.com/res/v1/news/search?" + q.Encode()
	body, err := b.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp braveNewsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse brave news response: %w", err)
	}

	var results []NewsResult
	for _, r := range resp.Results {
		results = append(results, NewsResult{
			Title:         r.Title,
			URL:           r.URL,
			Source:        r.MetaURL.Hostname,
			PublishedAt:   normalizePublishedAt(r.Age, time.Now()),
			Snippet:       r.Description,
			ExtraSnippets: r.ExtraSnippets,
		})
	}
	return results, nil
}

func (b *BraveProvider) doRequest(ctx context.Context, apiURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", b.apiKey)

	resp, err := b.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Brave gzip-encodes every response (errors included) when we send
	// Accept-Encoding: gzip, so decompress before reading either path —
	// otherwise error bodies surface as unreadable binary.
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("brave: failed to decompress response: %w", err)
		}
		defer gr.Close()
		reader = gr
	}

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("brave API rate limited")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(reader, 1024))
		return nil, fmt.Errorf("brave API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return io.ReadAll(io.LimitReader(reader, 5*1024*1024))
}

func (b *BraveProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:     []string{"*"},
		RateClass:   "paid",
		Description: "Brave Search — privacy-focused index with local POI search via the three-call pipeline (web/search?result_filter=locations → local/pois → local/descriptions).",
	}
}

// Local implements LocalSearcher via Brave's three-call local pipeline:
//  1. web/search?result_filter=locations to collect ephemeral location IDs
//  2. local/pois?ids=… to fetch POI details (address, phone, rating, hours)
//  3. local/descriptions?ids=… for AI-generated descriptions (best-effort)
//
// IDs are ephemeral — never persisted beyond the request lifecycle.
func (b *BraveProvider) Local(ctx context.Context, params LocalSearchParams) ([]LocalResult, error) {
	// Step 1: web search restricted to location results to collect ephemeral IDs.
	query := params.Query
	if params.Near != "" {
		query = query + " near " + params.Near
	}

	q := url.Values{}
	q.Set("q", query)
	n := params.NumResults
	if n <= 0 {
		n = 5
	}
	if n > 20 {
		n = 20
	}
	q.Set("count", strconv.Itoa(n))
	q.Set("result_filter", "locations")
	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Units != "" {
		q.Set("units", params.Units)
	}

	webData, err := b.doRequest(ctx, "https://api.search.brave.com/res/v1/web/search?"+q.Encode())
	if err != nil {
		return nil, err
	}

	var webResp struct {
		Locations struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(webData, &webResp); err != nil {
		return nil, fmt.Errorf("brave local: failed to parse location IDs: %w", err)
	}

	ids := make([]string, 0, len(webResp.Locations.Results))
	for _, r := range webResp.Locations.Results {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return []LocalResult{}, nil
	}
	// The locations filter ignores `count` and returns far more IDs than
	// requested, so cap to the caller's NumResults here (also keeps the
	// POI request URL well under Brave's length limit). n is already
	// clamped to 1-20 above.
	if len(ids) > n {
		ids = ids[:n]
	}

	// Brave requires each ID as its own repeated `ids=` query parameter
	// (ids=a&ids=b). A single comma-joined value yields a null POI result.
	idQuery := url.Values{}
	for _, id := range ids {
		idQuery.Add("ids", id)
	}
	idParam := idQuery.Encode()

	// Step 2: fetch POI details for the collected IDs.
	poisData, err := b.doRequest(ctx, "https://api.search.brave.com/res/v1/local/pois?"+idParam)
	if err != nil {
		return nil, err
	}

	// Step 3: fetch AI-generated descriptions (best-effort — don't fail the call).
	descData, _ := b.doRequest(ctx, "https://api.search.brave.com/res/v1/local/descriptions?"+idParam)

	var poisResp struct {
		Results []struct {
			ID            string    `json:"id"`
			Title         string    `json:"title"`
			Website       string    `json:"url"`
			Coordinates   []float64 `json:"coordinates"` // [latitude, longitude]
			PostalAddress struct {
				DisplayAddress string `json:"displayAddress"`
			} `json:"postal_address"`
			Contact struct {
				Telephone string `json:"telephone"`
			} `json:"contact"`
			Categories []string `json:"categories"`
			Rating     struct {
				RatingValue float64 `json:"ratingValue"`
				ReviewCount int     `json:"reviewCount"`
			} `json:"rating"`
			OpeningHours struct {
				CurrentDay []braveDayHours   `json:"current_day"`
				Days       [][]braveDayHours `json:"days"`
			} `json:"opening_hours"`
		} `json:"results"`
	}
	if err := json.Unmarshal(poisData, &poisResp); err != nil {
		return nil, fmt.Errorf("brave local: failed to parse POI details: %w", err)
	}

	// Build a description map from the best-effort descriptions response.
	descMap := map[string]string{}
	if descData != nil {
		var descResp struct {
			Results []struct {
				ID          string `json:"id"`
				Description string `json:"description"`
			} `json:"results"`
		}
		if err := json.Unmarshal(descData, &descResp); err == nil {
			for _, d := range descResp.Results {
				if d.Description != "" {
					descMap[d.ID] = d.Description
				}
			}
		}
	}

	results := make([]LocalResult, 0, len(poisResp.Results))
	for _, p := range poisResp.Results {
		var lat, lon float64
		if len(p.Coordinates) >= 2 {
			lat, lon = p.Coordinates[0], p.Coordinates[1]
		}

		results = append(results, LocalResult{
			ID:          p.ID,
			Name:        p.Title,
			Address:     p.PostalAddress.DisplayAddress,
			Lat:         lat,
			Lon:         lon,
			Phone:       p.Contact.Telephone,
			Website:     p.Website,
			Categories:  p.Categories,
			Rating:      p.Rating.RatingValue,
			RatingCount: p.Rating.ReviewCount,
			Hours:       braveFormatHours(p.OpeningHours.CurrentDay, p.OpeningHours.Days),
			Description: descMap[p.ID],
			Source:      "brave",
		})
	}
	return results, nil
}

// braveDayHours is one open segment for a single day in Brave's structured
// opening_hours block (a day may have several segments, e.g. a lunch break).
type braveDayHours struct {
	FullName string `json:"full_name"` // e.g. "Thursday"
	Opens    string `json:"opens"`     // "HH:MM"
	Closes   string `json:"closes"`    // "HH:MM"
}

// braveFormatHours flattens Brave's structured opening_hours (today's
// current_day plus the remaining days) into human-readable "Day: HH:MM-HH:MM"
// strings, preserving the API's day ordering.
func braveFormatHours(current []braveDayHours, days [][]braveDayHours) []string {
	var out []string
	add := func(segs []braveDayHours) {
		for _, s := range segs {
			if s.FullName == "" {
				continue
			}
			out = append(out, fmt.Sprintf("%s: %s-%s", s.FullName, s.Opens, s.Closes))
		}
	}
	add(current)
	for _, d := range days {
		add(d)
	}
	return out
}

// Context implements ContextSearcher via Brave's /res/v1/llm/context endpoint.
// It returns a server-assembled grounding text with per-snippet provenance for
// RAG/grounding workflows. Requires a Brave Data for AI plan that includes this
// endpoint; if the plan does not cover it, the API returns a 403 and this method
// returns an error — the caller (search_and_scrape) falls through to normal
// per-page scraping.
func (b *BraveProvider) Context(ctx context.Context, params ContextParams) (*ContextResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	if params.MaxTokens > 0 {
		q.Set("max_tokens", strconv.Itoa(params.MaxTokens))
	}
	if params.Threshold != "" {
		q.Set("threshold", params.Threshold)
	}
	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Language != "" {
		q.Set("lang", params.Language)
	}

	var result *ContextResult
	err := b.deps.Breaker.Execute(func() error {
		data, err := b.doRequest(ctx, "https://api.search.brave.com/res/v1/llm/context?"+q.Encode())
		if err != nil {
			return err
		}

		// Brave's /llm/context response has no top-level assembled string. It
		// returns grounding.generic[] (each url/title plus an ordered snippets[]
		// of plain-text excerpts) and a sources map keyed by URL (title/hostname/
		// age/snippet). We assemble the grounding text ourselves from the generic
		// snippets and carry each as a provenance-bearing ContextSnippet.
		var resp struct {
			Grounding struct {
				Generic []struct {
					URL      string   `json:"url"`
					Title    string   `json:"title"`
					Snippets []string `json:"snippets"`
				} `json:"generic"`
			} `json:"grounding"`
			Sources map[string]struct {
				Title string `json:"title"`
				Age   []any  `json:"age"` // ["Friday, May 30, 2025", "2025-05-30", "383 days ago"]
			} `json:"sources"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("brave context: failed to parse response: %w", err)
		}

		// braveContextAge picks the ISO-ish middle element of the age tuple when
		// present (the human "X days ago" form is the least useful for callers).
		braveContextAge := func(url string) string {
			s, ok := resp.Sources[url]
			if !ok || len(s.Age) < 2 {
				return ""
			}
			if iso, ok := s.Age[1].(string); ok {
				return iso
			}
			return ""
		}

		var b strings.Builder
		snippets := make([]ContextSnippet, 0, len(resp.Grounding.Generic))
		for _, g := range resp.Grounding.Generic {
			text := strings.Join(g.Snippets, " ")
			if text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(text)
			snippets = append(snippets, ContextSnippet{
				Title:  g.Title,
				URL:    g.URL,
				Age:    braveContextAge(g.URL),
				Text:   text,
				Source: "brave",
			})
		}

		result = &ContextResult{
			Context:  b.String(),
			Snippets: snippets,
			Source:   "brave",
		}
		return nil
	})
	return result, err
}

func mapBraveFreshness(tr string) string {
	// Custom date range: "YYYY-MM-DD..YYYY-MM-DD" → "YYYY-MM-DDtoYYYY-MM-DD"
	if strings.Contains(tr, "..") {
		parts := strings.SplitN(tr, "..", 2)
		if len(parts) == 2 {
			from, to := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			if from != "" && to != "" {
				return from + "to" + to
			}
		}
	}
	switch tr {
	case "hour":
		return "pd"
	case "day":
		return "pd"
	case "week":
		return "pw"
	case "month":
		return "pm"
	case "year":
		return "py"
	default:
		return ""
	}
}

type braveWebResponse struct {
	Web struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Description   string   `json:"description"`
			ExtraSnippets []string `json:"extra_snippets"`
		} `json:"results"`
	} `json:"web"`
}

type braveImageResponse struct {
	Results []struct {
		Title     string `json:"title"`
		URL       string `json:"url"`
		Source    string `json:"source"`
		Thumbnail struct {
			Src string `json:"src"`
		} `json:"thumbnail"`
	} `json:"results"`
}

type braveNewsResponse struct {
	Results []struct {
		Title         string   `json:"title"`
		URL           string   `json:"url"`
		Description   string   `json:"description"`
		Age           string   `json:"age"`
		ExtraSnippets []string `json:"extra_snippets"`
		MetaURL       struct {
			Hostname string `json:"hostname"`
		} `json:"meta_url"`
	} `json:"results"`
}

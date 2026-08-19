package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// FREDProvider implements EconSearcher over the Federal Reserve Economic Data
// (FRED) API: 800K+ macroeconomic time series (GDP, CPI, unemployment, rates).
// Requires a free API key.
//
// Endpoints (FRED API, file_type=json):
//   - series search:  /fred/series/search?search_text=
//     → {seriess:[{id,title,units,frequency,last_updated,notes}]}
//   - observations:   /fred/series/observations?series_id=
//     → {observations:[{date,value}]}
//   - series detail:  /fred/series?series_id= → {seriess:[{…}]}
type FREDProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

// NewFREDProvider creates the provider. apiKey must be non-empty (FRED requires it).
func NewFREDProvider(apiKey string, deps Deps) *FREDProvider {
	return &FREDProvider{
		apiKey:  apiKey,
		baseURL: "https://api.stlouisfed.org/fred",
		deps:    deps,
	}
}

func (f *FREDProvider) Name() string { return "fred" }

func (f *FREDProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"US", "*"},
		Capabilities: []string{"search", "timeseries", "macro"},
		RateClass:    "free",
		Description:  "FRED (St. Louis Fed) — 800K+ macroeconomic time series (GDP, CPI, unemployment, rates)",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (f *FREDProvider) SetBaseURL(base string) { f.baseURL = base }

func (f *FREDProvider) Econ(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	var results []EconResult
	err := f.deps.Breaker.Execute(func() error {
		var er error
		results, er = f.doEcon(ctx, params)
		return er
	})
	return results, err
}

func (f *FREDProvider) doEcon(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	if params.SeriesID != "" {
		return f.observations(ctx, params)
	}
	return f.seriesSearch(ctx, params)
}

// seriesSearch fetches FRED's own popularity-ranked results (order_by=popularity,
// sort_order=desc — see #434) so a canonical, heavily-referenced series (e.g.
// "GDP") isn't buried behind a series that merely matches the query text more
// closely by coincidence, then re-sorts client-side by query/title term overlap
// (see reorderByRelevance, #595): FRED's API offers no combined
// field-scope+popularity ordering, so a purely site-wide-popular but
// topically-unrelated series (e.g. CPIAUCSL for a "US unemployment rate"
// query) no longer outranks the topically-matching one (UNRATE). Popularity
// remains the tiebreak when term overlap is equal — preserving the original
// #434 fix for acronym queries like "GDP" that don't literally appear as a
// word in any candidate's spelled-out title.
func (f *FREDProvider) seriesSearch(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 25)
	q := f.baseParams()
	q.Set("search_text", params.Query)
	q.Set("limit", strconv.Itoa(num))
	q.Set("order_by", "popularity")
	q.Set("sort_order", "desc")

	body, err := f.get(ctx, "/series/search?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Seriess []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Units       string `json:"units"`
			Frequency   string `json:"frequency"`
			LastUpdated string `json:"last_updated"`
			Notes       string `json:"notes"`
			Popularity  int    `json:"popularity"`
		} `json:"seriess"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("fred: series search parse: %w", err)
	}
	out := make([]EconResult, 0, len(resp.Seriess))
	for _, s := range resp.Seriess {
		out = append(out, EconResult{
			SeriesID:    s.ID,
			Title:       s.Title,
			Units:       s.Units,
			Frequency:   s.Frequency,
			LastUpdated: s.LastUpdated,
			Notes:       truncateText(s.Notes, 500),
			Popularity:  s.Popularity,
			Source:      "fred",
		})
	}
	reorderByRelevance(out, params.Query)
	return out, nil
}

// reorderByRelevance stable-sorts results by descending count of distinct
// query words (length >= 3) that appear as whole words in the title, falling
// back to FRED's own popularity ordering (already reflected in the input
// order) when two results tie on term overlap — see seriesSearch (#595).
func reorderByRelevance(results []EconResult, query string) {
	queryWords := fredSignificantWords(query)
	if len(queryWords) == 0 {
		return
	}
	overlap := make([]int, len(results))
	for i, r := range results {
		titleWords := fredSignificantWords(r.Title)
		n := 0
		for w := range queryWords {
			if _, ok := titleWords[w]; ok {
				n++
			}
		}
		overlap[i] = n
	}
	idx := make([]int, len(results))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return overlap[idx[i]] > overlap[idx[j]]
	})
	reordered := make([]EconResult, len(results))
	for i, oi := range idx {
		reordered[i] = results[oi]
	}
	copy(results, reordered)
}

// fredSignificantWords lowercases s and returns its distinct words of length
// >= 3 as a set — a short, self-contained tokenizer scoped to this ranking
// heuristic (internal/search has no existing dependency on internal/content's
// claim-matching tokenizer, and this doesn't need its stemming/stopword rules).
func fredSignificantWords(s string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	set := make(map[string]struct{}, len(fields))
	for _, w := range fields {
		if len(w) >= 3 {
			set[w] = struct{}{}
		}
	}
	return set
}

func (f *FREDProvider) observations(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 100)
	q := f.baseParams()
	q.Set("series_id", params.SeriesID)
	q.Set("limit", strconv.Itoa(num))
	// Order so the `limit` window anchors where the caller asked. With a
	// `date_from`, ascending takes the first N observations FROM that date
	// (otherwise FRED filters to [date_from, end] then desc+limit hands back the
	// latest N, silently ignoring the anchor — #233). With no `date_from`,
	// descending gives the most-recent N (the "what's the latest value" case).
	if params.DateFrom != "" {
		q.Set("sort_order", "asc")
		q.Set("observation_start", params.DateFrom)
	} else {
		q.Set("sort_order", "desc")
	}
	if params.DateTo != "" {
		q.Set("observation_end", params.DateTo)
	}
	if params.Frequency != "" {
		q.Set("frequency", params.Frequency)
	}
	if params.Units != "" {
		q.Set("units", params.Units)
	}

	body, err := f.get(ctx, "/series/observations?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Observations []struct {
			Date  string `json:"date"`
			Value string `json:"value"` // FRED returns values as strings; "." = missing
		} `json:"observations"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("fred: observations parse: %w", err)
	}
	out := make([]EconResult, 0, len(resp.Observations))
	for _, o := range resp.Observations {
		r := EconResult{SeriesID: params.SeriesID, Date: o.Date, Source: "fred"}
		// "." denotes a missing observation; pass numeric values through exactly
		// (no rounding), flagging presence so a real 0.0 isn't read as missing.
		if o.Value != "." && o.Value != "" {
			if v, err := strconv.ParseFloat(o.Value, 64); err == nil {
				r.Value = v
				r.HasValue = true
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *FREDProvider) baseParams() url.Values {
	q := url.Values{}
	q.Set("api_key", f.apiKey) // never logged
	q.Set("file_type", "json")
	return q
}

func (f *FREDProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", f.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fred: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("fred: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("fred: access denied (check FRED_API_KEY)")
	}
	if resp.StatusCode == 400 {
		// FRED returns 400 for a bad/unknown series_id or malformed parameters —
		// NOT for a bad key (that is 403). Don't misdirect the user to their key.
		return nil, fmt.Errorf("fred: request rejected — check the series_id and parameters (search by keyword first to discover valid series IDs)")
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("fred: not found")
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("fred: API error %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

var _ EconProvider = (*FREDProvider)(nil)

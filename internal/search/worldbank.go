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

// WorldBankProvider implements EconSearcher over the World Bank Open Data
// (Indicators) API: development indicators (GDP, population, inflation, …) for
// 200+ economies. Keyless and free — complements FRED's US-only macro series
// with global, multi-country data behind the same EconProvider interface.
//
// Verified contract (2026):
//   - keyword mode has NO server-side search; we list the WDI indicator set
//     (source=2) once and filter client-side on the indicator name.
//   - observations:  /v2/country/{country}/indicator/{indicator}?format=json&date=from:to
//     → a 2-element JSON array: [0]=pagination metadata, [1]=[]observation.
//   - errors arrive as HTTP 200 with a {"message":[…]} object in element [0];
//     value is a nullable float; dates are strings ("2022", "2024Q4", "2020M01").
type WorldBankProvider struct {
	baseURL string
	deps    Deps
}

// NewWorldBankProvider creates the provider. No key required.
func NewWorldBankProvider(deps Deps) *WorldBankProvider {
	return &WorldBankProvider{
		baseURL: "https://api.worldbank.org/v2",
		deps:    deps,
	}
}

func (w *WorldBankProvider) Name() string { return "worldbank" }

func (w *WorldBankProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "timeseries", "macro", "development"},
		RateClass:    "free",
		Description:  "World Bank Open Data — development indicators (GDP, population, inflation, …) for 200+ economies",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (w *WorldBankProvider) SetBaseURL(base string) { w.baseURL = base }

func (w *WorldBankProvider) Econ(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	var results []EconResult
	err := w.deps.Breaker.Execute(func() error {
		var er error
		results, er = w.doEcon(ctx, params)
		return er
	})
	return results, err
}

func (w *WorldBankProvider) doEcon(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	if params.SeriesID != "" {
		return w.observations(ctx, params)
	}
	return w.indicatorSearch(ctx, params)
}

// indicatorSearch lists WDI indicators (source=2, the main ~1,500-indicator
// dataset) and filters them client-side by a case-insensitive name match — the
// API has no working text-search parameter. Returns the matches as series rows.
func (w *WorldBankProvider) indicatorSearch(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 25)
	q := url.Values{}
	q.Set("format", "json")
	q.Set("source", "2")      // World Development Indicators
	q.Set("per_page", "2000") // one page large enough to hold the whole WDI set

	body, err := w.get(ctx, "/indicator?"+q.Encode())
	if err != nil {
		return nil, err
	}

	// [0]=metadata (or error), [1]=[]indicator.
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("worldbank: indicator list parse: %w", err)
	}
	if apiErr := worldBankAPIError(raw); apiErr != nil {
		return nil, apiErr
	}
	if len(raw) < 2 {
		return nil, nil
	}

	var indicators []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Unit       string `json:"unit"`
		SourceNote string `json:"sourceNote"`
		Source     struct {
			Value string `json:"value"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw[1], &indicators); err != nil {
		return nil, fmt.Errorf("worldbank: indicators parse: %w", err)
	}

	needle := strings.ToLower(strings.TrimSpace(params.Query))
	out := make([]EconResult, 0, num)
	for _, ind := range indicators {
		if needle != "" && !strings.Contains(strings.ToLower(ind.Name), needle) {
			continue
		}
		out = append(out, EconResult{
			SeriesID: ind.ID,
			Title:    ind.Name,
			Units:    ind.Unit,
			Notes:    truncateText(ind.SourceNote, 500),
			Source:   "worldbank",
		})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

// observations fetches a single indicator's time series for a country (default
// WLD = World aggregate). Values pass through exactly as published (no rounding);
// a null observation carries no value (HasValue=false), matching FRED's contract.
func (w *WorldBankProvider) observations(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 100)
	country := strings.TrimSpace(params.Country)
	if country == "" {
		country = "WLD" // World aggregate when no country is specified
	}

	q := url.Values{}
	q.Set("format", "json")
	q.Set("per_page", strconv.Itoa(num))
	if date := worldBankDateRange(params.DateFrom, params.DateTo); date != "" {
		q.Set("date", date)
	}

	path := fmt.Sprintf("/country/%s/indicator/%s?%s",
		url.PathEscape(country), url.PathEscape(params.SeriesID), q.Encode())
	body, err := w.get(ctx, path)
	if err != nil {
		return nil, err
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("worldbank: observations parse: %w", err)
	}
	if apiErr := worldBankAPIError(raw); apiErr != nil {
		return nil, apiErr
	}
	if len(raw) < 2 || string(raw[1]) == "null" {
		return nil, nil // valid query, no data for the range
	}

	var obs []struct {
		Indicator struct {
			Value string `json:"value"`
		} `json:"indicator"`
		Country struct {
			Value string `json:"value"`
		} `json:"country"`
		CountryISO3 string   `json:"countryiso3code"`
		Date        string   `json:"date"`
		Value       *float64 `json:"value"`
		Unit        string   `json:"unit"`
	}
	if err := json.Unmarshal(raw[1], &obs); err != nil {
		return nil, fmt.Errorf("worldbank: observation rows parse: %w", err)
	}

	out := make([]EconResult, 0, len(obs))
	for _, o := range obs {
		r := EconResult{
			SeriesID: params.SeriesID,
			Title:    o.Indicator.Value,
			Units:    o.Unit,
			Date:     o.Date,
			Source:   "worldbank",
		}
		// Null = missing observation; pass real values through verbatim (no
		// rounding), flagging presence so a real 0.0 isn't read as missing.
		if o.Value != nil {
			r.Value = *o.Value
			r.HasValue = true
		}
		out = append(out, r)
	}
	return out, nil
}

// worldBankDateRange builds the API "from:to" date filter from the 4-digit years
// in the supplied dates. World Bank filters by year (or year-quarter/month), so
// we pass through the year portion; an empty range omits the filter (most-recent
// page is returned).
func worldBankDateRange(from, to string) string {
	yf, yt := worldBankYear(from), worldBankYear(to)
	switch {
	case yf != "" && yt != "":
		return yf + ":" + yt
	case yf != "":
		return yf + ":" + yf
	case yt != "":
		return yt + ":" + yt
	}
	return ""
}

// worldBankYear extracts a leading 4-digit year from a YYYY or YYYY-MM-DD date.
func worldBankYear(date string) string {
	date = strings.TrimSpace(date)
	if len(date) >= 4 && allDigitsWB(date[:4]) {
		return date[:4]
	}
	return ""
}

// allDigitsWB is a deliberate intra-package copy of content.allDigits — search
// must not import content for a 6-line helper, and the reverse would invert the
// package layering. The WB suffix marks the duplication as intentional.
func allDigitsWB(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// worldBankAPIError detects the API's HTTP-200 error envelope: a single-element
// array whose [0] carries a {"message":[{value}]} object. Returns nil when the
// response is a normal data/metadata pair.
func worldBankAPIError(raw []json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("worldbank: empty response")
	}
	var env struct {
		Message []struct {
			Value string `json:"value"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw[0], &env); err == nil && len(env.Message) > 0 {
		return fmt.Errorf("worldbank: %s", env.Message[0].Value)
	}
	return nil
}

func (w *WorldBankProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", w.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := w.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worldbank: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("worldbank: rate limited")
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("worldbank: API error %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

var _ EconProvider = (*WorldBankProvider)(nil)

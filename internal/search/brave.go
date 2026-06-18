package search

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Compile-time assertions: BraveProvider satisfies Provider, LocalProvider,
// and ContextProvider so a single construction serves all capability families.
var _ Provider = (*BraveProvider)(nil)
var _ LocalProvider = (*BraveProvider)(nil)
var _ ContextProvider = (*BraveProvider)(nil)

// braveAPIVersion pins the Brave API schema version (header "Api-Version",
// format YYYY-MM-DD). Brave gates backwards-incompatible response changes on
// this header; omitting it silently opts into "latest", so a future Brave
// change could break our parsing with no warning. Bump this constant
// deliberately only after validating our parsers against the newer schema.
const braveAPIVersion = "2023-01-01"

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

	// Brave's documented offset range is 0-9 (F8); clamp to avoid sending an
	// out-of-range value Brave rejects/ignores.
	if params.Offset > 0 {
		q.Set("offset", strconv.Itoa(clamp(params.Offset, 0, 9)))
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
	// F8: Brave web accepts off/moderate/strict — preserve the caller's level
	// instead of collapsing every non-off value to moderate.
	if ss := mapBraveWebSafe(params.Safe); ss != "" {
		q.Set("safesearch", ss)
	}
	if b.config.ExtraSnippets {
		q.Set("extra_snippets", "1")
	}
	if params.ResultFilter != "" {
		q.Set("result_filter", params.ResultFilter)
	}
	// F1: the param is `goggles` (string|string[]); `goggles_id` is deprecated and
	// on Brave's removal path. Append up to 3 as repeated params so lens stacking
	// composes (q.Add, not q.Set).
	for i, g := range params.Goggles {
		if i >= 3 {
			break
		}
		if g != "" {
			q.Add("goggles", g)
		}
	}

	apiURL := "https://api.search.brave.com/res/v1/web/search?" + q.Encode()
	body, err := b.doRequest(ctx, apiURL, withAPIVersion())
	if err != nil {
		return nil, err
	}

	var resp braveWebResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	// F8: surface Brave's pagination signal via the request-scoped result-meta
	// side channel when the tool layer installed one (nil-safe otherwise).
	resultMetaFromContext(ctx).setMoreResultsAvailable(resp.Query.MoreResultsAvailable)

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
	// F6: Brave images allow up to 200 (we honor the project ceiling of 200);
	// `size`/`type`/`color_type`/`dominant_color`/`file_type` are NOT documented
	// Brave image params, so we deliberately do not emit them (Brave drops them).
	// Those fields remain on ImageSearchParams for Google/SearchAPI, which honor them.
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 200)))

	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Language != "" {
		q.Set("search_lang", params.Language)
	}
	// F6: images have no `moderate` — map any non-off level to strict (Brave default).
	if ss := mapBraveImageSafe(params.Safe); ss != "" {
		q.Set("safesearch", ss)
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
	// F7: Brave news allows up to 50 (was silently clamped to 20).
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 50)))

	if params.Freshness != "" {
		if fs := mapBraveFreshness(params.Freshness); fs != "" {
			q.Set("freshness", fs)
		}
	}
	// F7: localize + language-scope news (previously dropped). sort_by/news_source
	// have no Brave backing, so they're honored only by Google and intentionally
	// not emitted here.
	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Language != "" {
		q.Set("search_lang", params.Language)
	}
	// Brave news accepts the full off|moderate|strict set (same as web).
	if ss := mapBraveWebSafe(params.Safe); ss != "" {
		q.Set("safesearch", ss)
	}
	// Brave news offset is 0–9, same as web (F8).
	if params.Offset > 0 {
		q.Set("offset", strconv.Itoa(clamp(params.Offset, 0, 9)))
	}
	// Gate extra_snippets behind the same config knob as web — the response side
	// already surfaces braveNewsResponse.ExtraSnippets.
	if b.config.ExtraSnippets {
		q.Set("extra_snippets", "1")
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

// braveReqOpt mutates an outbound Brave request before it is sent. It is the
// header-injection seam (F4): per-call headers (X-Loc-*, Cache-Control) ride on
// the request without disturbing the base Accept/token/Api-Version headers, and
// existing call sites stay unchanged because doRequest is variadic.
type braveReqOpt func(*http.Request)

// withHeader sets a single request header when the value is non-empty. Empty
// values are skipped so callers can pass through optional fields uniformly.
//
// Security: never use this to log header values — the subscription token and
// X-Loc-* (PII-adjacent location) must never appear in logs or errors.
func withHeader(k, v string) braveReqOpt {
	return func(r *http.Request) {
		if v != "" {
			r.Header.Set(k, v)
		}
	}
}

// withLocation attaches Brave's X-Loc-* location headers derived from the
// caller's coordinates or text fallback. Coordinates take precedence per the
// Brave reference; when absent, the text headers (city/state/country) are sent
// from Near/Country. This belongs on the step-1 web/search?result_filter=locations
// call ONLY — Brave's reference client sends no X-Loc-* on /local/pois.
func withLocation(p LocalSearchParams) braveReqOpt {
	return func(r *http.Request) {
		if p.Latitude != nil && p.Longitude != nil {
			r.Header.Set("X-Loc-Lat", strconv.FormatFloat(*p.Latitude, 'f', -1, 64))
			r.Header.Set("X-Loc-Long", strconv.FormatFloat(*p.Longitude, 'f', -1, 64))
		} else if p.Near != "" {
			// Text fallback: bias via the city header. The free-text Near can be a
			// city/neighborhood/region; Brave treats x-loc-city as the textual anchor.
			r.Header.Set("X-Loc-City", p.Near)
		}
		if p.Country != "" {
			r.Header.Set("X-Loc-Country", p.Country)
		}
	}
}

func (b *BraveProvider) doRequest(ctx context.Context, apiURL string, opts ...braveReqOpt) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", b.apiKey)
	for _, opt := range opts {
		opt(req)
	}

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

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(reader, 4096))
		return nil, braveError(resp.StatusCode, body)
	}

	return io.ReadAll(io.LimitReader(reader, 5*1024*1024))
}

// withNoCache sets Cache-Control: no-cache to request a best-effort cache bypass
// for freshness-critical calls (F10). Off by default — callers opt in.
func withNoCache() braveReqOpt {
	return withHeader("Cache-Control", "no-cache")
}

// withAPIVersion pins the Brave schema version (F10). It is scoped to the
// web-search product ONLY: that product accepts (and gates schema changes on)
// the Api-Version header, but the image, news, local (pois/descriptions), and
// llm/context products reject any explicit value — images/news with 404
// API_VERSION_NOT_FOUND, local/context with 422 — so the pin must NOT ride on
// those calls. Apply it only to /web/search requests (including the local
// pipeline's step-1 web/search?result_filter=locations call).
func withAPIVersion() braveReqOpt {
	return withHeader("Api-Version", braveAPIVersion)
}

// braveErrorResponse models Brave's structured error envelope (F10):
//
//	{ "type":"ErrorResponse",
//	  "error": { "id","status","code","detail","meta": {
//	      "plan","rate_limit","rate_current","quota_limit","quota_current","component" } },
//	  "time": ... }
//
// We parse it so a 429 distinguishes a per-second throttle (component/rate_limit)
// from monthly-quota exhaustion (quota_limit) instead of a generic "rate limited".
type braveErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		ID     string `json:"id"`
		Status int    `json:"status"`
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Meta   struct {
			Plan         string `json:"plan"`
			RateLimit    int    `json:"rate_limit"`
			RateCurrent  int    `json:"rate_current"`
			QuotaLimit   int    `json:"quota_limit"`
			QuotaCurrent int    `json:"quota_current"`
			Component    string `json:"component"`
		} `json:"meta"`
	} `json:"error"`
}

// braveError builds an actionable error from an HTTP status + (optionally
// structured) error body. The returned string always contains a token that
// isRateLimitError keys on ("rate limited", "429", or "quota") for any 429 or
// RATE_LIMITED code, so rate-limit classification survives the richer message.
// Security: the body is Brave's own error envelope (no secrets) — the
// subscription token is never echoed here.
func braveError(status int, body []byte) error {
	var er braveErrorResponse
	_ = json.Unmarshal(body, &er) // best-effort; fall back to raw body below

	if status == 429 || er.Error.Code == "RATE_LIMITED" {
		m := er.Error.Meta
		// Monthly quota exhaustion vs per-second throttle, when Brave tells us.
		if m.QuotaLimit > 0 && m.QuotaCurrent >= m.QuotaLimit {
			return fmt.Errorf("brave API monthly quota exhausted (plan=%s, quota %d/%d): rate limited",
				m.Plan, m.QuotaCurrent, m.QuotaLimit)
		}
		if m.Component != "" || m.RateLimit > 0 {
			return fmt.Errorf("brave API rate limited (component=%s, rate %d/%d per second, plan=%s)",
				m.Component, m.RateCurrent, m.RateLimit, m.Plan)
		}
		return fmt.Errorf("brave API rate limited")
	}

	if er.Error.Code != "" || er.Error.Detail != "" {
		return fmt.Errorf("brave API error %d [%s]: %s", status, er.Error.Code, er.Error.Detail)
	}
	return fmt.Errorf("brave API error %d: %s", status, strings.TrimSpace(string(body)))
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
//     (location anchored via X-Loc-* headers, NOT a query suffix — F2)
//  2. local/pois?ids=… to fetch POI details (address, phone, rating, hours)
//  3. local/descriptions?ids=… for AI-generated descriptions (best-effort)
//
// Steps 1+2 run inside the circuit breaker (F3); step 3 is best-effort and
// stays outside so a descriptions outage neither fails the call nor trips the
// breaker. When an anchor coordinate is supplied, POIs are distance-ranked
// nearest-first and optionally filtered by Radius (F2).
//
// IDs are ephemeral — never persisted beyond the request lifecycle.
func (b *BraveProvider) Local(ctx context.Context, params LocalSearchParams) ([]LocalResult, error) {
	var out []LocalResult
	err := b.deps.Breaker.Execute(func() error {
		ids, e := b.localIDs(ctx, params)
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			return nil
		}
		out, e = b.localPOIs(ctx, ids, params)
		return e
	})
	if err != nil || len(out) == 0 {
		if out == nil {
			out = []LocalResult{}
		}
		return out, err
	}

	// Step 3 (best-effort, outside the breaker): enrich with AI descriptions.
	b.attachDescriptions(ctx, out, params)

	// F2: distance-rank against the anchor coordinate when present.
	if params.Latitude != nil && params.Longitude != nil {
		out = rankByHaversine(out, *params.Latitude, *params.Longitude, params.Radius)
	}
	return out, nil
}

// localIDs runs step 1: a location-filtered web search that returns Brave's
// ephemeral location IDs. Location is anchored through X-Loc-* headers
// (coordinates or text fallback) — the query is NOT suffixed with "near …",
// which biases the text index rather than the POI geosystem (F2).
func (b *BraveProvider) localIDs(ctx context.Context, params LocalSearchParams) ([]string, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	n := clampLocalCount(params.NumResults)
	q.Set("count", strconv.Itoa(n))
	q.Set("result_filter", "locations")
	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Units != "" {
		q.Set("units", params.Units)
	}

	// Location headers ride on the step-1 call ONLY (Brave's reference client
	// sends no X-Loc-* on /local/pois). withLocation never logs header values.
	webData, err := b.doRequest(ctx,
		"https://api.search.brave.com/res/v1/web/search?"+q.Encode(),
		withAPIVersion(), withLocation(params))
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
	// The locations filter ignores `count` and returns far more IDs than
	// requested, so cap to the caller's NumResults here (also keeps the POI
	// request URL well under Brave's length limit). n is clamped to 1-20.
	if len(ids) > n {
		ids = ids[:n]
	}
	return ids, nil
}

// localPOIs runs step 2: fetch POI details for the collected IDs. Only ids and
// units (display) are valid here — no X-Loc-* and no country (Brave's reference
// client omits both on /local/pois).
func (b *BraveProvider) localPOIs(ctx context.Context, ids []string, params LocalSearchParams) ([]LocalResult, error) {
	q := url.Values{}
	for _, id := range ids {
		q.Add("ids", id) // repeated ids=; a comma-joined value yields a null result
	}
	if params.Units != "" {
		q.Set("units", params.Units)
	}

	poisData, err := b.doRequest(ctx, "https://api.search.brave.com/res/v1/local/pois?"+q.Encode())
	if err != nil {
		return nil, err
	}

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
			Source:      "brave",
		})
	}
	return results, nil
}

// attachDescriptions runs step 3 (best-effort): fetch AI-generated descriptions
// and merge them into the POIs by ID. Any failure is swallowed — descriptions
// are enrichment, not core data, and must not fail the overall call or trip the
// breaker (it runs outside Execute). Only ids are sent (no country, no X-Loc-*).
func (b *BraveProvider) attachDescriptions(ctx context.Context, results []LocalResult, params LocalSearchParams) {
	if len(results) == 0 {
		return
	}
	q := url.Values{}
	for _, r := range results {
		if r.ID != "" {
			q.Add("ids", r.ID)
		}
	}
	if params.Units != "" {
		q.Set("units", params.Units)
	}

	descData, err := b.doRequest(ctx, "https://api.search.brave.com/res/v1/local/descriptions?"+q.Encode())
	if err != nil || descData == nil {
		return
	}
	var descResp struct {
		Results []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.Unmarshal(descData, &descResp); err != nil {
		return
	}
	descMap := make(map[string]string, len(descResp.Results))
	for _, d := range descResp.Results {
		if d.Description != "" {
			descMap[d.ID] = d.Description
		}
	}
	for i := range results {
		if desc, ok := descMap[results[i].ID]; ok {
			results[i].Description = desc
		}
	}
}

// clampLocalCount normalizes NumResults to Brave's 1-20 local range (default 5).
func clampLocalCount(n int) int {
	if n <= 0 {
		return 5
	}
	return clamp(n, 1, 20)
}

// rankByHaversine sorts POIs nearest-first relative to an anchor coordinate and,
// when radiusMeters > 0, drops any POI farther than that radius. POIs missing
// coordinates (Lat==0 && Lon==0) are treated as unrankable: they sort after all
// ranked POIs and are excluded entirely when a radius filter is active (we can't
// prove they fall inside it). Returns the filtered, ordered slice (F2).
func rankByHaversine(results []LocalResult, lat, lon, radiusMeters float64) []LocalResult {
	type ranked struct {
		r      LocalResult
		dist   float64
		hasGeo bool
	}
	scored := make([]ranked, 0, len(results))
	for _, r := range results {
		hasGeo := r.Lat != 0 || r.Lon != 0
		var d float64
		if hasGeo {
			d = haversineMeters(lat, lon, r.Lat, r.Lon)
		}
		if radiusMeters > 0 && (!hasGeo || d > radiusMeters) {
			continue // outside the radius, or unprovable without coords
		}
		scored = append(scored, ranked{r: r, dist: d, hasGeo: hasGeo})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		// Geo-bearing POIs always precede coordinate-less ones; among geo POIs,
		// nearest first. Stable sort preserves Brave's order within ties.
		if scored[i].hasGeo != scored[j].hasGeo {
			return scored[i].hasGeo
		}
		return scored[i].dist < scored[j].dist
	})
	out := make([]LocalResult, len(scored))
	for i, s := range scored {
		out[i] = s.r
	}
	return out
}

// haversineMeters returns the great-circle distance in meters between two
// lat/lon points (decimal degrees). Stdlib math only — no new dependency.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusM = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
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
	// F5: Brave's /llm/context param names differ from our old spellings —
	// max_tokens/threshold/lang were silently dropped. Use the documented names.
	q := url.Values{}
	q.Set("q", params.Query)
	if params.MaxTokens > 0 {
		q.Set("maximum_number_of_tokens", strconv.Itoa(params.MaxTokens))
	}
	if params.ThresholdMode != "" {
		q.Set("context_threshold_mode", params.ThresholdMode)
	}
	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Language != "" {
		q.Set("search_lang", params.Language)
	}
	if params.Freshness != "" {
		if fs := mapBraveFreshness(params.Freshness); fs != "" {
			q.Set("freshness", fs)
		}
	}
	if params.MaxURLs > 0 {
		q.Set("maximum_number_of_urls", strconv.Itoa(params.MaxURLs))
	}
	if params.MaxSnippets > 0 {
		q.Set("maximum_number_of_snippets", strconv.Itoa(params.MaxSnippets))
	}
	if params.MaxTokensPerURL > 0 {
		q.Set("maximum_number_of_tokens_per_url", strconv.Itoa(params.MaxTokensPerURL))
	}
	if params.MaxSnippetsPerURL > 0 {
		q.Set("maximum_number_of_snippets_per_url", strconv.Itoa(params.MaxSnippetsPerURL))
	}
	if params.EnableLocal != nil {
		q.Set("enable_local", strconv.FormatBool(*params.EnableLocal))
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

// mapBraveWebSafe maps our safe-search level to Brave web's documented values
// (off | moderate | strict). An empty input returns "" so the param is omitted
// and Brave applies its own default. Unknown values fall back to moderate.
func mapBraveWebSafe(safe string) string {
	switch safe {
	case "":
		return ""
	case "off":
		return "off"
	case "strict", "high":
		return "strict"
	case "moderate", "medium":
		return "moderate"
	default:
		return "moderate"
	}
}

// mapBraveImageSafe maps our safe-search level to Brave's IMAGE endpoint values.
// Images have only off | strict (no moderate); any non-off value maps to strict,
// matching the endpoint's stricter default. Empty returns "" to omit the param.
func mapBraveImageSafe(safe string) string {
	switch safe {
	case "":
		return ""
	case "off":
		return "off"
	default:
		return "strict"
	}
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
	// Query carries Brave's pagination signal (F8): more_results_available is
	// true when offset can advance to fetch further results. Surfaced to the
	// caller via result _meta, never the result body.
	Query struct {
		MoreResultsAvailable bool `json:"more_results_available"`
	} `json:"query"`
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

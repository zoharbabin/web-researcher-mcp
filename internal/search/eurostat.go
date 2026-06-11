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
	"sync"
)

// EurostatProvider implements EconSearcher over the Eurostat dissemination API:
// official European statistics (GDP, unemployment, trade, prices, …) as JSON-stat
// 2.0. Keyless and free — complements FRED (US) and World Bank (global
// development) with EU/Eurozone official statistics behind the same EconProvider
// interface.
//
// Verified contract (2026):
//   - data:    /statistics/1.0/data/{dataset}?format=JSON&lang=EN&geo=..&sinceTimePeriod=..&untilTimePeriod=..
//     → JSON-stat 2.0: a SPARSE `value` map keyed by a single flattened index,
//     plus a `dimension` object whose `time`/`geo` categories carry index→code
//     and index→label maps. We decode (timePeriod,value) pairs by inverting the
//     time category index (positions are row-major, time varies fastest).
//   - search:  there is NO server-side keyword search; we fetch the catalogue
//     TOC once (/catalogue/toc/txt?lang=EN — TSV) and filter dataset titles
//     client-side. (toc/json does NOT exist — 404.)
//   - errors:  mixed — unknown dataset → HTTP 404 + {"error":[…]}; too-large
//     query → HTTP 413; unknown dimension value or empty range → HTTP 200 with
//     an empty value map. We inspect both the status and the body.
type EurostatProvider struct {
	dataBaseURL string
	tocBaseURL  string
	deps        Deps

	tocMu sync.Mutex
	toc   []eurostatTOCEntry // cached on first SUCCESSFUL fetch; nil until then
}

// eurostatTOCEntry is one catalogue row (dataset code + human title).
type eurostatTOCEntry struct {
	Code  string
	Title string
}

// NewEurostatProvider creates the provider. No key required.
func NewEurostatProvider(deps Deps) *EurostatProvider {
	return &EurostatProvider{
		dataBaseURL: "https://ec.europa.eu/eurostat/api/dissemination/statistics/1.0/data",
		tocBaseURL:  "https://ec.europa.eu/eurostat/api/dissemination/catalogue/toc/txt",
		deps:        deps,
	}
}

func (e *EurostatProvider) Name() string { return "eurostat" }

func (e *EurostatProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"EU", "EA", "*"},
		Capabilities: []string{"search", "timeseries", "macro", "official-statistics"},
		RateClass:    "free",
		Description:  "Eurostat — official European statistics (GDP, unemployment, trade, prices, …) as JSON-stat",
	}
}

// SetBaseURLs overrides the API base URLs (testing).
func (e *EurostatProvider) SetBaseURLs(data, toc string) {
	e.dataBaseURL = data
	e.tocBaseURL = toc
}

func (e *EurostatProvider) Econ(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	var results []EconResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doEcon(ctx, params)
		return er
	})
	return results, err
}

func (e *EurostatProvider) doEcon(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	if params.SeriesID != "" {
		return e.observations(ctx, params)
	}
	return e.datasetSearch(ctx, params)
}

// datasetSearch filters the cached catalogue TOC by a case-insensitive title
// match — Eurostat has no server-side keyword search. For single-word queries
// (including exact codes like "une_rt_m") we require the word to appear as a
// contiguous substring. For multi-word queries we require ALL words to appear
// somewhere in the title (AND-match), which lets "quarterly GDP growth" match a
// dataset titled "GDP and main components - quarterly" even though the three
// words are not adjacent. Returns matching dataset codes as series rows.
func (e *EurostatProvider) datasetSearch(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 25)
	toc, err := e.catalogue(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(params.Query))
	words := strings.Fields(needle)
	matchesEntry := func(title string) bool {
		lower := strings.ToLower(title)
		if len(words) <= 1 {
			return strings.Contains(lower, needle)
		}
		for _, w := range words {
			if !strings.Contains(lower, w) {
				return false
			}
		}
		return true
	}
	out := make([]EconResult, 0, num)
	for _, entry := range toc {
		if needle != "" && !matchesEntry(entry.Title) {
			continue
		}
		out = append(out, EconResult{
			SeriesID: entry.Code,
			Title:    entry.Title,
			Source:   "eurostat",
		})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

// catalogue fetches (once) and caches the dataset table-of-contents. The TSV has
// a header row; columns: title, code, type, last-update, last-structure-change,
// data-start, data-end, values. We keep only dataset/table leaves.
func (e *EurostatProvider) catalogue(ctx context.Context) ([]eurostatTOCEntry, error) {
	e.tocMu.Lock()
	defer e.tocMu.Unlock()
	if e.toc != nil {
		return e.toc, nil
	}
	// Cache only on success — a transient failure is returned, not cached, so the
	// next call retries (a sticky error would permanently disable dataset search).
	body, err := e.getRaw(ctx, e.tocBaseURL+"?lang=EN")
	if err != nil {
		return nil, err
	}
	e.toc = parseEurostatTOC(string(body))
	return e.toc, nil
}

// parseEurostatTOC parses the TSV catalogue into dataset/table entries (folders
// are skipped — they're not queryable data leaves).
func parseEurostatTOC(tsv string) []eurostatTOCEntry {
	lines := strings.Split(tsv, "\n")
	out := make([]eurostatTOCEntry, 0, len(lines))
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header / blank
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 3 {
			continue
		}
		title := strings.Trim(strings.TrimSpace(cols[0]), `"`)
		code := strings.Trim(strings.TrimSpace(cols[1]), `"`)
		typ := strings.Trim(strings.TrimSpace(cols[2]), `"`)
		if code == "" || (typ != "dataset" && typ != "table") {
			continue
		}
		out = append(out, eurostatTOCEntry{Code: code, Title: title})
	}
	return out
}

// jsonStat is the subset of the JSON-stat 2.0 response we decode.
type jsonStat struct {
	Label     string             `json:"label"`
	Updated   string             `json:"updated"`
	ID        []string           `json:"id"`
	Size      []int              `json:"size"`
	Value     map[string]float64 `json:"value"`
	Status    map[string]string  `json:"status"`
	Dimension map[string]struct {
		Label    string `json:"label"`
		Category struct {
			Index map[string]int    `json:"index"`
			Label map[string]string `json:"label"`
		} `json:"category"`
	} `json:"dimension"`
	Error []struct {
		Status int    `json:"status"`
		Label  string `json:"label"`
	} `json:"error"`
}

// observations fetches one dataset's time series, scoped by geo (Country) and an
// optional time range, and decodes the JSON-stat value map into observations.
func (e *EurostatProvider) observations(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 200)

	q := url.Values{}
	q.Set("format", "JSON")
	q.Set("lang", "EN")
	if c := strings.TrimSpace(params.Country); c != "" {
		q.Set("geo", c)
	}
	if params.DateFrom != "" {
		q.Set("sinceTimePeriod", eurostatPeriod(params.DateFrom))
	}
	if params.DateTo != "" {
		q.Set("untilTimePeriod", eurostatPeriod(params.DateTo))
	}

	endpoint := e.dataBaseURL + "/" + url.PathEscape(strings.TrimSpace(params.SeriesID)) + "?" + q.Encode()
	body, err := e.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var js jsonStat
	if err := json.Unmarshal(body, &js); err != nil {
		return nil, fmt.Errorf("eurostat: JSON-stat parse: %w", err)
	}
	if len(js.Error) > 0 {
		return nil, fmt.Errorf("eurostat: %s", js.Error[0].Label)
	}
	return decodeJSONStatObservations(&js, params.SeriesID, num), nil
}

// decodeJSONStatObservations turns a JSON-stat cube into observation rows.
// JSON-stat encodes values in a sparse map keyed by a single flattened, row-major
// index over ALL dimensions (last dimension varies fastest). A Eurostat dataset
// is multi-dimensional (e.g. une_rt_m = freq×s_adj×age×unit×sex×geo×time), so
// pinning geo still leaves several series; we must recover EVERY dimension's
// coordinate for each value — not just the time coordinate — or distinct series
// collapse into undifferentiated rows. For each value we therefore: (1) decode
// the full coordinate via the per-dimension strides; (2) read the time code →
// period; (3) compose a human series label from the non-time dimension category
// labels (so a caller can tell "Females, seasonally adjusted, % of active pop."
// apart from other series); (4) emit it with that label as the title suffix.
// Rows are ordered by (series label, period) so each series' time points stay
// together and ascend — then bounded by limit.
func decodeJSONStatObservations(js *jsonStat, seriesID string, limit int) []EconResult {
	if len(js.Value) == 0 || len(js.ID) == 0 || len(js.Size) != len(js.ID) {
		return nil
	}

	// Locate the time dimension.
	timeDim := -1
	for i, id := range js.ID {
		if id == "time" {
			timeDim = i
			break
		}
	}
	if timeDim < 0 {
		return nil // no time dimension — not a time series we can render
	}

	// Row-major strides; guard against any zero-sized dimension (a malformed cube
	// with a non-empty value map would divide/mod by zero below).
	strides := make([]int, len(js.Size))
	strides[len(js.Size)-1] = 1
	for i := len(js.Size) - 2; i >= 0; i-- {
		strides[i] = strides[i+1] * js.Size[i+1]
	}
	for _, s := range js.Size {
		if s <= 0 {
			return nil
		}
	}

	// Pre-invert each dimension's category index (position → code) once.
	posToCode := make([][]string, len(js.ID))
	for d, id := range js.ID {
		dim := js.Dimension[id]
		codes := make([]string, js.Size[d])
		for code, pos := range dim.Category.Index {
			if pos >= 0 && pos < len(codes) {
				codes[pos] = code
			}
		}
		posToCode[d] = codes
	}

	timeDimObj := js.Dimension["time"]
	baseTitle := js.Label
	units := eurostatUnits(js)

	out := make([]EconResult, 0, len(js.Value))
	for flatKey, v := range js.Value {
		idx := atoiSafe(flatKey)
		if idx < 0 {
			continue
		}
		// Recover every dimension's coordinate from the flat key.
		period := ""
		var labelParts []string
		for d, id := range js.ID {
			pos := (idx / strides[d]) % js.Size[d]
			code := ""
			if pos < len(posToCode[d]) {
				code = posToCode[d][pos]
			}
			if d == timeDim {
				period = code
				if lbl, ok := timeDimObj.Category.Label[code]; ok && lbl != "" {
					period = lbl
				}
				continue
			}
			// Build the series label from non-time dimensions that actually vary
			// (size>1) or carry a meaningful label — skip the single-valued ones
			// (e.g. a pinned geo) only when they add no disambiguation.
			if js.Size[d] <= 1 {
				continue
			}
			lbl := code
			if dl, ok := js.Dimension[id].Category.Label[code]; ok && dl != "" {
				lbl = dl
			}
			if lbl != "" {
				labelParts = append(labelParts, lbl)
			}
		}
		title := baseTitle
		if len(labelParts) > 0 {
			title = baseTitle + " — " + strings.Join(labelParts, ", ")
		}
		r := EconResult{
			SeriesID: seriesID,
			Title:    title,
			Units:    units,
			Date:     period,
			Value:    v,
			HasValue: true,
			Source:   "eurostat",
		}
		if flag, ok := js.Status[flatKey]; ok && flag != "" {
			r.Notes = "status: " + flag
		}
		out = append(out, r)
	}

	// Order by (series label, period) so each series' time points stay together
	// and ascend — a coherent time series rather than a cross-section of distinct
	// series all at the earliest period.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Date < out[j].Date
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// eurostatUnits pulls a units label from the single-valued `unit` dimension when
// present (the common case for a scoped query). Empty when absent or multi-valued.
func eurostatUnits(js *jsonStat) string {
	dim, ok := js.Dimension["unit"]
	if !ok || len(dim.Category.Label) != 1 {
		return ""
	}
	for _, lbl := range dim.Category.Label {
		return lbl
	}
	return ""
}

// eurostatPeriod passes through a Eurostat time period (YYYY, YYYY-MM, YYYY-Qn).
// We accept the same forms the API does; a YYYY-MM-DD is trimmed to YYYY-MM since
// Eurostat has no daily frequency.
func eurostatPeriod(date string) string {
	date = strings.TrimSpace(date)
	if len(date) >= 10 && date[4] == '-' && date[7] == '-' {
		return date[:7] // YYYY-MM-DD → YYYY-MM
	}
	return date
}

// get fetches an endpoint expecting a JSON body, mapping Eurostat's status codes
// to clear errors (404 unknown dataset, 413 too-large query).
func (e *EurostatProvider) get(ctx context.Context, endpoint string) ([]byte, error) {
	resp, body, err := e.do(ctx, endpoint, "application/json")
	if err != nil {
		return nil, err
	}
	switch {
	case resp == 404:
		return nil, fmt.Errorf("eurostat: dataset not found")
	case resp == 413:
		return nil, fmt.Errorf("eurostat: query too large — narrow the geo/time filters")
	case resp == 429:
		return nil, fmt.Errorf("eurostat: rate limited")
	case resp >= 400:
		return nil, fmt.Errorf("eurostat: API error %d: %s", resp, truncateText(string(body), 200))
	}
	return body, nil
}

// getRaw fetches a non-JSON endpoint (the TSV catalogue).
func (e *EurostatProvider) getRaw(ctx context.Context, endpoint string) ([]byte, error) {
	resp, body, err := e.do(ctx, endpoint, "text/plain")
	if err != nil {
		return nil, err
	}
	if resp >= 400 {
		return nil, fmt.Errorf("eurostat: catalogue error %d", resp)
	}
	return body, nil
}

func (e *EurostatProvider) do(ctx context.Context, endpoint, accept string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := e.deps.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("eurostat: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("eurostat: read body: %w", err)
	}
	return resp.StatusCode, body, nil
}

// atoiSafe parses a non-negative integer string (a JSON-stat flat index key),
// returning -1 on any non-digit input rather than erroring — a malformed key is
// skipped, not fatal.
func atoiSafe(s string) int {
	if s == "" {
		return -1
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return -1
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

var _ EconProvider = (*EurostatProvider)(nil)

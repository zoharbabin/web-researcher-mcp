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

// OECDProvider implements EconSearcher over the modern OECD SDMX REST API
// (sdmx.oecd.org, the system that replaced stats.oecd.org). Keyless and free —
// OECD-country economic indicators (national accounts, prices, labour, trade, …)
// behind the same EconProvider interface as FRED / World Bank / Eurostat.
//
// Verified contract (2026):
//   - data:       /data/{agency},{dataflow},{version}/{key}?startPeriod=..&endPeriod=..&dimensionAtObservation=TIME_PERIOD
//     Accept: application/vnd.sdmx.data+json;version=2.0.0
//     → SDMX-JSON 2.0: data.dataSets[0].series[KEY].observations is a map keyed
//     by the TIME index; each value is [number, ...attr-indices]. Time labels are
//     in data.structures[0].dimensions.observation[0].values (by index, NOT
//     date-sorted). Series dimension labels (country/indicator/unit) are in
//     data.structures[0].dimensions.series.
//   - discovery:  /dataflow/all/all/latest?detail=allstubs
//     Accept: application/vnd.sdmx.structure+json (do NOT append ;version=…)
//     → 1,500+ dataflows with id/name/agencyID/version; no server-side search, so
//     we filter the cached list client-side by name.
//   - SeriesID is a dataflow ref "agency,dataflow,version" (e.g.
//     "OECD.SDD.NAD,DSD_NAMAIN1@DF_QNA,1.1"). Country scoping uses the POSITIONAL
//     key (a dot-separated slot per series dimension) with the country pinned in
//     the REF_AREA slot — the `c[REF_AREA]=` query param is a 2.1-style filter the
//     2.0 data endpoint rejects with 422. We resolve the dataflow's dimension
//     order once (cached per ref) to find REF_AREA's slot; no country ⇒ empty key
//     (whole dataflow).
//   - errors:     proper HTTP codes (404 unknown dataflow, 406 bad Accept, 422
//     bad key) with plain-text bodies — trust the status, not the body.
type OECDProvider struct {
	baseURL string
	deps    Deps

	flowsMu sync.Mutex
	flows   []oecdDataflow // cached on first SUCCESSFUL fetch; nil until then

	dimsMu sync.Mutex
	dims   map[string][]string // dataflow ref → ordered series-dimension IDs (cached)
}

type oecdDataflow struct {
	Ref  string // "agency,id,version" — the data-endpoint reference
	Name string
}

// NewOECDProvider creates the provider. No key required.
func NewOECDProvider(deps Deps) *OECDProvider {
	return &OECDProvider{
		baseURL: "https://sdmx.oecd.org/public/rest",
		deps:    deps,
	}
}

func (o *OECDProvider) Name() string { return "oecd" }

func (o *OECDProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"OECD", "*"},
		Capabilities: []string{"search", "timeseries", "macro", "official-statistics"},
		RateClass:    "free",
		Description:  "OECD — economic indicators for OECD economies (national accounts, prices, labour, trade) via SDMX",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (o *OECDProvider) SetBaseURL(base string) { o.baseURL = base }

func (o *OECDProvider) Econ(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	var results []EconResult
	err := o.deps.Breaker.Execute(func() error {
		var er error
		results, er = o.doEcon(ctx, params)
		return er
	})
	return results, err
}

func (o *OECDProvider) doEcon(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	if params.SeriesID != "" {
		return o.observations(ctx, params)
	}
	return o.dataflowSearch(ctx, params)
}

// dataflowSearch filters the cached dataflow list by a case-insensitive name
// substring — OECD has no server-side search. Returns matching dataflow refs as
// series rows (the ref is what the caller passes back as SeriesID).
func (o *OECDProvider) dataflowSearch(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 25)
	flows, err := o.dataflows(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(params.Query))
	out := make([]EconResult, 0, num)
	for _, f := range flows {
		if needle != "" && !strings.Contains(strings.ToLower(f.Name), needle) {
			continue
		}
		out = append(out, EconResult{
			SeriesID: f.Ref,
			Title:    f.Name,
			Source:   "oecd",
		})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

// sdmxStructureList is the dataflow-list response (SDMX-JSON structure 1.0).
type sdmxStructureList struct {
	Data struct {
		Dataflows []struct {
			ID       string            `json:"id"`
			AgencyID string            `json:"agencyID"`
			Version  string            `json:"version"`
			Name     string            `json:"name"`
			Names    map[string]string `json:"names"`
		} `json:"dataflows"`
	} `json:"data"`
}

// dataflows fetches and caches the OECD dataflow list. The list is cached only on
// a SUCCESSFUL fetch; a transient failure is returned (not cached) so the next
// call retries — a sticky error would permanently disable search for the process.
func (o *OECDProvider) dataflows(ctx context.Context) ([]oecdDataflow, error) {
	o.flowsMu.Lock()
	defer o.flowsMu.Unlock()
	if o.flows != nil {
		return o.flows, nil
	}

	endpoint := o.baseURL + "/dataflow/all/all/latest?detail=allstubs"
	body, err := o.get(ctx, endpoint, "application/vnd.sdmx.structure+json")
	if err != nil {
		return nil, err
	}
	var list sdmxStructureList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("oecd: dataflow list parse: %w", err)
	}
	flows := make([]oecdDataflow, 0, len(list.Data.Dataflows))
	for _, df := range list.Data.Dataflows {
		name := df.Name
		if name == "" {
			name = df.Names["en"]
		}
		ver := df.Version
		if ver == "" {
			ver = "latest"
		}
		flows = append(flows, oecdDataflow{
			Ref:  fmt.Sprintf("%s,%s,%s", df.AgencyID, df.ID, ver),
			Name: name,
		})
	}
	o.flows = flows
	return o.flows, nil
}

// sdmxDimValue is one coded value of an SDMX dimension (id + human name).
type sdmxDimValue struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// sdmxDimension is one SDMX dimension with its ordered coded values; a series-key
// or observation-key index selects into Values.
type sdmxDimension struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Values []sdmxDimValue `json:"values"`
}

// sdmxData is the subset of the SDMX-JSON 2.0 data response we decode.
type sdmxData struct {
	Data struct {
		DataSets []struct {
			Series map[string]struct {
				Observations map[string][]json.RawMessage `json:"observations"`
			} `json:"series"`
		} `json:"dataSets"`
		Structures []struct {
			Dimensions struct {
				Series      []sdmxDimension `json:"series"`
				Observation []sdmxDimension `json:"observation"`
			} `json:"dimensions"`
		} `json:"structures"`
	} `json:"data"`
}

// observations fetches a dataflow's observations, scoped to a country (REF_AREA)
// and an optional time range, and decodes the SDMX-JSON value/time structure.
func (o *OECDProvider) observations(ctx context.Context, params EconSearchParams) ([]EconResult, error) {
	num := clamp(params.NumResults, 1, 200)
	ref := strings.TrimSpace(params.SeriesID)
	// The ref is interpolated into the request PATH (its commas/`@`/dots must stay
	// literal, so url.PathEscape is unsuitable). Validate it against the SDMX
	// dataflow-ref grapheme set instead, rejecting any path/query metacharacter
	// (`/ ? # %` etc.) that could reshape the upstream URL — a boundary check on
	// user-supplied input (Security Rule #5), even though the host is fixed.
	if !validOECDRef(ref) {
		return nil, fmt.Errorf("oecd: invalid dataflow ref %q (expected agency,dataflow,version)", ref)
	}

	q := url.Values{}
	q.Set("dimensionAtObservation", "TIME_PERIOD")
	if params.DateFrom != "" {
		q.Set("startPeriod", oecdPeriod(params.DateFrom))
	}
	if params.DateTo != "" {
		q.Set("endPeriod", oecdPeriod(params.DateTo))
	}

	// Country scoping in SDMX-JSON 2.0 is done via the POSITIONAL key (a
	// dot-separated slot per series dimension), NOT a `c[REF_AREA]=` query param
	// (that 2.1-style param is rejected with 422 by this endpoint). When a country
	// is given we resolve the dataflow's dimension order (cached per ref) to find
	// REF_AREA's slot and build a wildcard key with the country pinned there; an
	// empty key requests the whole dataflow. A structure-fetch failure degrades to
	// the unscoped key rather than erroring.
	key := ""
	if c := strings.TrimSpace(params.Country); c != "" {
		key = o.buildCountryKey(ctx, ref, c)
	}

	endpoint := fmt.Sprintf("%s/data/%s/%s?%s", o.baseURL, ref, key, q.Encode())
	body, err := o.get(ctx, endpoint, "application/vnd.sdmx.data+json;version=2.0.0")
	if err != nil {
		return nil, err
	}

	var sd sdmxData
	if err := json.Unmarshal(body, &sd); err != nil {
		return nil, fmt.Errorf("oecd: SDMX-JSON parse: %w", err)
	}
	return decodeSDMXObservations(&sd, ref, num), nil
}

// buildCountryKey returns a positional SDMX key (dot-separated, one empty slot per
// series dimension) with the given country pinned in the REF_AREA slot — e.g.
// "..USA.........." for a 13-dimension dataflow. It resolves the dataflow's
// dimension order via the cached structure; on any failure (unresolvable
// structure, no REF_AREA dimension) it returns "" so the caller falls back to the
// unscoped whole-dataflow query rather than erroring.
func (o *OECDProvider) buildCountryKey(ctx context.Context, ref, country string) string {
	dims := o.dimensionOrder(ctx, ref)
	refAreaPos := -1
	for i, id := range dims {
		if id == "REF_AREA" {
			refAreaPos = i
			break
		}
	}
	if refAreaPos < 0 || len(dims) == 0 {
		return "" // can't place the country → unscoped query
	}
	slots := make([]string, len(dims))
	slots[refAreaPos] = country
	return strings.Join(slots, ".")
}

// dimensionOrder returns the ordered series-dimension IDs for a dataflow ref,
// fetched from its data-structure definition and cached per ref (the order is
// stable for a given dataflow version). Returns nil on any failure.
func (o *OECDProvider) dimensionOrder(ctx context.Context, ref string) []string {
	o.dimsMu.Lock()
	defer o.dimsMu.Unlock()
	if o.dims == nil {
		o.dims = make(map[string][]string)
	}
	if cached, ok := o.dims[ref]; ok {
		return cached
	}

	// ref is "agency,id,version"; the structure endpoint wants slash-separated path.
	parts := strings.SplitN(ref, ",", 3)
	if len(parts) != 3 {
		return nil
	}
	endpoint := fmt.Sprintf("%s/dataflow/%s/%s/%s?references=datastructure",
		o.baseURL, parts[0], parts[1], parts[2])
	body, err := o.get(ctx, endpoint, "application/vnd.sdmx.structure+json")
	if err != nil {
		return nil
	}
	var s struct {
		Data struct {
			DataStructures []struct {
				DataStructureComponents struct {
					DimensionList struct {
						Dimensions []struct {
							ID       string `json:"id"`
							Position int    `json:"position"`
						} `json:"dimensions"`
					} `json:"dimensionList"`
				} `json:"dataStructureComponents"`
			} `json:"dataStructures"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &s); err != nil || len(s.Data.DataStructures) == 0 {
		return nil
	}
	dimList := s.Data.DataStructures[0].DataStructureComponents.DimensionList.Dimensions
	if len(dimList) == 0 {
		return nil
	}
	// Order by position (defensive — the API usually returns them in order already).
	order := make([]string, len(dimList))
	for _, d := range dimList {
		if d.Position >= 0 && d.Position < len(order) {
			order[d.Position] = d.ID
		}
	}
	// Guard against gaps (a missing position would leave an empty id).
	for _, id := range order {
		if id == "" {
			return nil
		}
	}
	o.dims[ref] = order
	return order
}

// decodeSDMXObservations turns an SDMX-JSON 2.0 cube into observation rows. The
// observation map is keyed by the TIME index (dimensionAtObservation=TIME_PERIOD);
// each value is [number, ...attribute-indices]. We resolve the TIME index to a
// period string via structures[0].dimensions.observation[0].values (by index,
// since that array is NOT date-sorted), and compose a title from the series
// dimension labels.
func decodeSDMXObservations(sd *sdmxData, ref string, limit int) []EconResult {
	if len(sd.Data.DataSets) == 0 || len(sd.Data.Structures) == 0 {
		return nil
	}
	st := sd.Data.Structures[0]
	if len(st.Dimensions.Observation) == 0 {
		return nil
	}
	timeValues := st.Dimensions.Observation[0].Values // index → {id,name}

	out := make([]EconResult, 0)
	for seriesKey, series := range sd.Data.DataSets[0].Series {
		title, units := oecdSeriesLabels(seriesKey, st.Dimensions.Series)
		for obsIdxStr, cells := range series.Observations {
			obsIdx := atoiSafe(obsIdxStr)
			if obsIdx < 0 || obsIdx >= len(timeValues) || len(cells) == 0 {
				continue
			}
			var val float64
			hasVal := false
			// cells[0] is the observation value (nullable).
			if string(cells[0]) != "null" {
				if err := json.Unmarshal(cells[0], &val); err == nil {
					hasVal = true
				}
			}
			period := timeValues[obsIdx].ID
			if timeValues[obsIdx].Name != "" {
				period = timeValues[obsIdx].Name
			}
			out = append(out, EconResult{
				SeriesID: ref,
				Title:    title,
				Units:    units,
				Date:     period,
				Value:    val,
				HasValue: hasVal,
				Source:   "oecd",
			})
		}
	}

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

// oecdSeriesLabelSkip are dimensions excluded from the composed series title: the
// unit dimensions (surfaced as Units instead) and TIME (the observation axis, not
// a series facet). FREQ is kept — a flow can mix monthly and quarterly series, so
// the frequency is a real differentiator. Every other coded dimension's value name
// is joined into the title so demographically-distinct series (sex, age, measure,
// adjustment, …) are disambiguated rather than collapsing to one identical label.
var oecdSeriesLabelSkip = map[string]bool{
	"UNIT_MEASURE": true, "UNIT": true,
	"TIME_PERIOD": true, "TIME": true,
}

// oecdSeriesLabels composes a human title and units from a series key like
// "0:1:0:..." indexing into the series dimension value lists. The title joins
// EVERY series-dimension value name except the skip set (so sex/age/measure/etc.
// distinguish otherwise-identical series); units is the UNIT_MEASURE/UNIT value.
func oecdSeriesLabels(seriesKey string, dims []sdmxDimension) (title, units string) {
	idxParts := strings.Split(seriesKey, ":")
	var labels []string
	for d, part := range idxParts {
		if d >= len(dims) {
			break
		}
		vi := atoiSafe(part)
		if vi < 0 || vi >= len(dims[d].Values) {
			continue
		}
		valName := dims[d].Values[vi].Name
		if valName == "" {
			valName = dims[d].Values[vi].ID
		}
		if dims[d].ID == "UNIT_MEASURE" || dims[d].ID == "UNIT" {
			units = valName
			continue
		}
		if oecdSeriesLabelSkip[dims[d].ID] || valName == "" {
			continue
		}
		labels = append(labels, valName)
	}
	return strings.Join(labels, " — "), units
}

// oecdPeriod passes an OECD SDMX time period through at the granularity the caller
// supplied. OECD accepts YYYY, YYYY-MM, and YYYY-Qn and returns observations at
// that granularity, so we must NOT truncate to the year (doing so makes a monthly
// flow return only annual aggregates). A full ISO date YYYY-MM-DD is trimmed to
// YYYY-MM (OECD has no daily frequency); anything else is passed through verbatim.
func oecdPeriod(date string) string {
	date = strings.TrimSpace(date)
	// YYYY-MM-DD → YYYY-MM (SDMX max granularity is monthly).
	if len(date) == 10 && date[4] == '-' && date[7] == '-' {
		return date[:7]
	}
	return date
}

// validOECDRef reports whether ref is a well-formed SDMX dataflow reference
// (`agency,dataflow,version`) safe to interpolate into the request path. It
// allows only the characters real OECD refs use — letters, digits, and
// `. _ - @ ,` — so no `/ ? # %` or whitespace can reshape the URL. Empty is
// rejected. This is a closed allowlist, not a denylist.
func validOECDRef(ref string) bool {
	if ref == "" {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '@' || r == ',':
		default:
			return false
		}
	}
	return true
}

func (o *OECDProvider) get(ctx context.Context, endpoint, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := o.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oecd: request failed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404:
		return nil, fmt.Errorf("oecd: dataflow not found")
	case resp.StatusCode == 406:
		return nil, fmt.Errorf("oecd: unsupported response format")
	case resp.StatusCode == 422:
		return nil, fmt.Errorf("oecd: invalid query key for this dataflow")
	case resp.StatusCode == 429:
		return nil, fmt.Errorf("oecd: rate limited")
	case resp.StatusCode >= 400:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("oecd: API error %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
}

var _ EconProvider = (*OECDProvider)(nil)

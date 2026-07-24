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

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// EDGARProvider implements FilingSearcher over the SEC EDGAR public APIs.
// Coverage: every US public-company disclosure (10-K/10-Q/8-K/S-1/DEF 14A/…).
// No API key — SEC requires only a descriptive User-Agent with a contact email.
//
// Live-verified (2026-06-06):
//   - ticker→CIK map: https://www.sec.gov/files/company_tickers.json
//   - full-text search: https://efts.sec.gov/LATEST/search-index?q=&forms=
//     → {hits:{total:{value},hits:[{_id,_source:{adsh,form,file_date,
//     period_ending,display_names,ciks}}]}}
//   - submissions: https://data.sec.gov/submissions/CIK{10-digit}.json
//   - company facts: https://data.sec.gov/api/xbrl/companyfacts/CIK{10-digit}.json
type EDGARProvider struct {
	userAgent string
	ftsURL    string // efts.sec.gov full-text search base
	dataURL   string // data.sec.gov base
	wwwURL    string // www.sec.gov base (ticker map + Archives)
	deps      Deps

	tickerOnce sync.Once
	tickerMap  map[string]string // upper(ticker) → 10-digit CIK
	nameMap    map[string]string // upper(company title) → 10-digit CIK (for company-name resolution)
	tickerErr  error
}

// NewEDGARProvider creates the provider. userAgent must be non-empty (SEC blocks
// requests without a descriptive UA).
func NewEDGARProvider(userAgent string, deps Deps) *EDGARProvider {
	return &EDGARProvider{
		userAgent: userAgent,
		ftsURL:    "https://efts.sec.gov/LATEST",
		dataURL:   "https://data.sec.gov",
		wwwURL:    "https://www.sec.gov",
		deps:      deps,
	}
}

func (e *EDGARProvider) Name() string { return "edgar" }

func (e *EDGARProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"US"},
		Capabilities: []string{"search", "filings", "facts"},
		RateClass:    "free",
		Description:  "SEC EDGAR — authoritative US public-company filings (10-K/10-Q/8-K/…) and XBRL company facts",
	}
}

// SetBaseURLs overrides all three API bases (testing).
func (e *EDGARProvider) SetBaseURLs(fts, data, www string) {
	e.ftsURL, e.dataURL, e.wwwURL = fts, data, www
}

func (e *EDGARProvider) Filings(ctx context.Context, params FilingSearchParams) ([]FilingResult, error) {
	var results []FilingResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doFilings(ctx, params)
		return er
	})
	return results, err
}

func (e *EDGARProvider) doFilings(ctx context.Context, params FilingSearchParams) ([]FilingResult, error) {
	num := clamp(params.NumResults, 1, 10)

	// Facts mode: resolve the entity to a CIK and return XBRL company facts.
	if params.Facts {
		cik, company, err := e.resolveCIK(ctx, params)
		if err != nil {
			return nil, err
		}
		if cik == "" {
			return nil, nil
		}
		return e.companyFacts(ctx, cik, company, num)
	}

	// If the query resolves to a specific company (ticker/CIK/name), list its
	// recent filings from the submissions API (precise, structured). Otherwise
	// run a full-text search across all filings.
	if cik, company, _ := e.resolveCIK(ctx, params); cik != "" {
		return e.companySubmissions(ctx, cik, company, params, num)
	}
	return e.fullTextSearch(ctx, params, num)
}

// resolveCIK maps a ticker/CIK/company-name to a 10-digit CIK. Returns ("", "",
// nil) when the entity can't be resolved (caller falls back to full-text search).
//
// Resolution order:
//  1. params.Ticker — exact ticker map lookup (always trusted; used when ticker is explicit)
//  2. Bare numeric seed — treated as a raw CIK directly
//  3. Exact ticker match on params.Query (e.g. query "AAPL")
//  4. Company-name extraction from params.Query — strips known stop-words (form
//     types, "annual", "report", "10-K", etc.) to isolate the company name token(s),
//     then does an exact match against the SEC title map and a prefix match for
//     single-token queries. Falls through to full-text search when no confident
//     match is found — never guesses.
func (e *EDGARProvider) resolveCIK(ctx context.Context, params FilingSearchParams) (string, string, error) {
	seed := strings.TrimSpace(params.Ticker)
	if seed == "" {
		seed = strings.TrimSpace(params.Query)
	}
	if seed == "" {
		return "", "", nil
	}
	// A bare numeric seed is a CIK already.
	if isAllDigits(seed) {
		return padCIK(seed), "", nil
	}
	if err := e.loadTickerMap(ctx); err != nil {
		return "", "", err
	}
	// Exact ticker match.
	if cik, ok := e.tickerMap[strings.ToUpper(seed)]; ok {
		return cik, "", nil
	}
	// Company-name resolution: only attempted when the query came from params.Query
	// (not an explicit ticker), so we don't misfire on a valid ticker that happens
	// to look like a stop-word. Strip known noise words to isolate the company name.
	if params.Ticker == "" {
		cik, company := e.resolveByCompanyName(seed)
		if cik != "" {
			return cik, company, nil
		}
	}
	return "", "", nil // fall through to full-text search
}

// queryStopWords are words that appear in natural-language filing queries but
// carry no company-name signal. Stripping them from a query like
// "Apple annual report 10-K" leaves "Apple", which maps cleanly to the ticker map.
var queryStopWords = map[string]bool{
	"annual": true, "report": true, "filing": true, "filings": true,
	"10-k": true, "10-q": true, "8-k": true, "s-1": true, "def": true, "14a": true,
	"proxy": true, "earnings": true, "financial": true, "statements": true,
	"the": true, "for": true, "of": true, "and": true, "inc": true,
	"corp": true, "company": true, "quarterly": true, "latest": true,
}

// resolveByCompanyName strips stop-words from the raw query, then tries:
//  1. Exact upper-case match of the cleaned phrase against the SEC title map
//     (handles "Apple Inc", "NVIDIA Corp", "Meta Platforms Inc")
//  2. Prefix scan for single-token candidates (e.g. "Apple" matches "APPLE INC")
//
// Returns ("", "") when no confident match is found so the caller falls through
// to full-text search rather than guessing.
func (e *EDGARProvider) resolveByCompanyName(rawQuery string) (cik, company string) {
	// Split on whitespace, discard stop-words and form-type tokens.
	tokens := strings.Fields(rawQuery)
	var nameTokens []string
	for _, t := range tokens {
		lower := strings.ToLower(t)
		if !queryStopWords[lower] {
			nameTokens = append(nameTokens, t)
		}
	}
	if len(nameTokens) == 0 {
		return "", ""
	}
	candidate := strings.ToUpper(strings.Join(nameTokens, " "))

	// Exact match first (e.g. "APPLE INC" or "NVIDIA CORP").
	if cikVal, ok := e.nameMap[candidate]; ok {
		return cikVal, strings.Title(strings.ToLower(candidate)) //nolint:staticcheck
	}

	// Single-token prefix scan: "APPLE" → finds "APPLE INC", "APPLE HOSPITALITY REIT INC", etc.
	// Only use when exactly one name token remains (avoids ambiguous multi-word partials).
	if len(nameTokens) == 1 {
		prefix := candidate + " "
		var bestCIK, bestName string
		for title, cikVal := range e.nameMap {
			// Exact match takes precedence (already handled above, but guard anyway).
			if title == candidate {
				return cikVal, strings.Title(strings.ToLower(title)) //nolint:staticcheck
			}
			// Take the shortest prefix-matching title so "APPLE INC" wins over
			// "APPLE HOSPITALITY REIT INC" for query "Apple".
			if strings.HasPrefix(title, prefix) {
				if bestName == "" || len(title) < len(bestName) {
					bestCIK = cikVal
					bestName = title
				}
			}
		}
		if bestCIK != "" {
			return bestCIK, strings.Title(strings.ToLower(bestName)) //nolint:staticcheck
		}
	}
	return "", ""
}

func (e *EDGARProvider) loadTickerMap(ctx context.Context) error {
	e.tickerOnce.Do(func() {
		body, err := e.get(ctx, e.wwwURL+"/files/company_tickers.json")
		if err != nil {
			e.tickerErr = err
			return
		}
		// Shape: {"0":{"cik_str":1045810,"ticker":"NVDA","title":"NVIDIA CORP"},…}
		var raw map[string]struct {
			CIK    int    `json:"cik_str"`
			Ticker string `json:"ticker"`
			Title  string `json:"title"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			e.tickerErr = fmt.Errorf("edgar: ticker map parse: %w", err)
			return
		}
		tm := make(map[string]string, len(raw))
		nm := make(map[string]string, len(raw))
		for _, v := range raw {
			cik := fmt.Sprintf("%010d", v.CIK)
			tm[strings.ToUpper(v.Ticker)] = cik
			nm[strings.ToUpper(v.Title)] = cik
		}
		e.tickerMap = tm
		e.nameMap = nm
	})
	return e.tickerErr
}

func (e *EDGARProvider) companySubmissions(ctx context.Context, cik, company string, params FilingSearchParams, num int) ([]FilingResult, error) {
	body, err := e.get(ctx, fmt.Sprintf("%s/submissions/CIK%s.json", e.dataURL, cik))
	if err != nil {
		return nil, err
	}
	var sub struct {
		Name    string `json:"name"`
		Filings struct {
			Recent struct {
				AccessionNumber []string `json:"accessionNumber"`
				Form            []string `json:"form"`
				FilingDate      []string `json:"filingDate"`
				ReportDate      []string `json:"reportDate"`
				PrimaryDocument []string `json:"primaryDocument"`
				PrimaryDocDesc  []string `json:"primaryDocDescription"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if err := json.Unmarshal(body, &sub); err != nil {
		return nil, fmt.Errorf("edgar: submissions parse: %w", err)
	}
	if company == "" {
		company = sub.Name
	}
	r := sub.Filings.Recent
	form := strings.ToUpper(strings.TrimSpace(params.FormType))
	out := make([]FilingResult, 0, num)
	for i := range r.AccessionNumber {
		if form != "" && !strings.EqualFold(at(r.Form, i), form) {
			continue
		}
		if !dateInRange(at(r.FilingDate, i), params.DateFrom, params.DateTo) {
			continue
		}
		out = append(out, FilingResult{
			Company:     company,
			CIK:         cik,
			FormType:    at(r.Form, i),
			FilingDate:  at(r.FilingDate, i),
			PeriodOf:    at(r.ReportDate, i),
			Accession:   at(r.AccessionNumber, i),
			URL:         filingURL(cik, at(r.AccessionNumber, i), at(r.PrimaryDocument, i)),
			Description: at(r.PrimaryDocDesc, i),
			Source:      "edgar",
		})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

func (e *EDGARProvider) fullTextSearch(ctx context.Context, params FilingSearchParams, num int) ([]FilingResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	if params.FormType != "" {
		q.Set("forms", params.FormType)
	}
	if params.DateFrom != "" {
		q.Set("dateRange", "custom")
		q.Set("startdt", params.DateFrom)
	}
	if params.DateTo != "" {
		q.Set("dateRange", "custom")
		q.Set("enddt", params.DateTo)
	}
	body, err := e.get(ctx, e.ftsURL+"/search-index?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Hits struct {
			Hits []struct {
				ID     string `json:"_id"`
				Source struct {
					Adsh         string   `json:"adsh"`
					Form         string   `json:"form"`
					FileDate     string   `json:"file_date"`
					PeriodEnding string   `json:"period_ending"`
					DisplayNames []string `json:"display_names"`
					CIKs         []string `json:"ciks"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("edgar: fts parse: %w", err)
	}
	out := make([]FilingResult, 0, num)
	for _, h := range resp.Hits.Hits {
		s := h.Source
		cik := ""
		if len(s.CIKs) > 0 {
			cik = s.CIKs[0]
		}
		company := ""
		if len(s.DisplayNames) > 0 {
			company = s.DisplayNames[0]
		}
		// _id is "accession:document"; build the document URL from its parts.
		doc := ""
		if i := strings.IndexByte(h.ID, ':'); i >= 0 {
			doc = h.ID[i+1:]
		}
		out = append(out, FilingResult{
			Company:    company,
			CIK:        cik,
			FormType:   s.Form,
			FilingDate: s.FileDate,
			PeriodOf:   s.PeriodEnding,
			Accession:  s.Adsh,
			URL:        filingURL(cik, s.Adsh, doc),
			Source:     "edgar",
		})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

func (e *EDGARProvider) companyFacts(ctx context.Context, cik, company string, num int) ([]FilingResult, error) {
	body, err := e.get(ctx, fmt.Sprintf("%s/api/xbrl/companyfacts/CIK%s.json", e.dataURL, cik))
	if err != nil {
		return nil, err
	}
	var facts struct {
		EntityName string `json:"entityName"`
		Facts      map[string]map[string]struct {
			Label string `json:"label"`
			Units map[string][]struct {
				End  string  `json:"end"`
				Val  float64 `json:"val"`
				Form string  `json:"form"`
				FY   int     `json:"fy"`
			} `json:"units"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(body, &facts); err != nil {
		return nil, fmt.Errorf("edgar: companyfacts parse: %w", err)
	}
	if company == "" {
		company = facts.EntityName
	}
	// Surface a curated set of headline concepts, most-recent value each, so the
	// output is useful without dumping the entire (huge) facts document.
	headline := []string{
		"Revenues", "RevenueFromContractWithCustomerExcludingAssessedTax",
		"NetIncomeLoss", "Assets", "Liabilities", "StockholdersEquity",
		"EarningsPerShareBasic", "EarningsPerShareDiluted", "OperatingIncomeLoss",
		"CashAndCashEquivalentsAtCarryingValue",
	}
	usgaap := facts.Facts["us-gaap"]
	out := make([]FilingResult, 0, num)
	for _, concept := range headline {
		c, ok := usgaap[concept]
		if !ok {
			continue
		}
		unit, dp := latestFact(c.Units)
		if unit == "" {
			continue
		}
		out = append(out, FilingResult{
			Company:    company,
			CIK:        cik,
			Concept:    concept,
			Unit:       unit,
			Value:      dp.Val,
			PeriodOf:   dp.End,
			FormType:   dp.Form,
			FilingDate: dp.End,
			URL:        fmt.Sprintf("%s/cgi-bin/browse-edgar?action=getcompany&CIK=%s&type=10-K", e.wwwURL, cik),
			Source:     "edgar",
		})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

// latestFact returns the unit and most-recent datapoint for an XBRL concept.
// It prefers the most-recent 10-K annual entry over 10-Q quarterly entries so
// that headline concepts (Revenues, Assets) reflect full-year values rather than
// a mix of stale annual + current quarterly figures. When no 10-K exists it falls
// back to the latest entry of any form (honest: better than nothing).
func latestFact(units map[string][]struct {
	End  string  `json:"end"`
	Val  float64 `json:"val"`
	Form string  `json:"form"`
	FY   int     `json:"fy"`
}) (string, struct {
	End  string  `json:"end"`
	Val  float64 `json:"val"`
	Form string  `json:"form"`
	FY   int     `json:"fy"`
}) {
	type dp = struct {
		End  string  `json:"end"`
		Val  float64 `json:"val"`
		Form string  `json:"form"`
		FY   int     `json:"fy"`
	}
	// Deterministic unit selection: alphabetically first key (e.g. "USD" before "USD/shares").
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pts := units[k]
		if len(pts) == 0 {
			continue
		}
		// Prefer 10-K annual entries so full-year values win over quarterly snapshots.
		var bestAnnual, bestAny dp
		var hasAnnual bool
		for _, p := range pts {
			if strings.Contains(p.Form, "10-K") {
				if !hasAnnual || p.End > bestAnnual.End {
					bestAnnual = p
					hasAnnual = true
				}
			}
			if p.End > bestAny.End {
				bestAny = p
			}
		}
		if hasAnnual {
			return k, bestAnnual
		}
		return k, bestAny
	}
	return "", dp{}
}

// get performs a bare authenticated GET (User-Agent set), with EDGAR-typed error
// handling. The breaker is applied by the public Filings method.
func (e *EDGARProvider) get(ctx context.Context, fullURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	// SEC mandates a descriptive User-Agent; requests without it are blocked.
	req.Header.Set("User-Agent", e.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := e.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("edgar: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("edgar: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("edgar: not found")
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("edgar: API error %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
}

// filingURL builds the EDGAR document URL from CIK + accession (+ optional doc).
// Accession dashes are stripped for the Archives path.
func filingURL(cik, accession, doc string) string {
	if accession == "" {
		return ""
	}
	noDash := strings.ReplaceAll(accession, "-", "")
	cikNum := strings.TrimLeft(cik, "0")
	if cikNum == "" {
		cikNum = cik
	}
	base := fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s", cikNum, noDash)
	if doc != "" {
		return base + "/" + doc
	}
	return base + "/" + accession + "-index.htm"
}

func padCIK(s string) string {
	if len(s) >= 10 {
		return s
	}
	return strings.Repeat("0", 10-len(s)) + s
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// dateInRange reports whether a YYYY-MM-DD date is within [from,to] (either bound
// may be empty = unbounded). Lexicographic compare works for ISO dates.
func dateInRange(date, from, to string) bool {
	if date == "" {
		return true
	}
	if from != "" && date < from {
		return false
	}
	if to != "" && date > to {
		return false
	}
	return true
}

// at safely indexes a string slice (parallel-array submissions data may be
// ragged), returning "" when out of range.
func at(s []string, i int) string {
	if i >= 0 && i < len(s) {
		return s[i]
	}
	return ""
}

var _ FilingProvider = (*EDGARProvider)(nil)

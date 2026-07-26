package search

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// PubMedProvider implements AcademicSearcher over the NCBI E-utilities API:
// biomedical literature (PubMed). Keyless and free (an optional API key raises
// the per-IP rate). Returns paper records with DOIs where available, so the
// tool-layer retraction (EnrichRetraction) and open-access (EnrichOpenAccess)
// enrichment run on them exactly as for the other academic providers.
//
// Verified contract (2026): a two-call flow —
//  1. esearch.fcgi?db=pubmed&term=…&retmode=json&retmax=N → esearchresult.idlist (PMIDs)
//  2. esummary.fcgi?db=pubmed&id=…&retmode=json → result[<pmid>] document summaries.
//
// The DOI lives in result[pmid].articleids[] where idtype=="doi". Authors are in
// result[pmid].authors[].name ("Surname Initials"). Year is the first 4 chars of
// sortpubdate. Both calls return HTTP 200 even for errors/empty, so the JSON body
// is inspected (esearchresult.ERROR, empty idlist, per-record error).
//
// When AcademicSearchParams.FullText is true, the esearch term is narrowed with
// PubMed's "pubmed pmc"[sb] filter so results are biased toward PMC-deposited
// papers, and a third call — efetch.fcgi against PMC — fetches the JATS XML full
// text for any result with a PMCID (present in articleids[]). Best-effort:
// articles without a PMCID or with an efetch failure are returned without
// FullText set.
type PubMedProvider struct {
	baseURL string
	apiKey  string
	email   string
	deps    Deps
}

// NewPubMedProvider creates the provider. apiKey and email are optional (keyless
// by default); when set they are appended to every request (key raises the rate,
// tool/email are NCBI's recommended contact params).
func NewPubMedProvider(apiKey, email string, deps Deps) *PubMedProvider {
	return &PubMedProvider{
		baseURL: "https://eutils.ncbi.nlm.nih.gov/entrez/eutils",
		apiKey:  apiKey,
		email:   email,
		deps:    deps,
	}
}

func (p *PubMedProvider) Name() string { return "pubmed" }

func (p *PubMedProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"academic", "biomedical"},
		RateClass:    "free",
		Description:  "PubMed (NCBI E-utilities) — biomedical and life-sciences literature, with DOIs for retraction/open-access enrichment",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (p *PubMedProvider) SetBaseURL(base string) { p.baseURL = base }

func (p *PubMedProvider) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	var results []AcademicResult
	err := p.deps.Breaker.Execute(func() error {
		var er error
		results, er = p.doScholarly(ctx, params)
		return er
	})
	return results, err
}

func (p *PubMedProvider) doScholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, nil // honor empty: no query, no results (no fallback)
	}
	pmids, err := p.esearch(ctx, params)
	if err != nil {
		return nil, err
	}
	if len(pmids) == 0 {
		return nil, nil // valid query, no matches
	}
	results, pmcids, err := p.esummary(ctx, pmids)
	if err != nil {
		return nil, err
	}
	if params.FullText {
		for i := range results {
			pmcid, ok := pmcids[results[i].URL]
			if !ok || pmcid == "" {
				continue
			}
			text, ferr := p.FetchFullText(ctx, pmcid)
			if ferr != nil || text == "" {
				continue // best-effort: full text is an enrichment, not required
			}
			results[i].FullText = text
		}
	}
	return results, nil
}

// esearchResponse models the esearch JSON we read.
type esearchResponse struct {
	ESearchResult struct {
		Count    string   `json:"count"`
		IDList   []string `json:"idlist"`
		ErrorMsg string   `json:"ERROR"`
	} `json:"esearchresult"`
}

// esearch maps a query (+ optional year range) to a list of PMIDs. When
// FullText is requested, the query is narrowed with PubMed's own "pubmed
// pmc"[sb] search field — documented at pubmed.ncbi.nlm.nih.gov/help/ — so the
// search itself surfaces PMC-deposited (full-text-available) papers instead of
// leaving full-text attachment to chance in doScholarly.
func (p *PubMedProvider) esearch(ctx context.Context, params AcademicSearchParams) ([]string, error) {
	num := clamp(params.NumResults, 1, 50)
	term := params.Query
	if params.FullText {
		term += ` AND "pubmed pmc"[sb]`
	}
	q := url.Values{}
	q.Set("db", "pubmed")
	q.Set("term", term)
	q.Set("retmode", "json")
	q.Set("retmax", strconv.Itoa(num))
	if params.SortBy == "date" {
		q.Set("sort", "pub_date")
	}
	// Publication-date range → mindate/maxdate with datetype=pdat (matches the
	// sortpubdate we display). Only sent when a bound is given.
	if params.YearFrom > 0 || params.YearTo > 0 {
		q.Set("datetype", "pdat")
		if params.YearFrom > 0 {
			q.Set("mindate", strconv.Itoa(params.YearFrom))
		}
		if params.YearTo > 0 {
			q.Set("maxdate", strconv.Itoa(params.YearTo))
		}
	}
	p.addAuth(q)

	body, err := p.get(ctx, "/esearch.fcgi?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var resp esearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("pubmed: esearch parse: %w", err)
	}
	if resp.ESearchResult.ErrorMsg != "" {
		return nil, fmt.Errorf("pubmed: %s", resp.ESearchResult.ErrorMsg)
	}
	return resp.ESearchResult.IDList, nil
}

// esummaryResponse models the esummary JSON. Records are keyed by PMID under
// "result"; "uids" preserves order. We decode each record loosely.
type pubmedArticleID struct {
	IDType string `json:"idtype"`
	Value  string `json:"value"`
}

type pubmedAuthor struct {
	Name     string `json:"name"`
	AuthType string `json:"authtype"`
}

type pubmedDocSummary struct {
	UID             string            `json:"uid"`
	Title           string            `json:"title"`
	Authors         []pubmedAuthor    `json:"authors"`
	SortPubDate     string            `json:"sortpubdate"`
	PubDate         string            `json:"pubdate"`
	Source          string            `json:"source"`
	FullJournalName string            `json:"fulljournalname"`
	ArticleIDs      []pubmedArticleID `json:"articleids"`
	Error           string            `json:"error"`
}

// esummary fetches document summaries for the PMIDs and maps them to records.
// The second return value maps each result's URL to its PMCID (when the article
// is deposited in PubMed Central); entries are absent when no PMCID is present.
func (p *PubMedProvider) esummary(ctx context.Context, pmids []string) ([]AcademicResult, map[string]string, error) {
	q := url.Values{}
	q.Set("db", "pubmed")
	q.Set("id", strings.Join(pmids, ","))
	q.Set("retmode", "json")
	p.addAuth(q)

	body, err := p.get(ctx, "/esummary.fcgi?"+q.Encode())
	if err != nil {
		return nil, nil, err
	}

	// "result" is a heterogeneous object: "uids":[…] plus one object per PMID.
	var envelope struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, fmt.Errorf("pubmed: esummary parse: %w", err)
	}

	out := make([]AcademicResult, 0, len(pmids))
	pmcids := make(map[string]string)
	for _, pmid := range pmids { // preserve esearch (relevance/date) order
		raw, ok := envelope.Result[pmid]
		if !ok {
			continue
		}
		var doc pubmedDocSummary
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue // skip an unparsable record, don't fail the batch
		}
		if doc.Error != "" {
			continue // per-record error (e.g. bad PMID) — skip, keep the rest
		}
		result := p.toResult(pmid, doc)
		if pmcid := pubmedPMCID(doc.ArticleIDs); pmcid != "" {
			pmcids[result.URL] = pmcid
		}
		out = append(out, result)
	}
	return out, pmcids, nil
}

// toResult maps one PubMed document summary to an AcademicResult.
func (p *PubMedProvider) toResult(pmid string, doc pubmedDocSummary) AcademicResult {
	authors := make([]string, 0, len(doc.Authors))
	for _, a := range doc.Authors {
		if a.AuthType == "Author" && a.Name != "" {
			authors = append(authors, a.Name)
		}
	}
	journal := doc.FullJournalName
	if journal == "" {
		journal = doc.Source
	}
	return AcademicResult{
		Title:   strings.TrimSpace(doc.Title),
		URL:     "https://pubmed.ncbi.nlm.nih.gov/" + pmid + "/",
		DOI:     pubmedDOI(doc.ArticleIDs),
		Authors: authors,
		Journal: journal,
		Year:    pubmedYear(doc.SortPubDate, doc.PubDate),
		Source:  "pubmed",
	}
}

// pubmedDOI extracts the DOI from the articleids list (idtype=="doi"). Returns ""
// when absent — DOI is best-effort (enrichment keys off it when present).
func pubmedDOI(ids []pubmedArticleID) string {
	for _, id := range ids {
		if id.IDType == "doi" {
			return strings.TrimSpace(id.Value)
		}
	}
	return ""
}

// pubmedPMCID extracts the PubMed Central ID from the articleids list
// (idtype=="pmc"). Returns "" when the article isn't deposited in PMC.
func pubmedPMCID(ids []pubmedArticleID) string {
	for _, id := range ids {
		if id.IDType == "pmc" {
			return strings.TrimSpace(id.Value)
		}
	}
	return ""
}

// jatsArticle models the minimal subset of PMC's JATS XML we extract text
// from: the abstract and body paragraphs. efetch (db=pmc) wraps the article in
// a <pmc-articleset> root, hence the leading "article>" path segment. Body
// paragraphs can appear as a lead-in directly under <body> before any <sec>,
// or nested arbitrarily deep inside <sec><sec>...; jatsSection recurses to
// collect both, since Go's xml chained-path tags only match a fixed depth.
type jatsArticle struct {
	Abstract []jatsParagraph `xml:"article>front>article-meta>abstract>p"`
	Body     jatsSection     `xml:"article>body"`
}

type jatsSection struct {
	Paragraphs  []jatsParagraph `xml:"p"`
	Subsections []jatsSection   `xml:"sec"`
}

// paragraphText returns the trimmed, non-empty text of every paragraph in
// this section and all nested subsections, depth-first.
func (s jatsSection) paragraphText() []string {
	var out []string
	for _, p := range s.Paragraphs {
		if t := strings.TrimSpace(p.Text); t != "" {
			out = append(out, t)
		}
	}
	for _, sub := range s.Subsections {
		out = append(out, sub.paragraphText()...)
	}
	return out
}

type jatsParagraph struct {
	Text string `xml:",chardata"`
}

// FetchFullText retrieves and extracts the full text of a PMC open-access
// article via efetch (JATS XML). Returns ("", nil) on a soft failure (not
// found, unparsable XML) — full text is best-effort enrichment, never a hard
// requirement for a search result. Returns a non-nil error only on network
// failure, so callers can distinguish "try again" from "no full text exists".
func (p *PubMedProvider) FetchFullText(ctx context.Context, pmcid string) (string, error) {
	pmcid = strings.TrimSpace(pmcid)
	if pmcid == "" {
		return "", nil
	}
	q := url.Values{}
	q.Set("db", "pmc")
	q.Set("id", pmcid)
	q.Set("rettype", "xml")
	q.Set("retmode", "xml")
	p.addAuth(q)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/efetch.fcgi?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/xml")
	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("pubmed: efetch request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return "", fmt.Errorf("pubmed: efetch rate limited (set PUBMED_API_KEY to raise the limit): %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 400 {
		return "", nil // not found / not OA in PMC — soft failure
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return "", fmt.Errorf("pubmed: efetch read body: %w", err)
	}

	var article jatsArticle
	if err := xml.Unmarshal(body, &article); err != nil {
		return "", nil // malformed XML — soft failure, not propagated
	}

	var parts []string
	for _, p := range article.Abstract {
		if t := strings.TrimSpace(p.Text); t != "" {
			parts = append(parts, t)
		}
	}
	parts = append(parts, article.Body.paragraphText()...)
	return strings.Join(parts, "\n\n"), nil
}

// pubmedYear parses a 4-digit year from sortpubdate ("2026/06/01 00:00") or, as a
// fallback, pubdate ("2026 Jun"). Returns 0 when no year is found.
func pubmedYear(sortPubDate, pubDate string) int {
	for _, s := range []string{sortPubDate, pubDate} {
		s = strings.TrimSpace(s)
		if len(s) >= 4 {
			if y, err := strconv.Atoi(s[:4]); err == nil && y > 1000 {
				return y
			}
		}
	}
	return 0
}

// addAuth appends NCBI's optional contact params: the api_key (raises the rate)
// and tool/email (recommended identification). All best-effort — the API works
// without any of them.
func (p *PubMedProvider) addAuth(q url.Values) {
	q.Set("tool", "web-researcher-mcp")
	if p.apiKey != "" {
		q.Set("api_key", p.apiKey)
	}
	if p.email != "" {
		q.Set("email", p.email)
	}
}

func (p *PubMedProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pubmed: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("pubmed: rate limited (set PUBMED_API_KEY to raise the limit): %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("pubmed: API error %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
}

var _ AcademicProvider = (*PubMedProvider)(nil)

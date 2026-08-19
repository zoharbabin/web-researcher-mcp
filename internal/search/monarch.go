package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// MonarchAPIProvider implements MonarchSearcher over the Monarch Initiative v3
// API (https://api.monarchinitiative.org/v3): a keyless, open biomedical
// knowledge graph linking diseases, genes, phenotypes, and cross-species
// model-organism data, with semantic-similarity (semsim) phenotype matching as
// its flagship capability (#318).
//
// Verified contract (live API, 2026-07):
//   - entity by ID:    GET /v3/api/entity/{id} → 404 on miss (not JSON body)
//   - entity search:   GET /v3/api/search?q=&limit= → {total, items:[...]}
//   - associations:    GET /v3/api/association?subject=&object=&category=&limit=
//     → {total, items:[...]}; "category" filters, "assoc_type" does not exist.
//   - semsim search:   POST /v3/api/semsim/search {termset, group} → array of
//     {subject, score, similarity{subject_best_matches{termId}.similarity.
//     {ancestor_id, ancestor_label}}}. The top-level match_subsumer is always null.
//   - semsim compare:  POST /v3/api/semsim/compare {subjects, objects} → single
//     object with average_score/best_score + the same best_matches shape.
//   - annotate:        GET /v3/api/annotate?text= → HTML string with
//     <span data-sciGraph="Label,CURIE,Category,..."> markup, content-type
//     text/html despite the OpenAPI spec's "application/json" label.
type MonarchAPIProvider struct {
	baseURL string
	deps    Deps
}

// NewMonarchProvider creates the provider. No key required.
func NewMonarchProvider(deps Deps) *MonarchAPIProvider {
	return &MonarchAPIProvider{
		baseURL: "https://api.monarchinitiative.org/v3",
		deps:    deps,
	}
}

func (m *MonarchAPIProvider) Name() string { return "monarch" }

func (m *MonarchAPIProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biomedical", "rare-disease", "phenotype-similarity"},
		RateClass:    "free",
		Description:  "Monarch Initiative — biomedical knowledge graph: disease/gene/phenotype entities, associations, and semantic-similarity phenotype matching",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (m *MonarchAPIProvider) SetBaseURL(base string) { m.baseURL = base }

// curieRe validates a Monarch CURIE (prefix:local_id) before it is embedded in
// a URL path segment — the path-injection guard required by #318.
var curieRe = regexp.MustCompile(`^[A-Za-z0-9._-]+:[A-Za-z0-9._-]+$`)

// ValidCURIE reports whether id has the shape prefix:local_id using only
// characters safe to embed in a URL path segment.
func ValidCURIE(id string) bool { return curieRe.MatchString(id) }

// monarchSemsimGroups is the exhaustive, API-verified SemsimSearchGroup enum.
// "C. Elegans Genes" is literal — the API 422s on any other spelling.
var monarchSemsimGroups = map[string]bool{
	"Human Genes":      true,
	"Mouse Genes":      true,
	"Rat Genes":        true,
	"Zebrafish Genes":  true,
	"C. Elegans Genes": true,
	"Human Diseases":   true,
}

// ValidSemsimGroup reports whether group is a recognized SemsimSearchGroup value.
func ValidSemsimGroup(group string) bool { return monarchSemsimGroups[group] }

func (m *MonarchAPIProvider) Search(ctx context.Context, params MonarchSearchParams) ([]MonarchResult, error) {
	var results []MonarchResult
	err := m.deps.Breaker.Execute(func() error {
		var er error
		switch params.Operation {
		case "semsim":
			results, er = m.semsim(ctx, params)
		case "entity":
			results, er = m.entity(ctx, params)
		case "associations":
			results, er = m.associations(ctx, params)
		case "compare":
			results, er = m.compare(ctx, params)
		case "annotate":
			results, er = m.annotate(ctx, params)
		default:
			er = fmt.Errorf("monarch: unknown operation %q", params.Operation)
		}
		return er
	})
	return results, err
}

func (m *MonarchAPIProvider) semsim(ctx context.Context, params MonarchSearchParams) ([]MonarchResult, error) {
	group := params.Group
	if group == "" {
		group = "Human Diseases"
	}
	body, err := json.Marshal(map[string]any{
		"termset": params.Phenotypes,
		"group":   group,
		"limit":   clampMonarch(params.NumResults, 1, 200),
	})
	if err != nil {
		return nil, fmt.Errorf("monarch: encode semsim request: %w", err)
	}
	respBody, err := m.post(ctx, "/api/semsim/search", body)
	if err != nil {
		return nil, err
	}
	var parsed []monarchSemsimMatch
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("monarch: parse semsim response: %w", err)
	}
	out := make([]MonarchResult, 0, len(parsed))
	for _, match := range parsed {
		ancestorID, ancestorLabel := match.bestAncestor()
		out = append(out, MonarchResult{
			ID:            match.Subject.ID,
			Label:         match.Subject.Name,
			Score:         match.Score,
			Category:      match.Subject.Category,
			AncestorID:    ancestorID,
			AncestorLabel: ancestorLabel,
			Source:        "monarch",
		})
	}
	return out, nil
}

func (m *MonarchAPIProvider) compare(ctx context.Context, params MonarchSearchParams) ([]MonarchResult, error) {
	body, err := json.Marshal(map[string]any{
		"subjects": params.Phenotypes,
		"objects":  params.CompareTo,
	})
	if err != nil {
		return nil, fmt.Errorf("monarch: encode compare request: %w", err)
	}
	respBody, err := m.post(ctx, "/api/semsim/compare", body)
	if err != nil {
		return nil, err
	}
	var parsed monarchCompareResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("monarch: parse compare response: %w", err)
	}
	ancestorID, ancestorLabel := "", ""
	for _, match := range parsed.SubjectBestMatches {
		ancestorID, ancestorLabel = match.bestAncestorSingle()
		break
	}
	return []MonarchResult{{
		Score:         parsed.BestScore,
		AncestorID:    ancestorID,
		AncestorLabel: ancestorLabel,
		Source:        "monarch",
	}}, nil
}

func (m *MonarchAPIProvider) entity(ctx context.Context, params MonarchSearchParams) ([]MonarchResult, error) {
	if params.EntityID != "" {
		if !ValidCURIE(params.EntityID) {
			return nil, fmt.Errorf("monarch: entityId is not a valid CURIE")
		}
		respBody, err := m.get(ctx, "/api/entity/"+url.PathEscape(params.EntityID))
		if err != nil {
			return nil, err
		}
		if respBody == nil {
			return nil, nil // 404 → no record, not an error
		}
		var e monarchEntity
		if err := json.Unmarshal(respBody, &e); err != nil {
			return nil, fmt.Errorf("monarch: parse entity response: %w", err)
		}
		return []MonarchResult{e.toResult()}, nil
	}

	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("limit", strconv.Itoa(clampMonarch(params.NumResults, 1, 200)))
	respBody, err := m.get(ctx, "/api/search?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if respBody == nil {
		return nil, nil
	}
	var parsed struct {
		Items []monarchEntity `json:"items"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("monarch: parse search response: %w", err)
	}
	out := make([]MonarchResult, 0, len(parsed.Items))
	for _, e := range parsed.Items {
		out = append(out, e.toResult())
	}
	return out, nil
}

func (m *MonarchAPIProvider) associations(ctx context.Context, params MonarchSearchParams) ([]MonarchResult, error) {
	q := url.Values{}
	if params.EntityID != "" {
		if !isSafeAssocValue(params.EntityID) {
			return nil, fmt.Errorf("monarch: entityId contains disallowed characters")
		}
		q.Set("entity", params.EntityID)
	}
	if params.AssocSubject != "" {
		if !isSafeAssocValue(params.AssocSubject) {
			return nil, fmt.Errorf("monarch: assocSubject contains disallowed characters")
		}
		q.Set("subject", params.AssocSubject)
	}
	if params.AssocObject != "" {
		if !isSafeAssocValue(params.AssocObject) {
			return nil, fmt.Errorf("monarch: assocObject contains disallowed characters")
		}
		q.Set("object", params.AssocObject)
	}
	if params.AssocCategory != "" {
		if !isSafeAssocValue(params.AssocCategory) {
			return nil, fmt.Errorf("monarch: category contains disallowed characters")
		}
		q.Set("category", params.AssocCategory)
	}
	q.Set("limit", strconv.Itoa(clampMonarch(params.NumResults, 1, 200)))

	respBody, err := m.get(ctx, "/api/association?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if respBody == nil {
		return nil, nil
	}
	var parsed struct {
		Items []monarchAssociation `json:"items"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("monarch: parse association response: %w", err)
	}
	out := make([]MonarchResult, 0, len(parsed.Items))
	for _, a := range parsed.Items {
		out = append(out, MonarchResult{
			SubjectID:              a.Subject,
			SubjectLabel:           a.SubjectLabel,
			ObjectID:               a.Object,
			ObjectLabel:            a.ObjectLabel,
			Category:               a.Category,
			PrimaryKnowledgeSource: a.PrimaryKnowledgeSource,
			Source:                 "monarch",
		})
	}
	return out, nil
}

func (m *MonarchAPIProvider) annotate(ctx context.Context, params MonarchSearchParams) ([]MonarchResult, error) {
	q := url.Values{}
	q.Set("text", params.Text)
	respBody, err := m.get(ctx, "/api/annotate?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if respBody == nil {
		return nil, nil
	}
	terms := parseAnnotateSpans(string(respBody))
	out := make([]MonarchResult, 0, len(terms))
	for _, t := range terms {
		if !isGroundedHPOTerm(t) {
			continue
		}
		out = append(out, MonarchResult{
			ID:     t.id,
			Label:  t.label,
			Text:   t.text,
			Source: "monarch",
		})
	}
	return out, nil
}

// monarchAnnotateStopwords is a minimal deny-list of generic clinical-narrative
// words that carry no diagnostic meaning themselves but that SciGraph's NER can
// still ground to some unrelated specific literature/ontology entity, actively
// misleading a caller who treats every returned term as a confirmed HPO match.
var monarchAnnotateStopwords = map[string]bool{
	"patient": true, "patients": true, "presented": true, "presenting": true,
	"history": true, "reported": true, "noted": true, "male": true, "female": true,
	"man": true, "woman": true, "child": true, "born": true, "year": true,
	"years": true, "old": true, "case": true,
}

// isGroundedHPOTerm reports whether a parsed annotate span meets the
// documented "ground to HPO terms" contract: an HP:-namespace CURIE (excluding
// other Monarch/SciGraph namespaces such as MP:, NCBITaxon:, or literature/case
// references) whose matched text isn't a generic clinical-narrative stopword.
func isGroundedHPOTerm(t groundedTerm) bool {
	if !strings.HasPrefix(t.id, "HP:") {
		return false
	}
	return !monarchAnnotateStopwords[strings.ToLower(strings.TrimSpace(t.text))]
}

// isSafeAssocValue rejects characters that could smuggle extra query params or
// path segments into an association filter value — defense in depth on top of
// url.Values' own encoding.
func isSafeAssocValue(s string) bool {
	return !strings.ContainsAny(s, "&?#/")
}

func clampMonarch(n, min, max int) int {
	if n <= 0 {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func (m *MonarchAPIProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return m.do(req)
}

func (m *MonarchAPIProvider) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return m.do(req)
}

func (m *MonarchAPIProvider) do(req *http.Request) ([]byte, error) {
	resp, err := m.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("monarch: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("monarch: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // not found → empty, not an error
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("monarch: API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

// ── Response types ──────────────────────────────────────────────────────────

type monarchEntity struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Xref        []string `json:"xref"`
}

func (e monarchEntity) toResult() MonarchResult {
	return MonarchResult{
		ID:              e.ID,
		Label:           e.Name,
		Category:        e.Category,
		Description:     e.Description,
		CrossReferences: e.Xref,
		Source:          "monarch",
	}
}

type monarchSimilarityDetail struct {
	AncestorID    string `json:"ancestor_id"`
	AncestorLabel string `json:"ancestor_label"`
}

type monarchBestMatch struct {
	Similarity monarchSimilarityDetail `json:"similarity"`
}

type monarchSemsimMatch struct {
	Subject    monarchEntity `json:"subject"`
	Score      float64       `json:"score"`
	Similarity struct {
		SubjectBestMatches map[string]monarchBestMatch `json:"subject_best_matches"`
	} `json:"similarity"`
}

// bestAncestor reads the shared ontology ancestor from the FIRST entry of
// subject_best_matches — the API nests it three levels deep
// (similarity.subject_best_matches[termId].similarity.{ancestor_id,label}); the
// top-level match_subsumer field is always null, so it must not be read from there.
func (m monarchSemsimMatch) bestAncestor() (string, string) {
	for _, match := range m.Similarity.SubjectBestMatches {
		return match.Similarity.AncestorID, match.Similarity.AncestorLabel
	}
	return "", ""
}

func (m monarchBestMatch) bestAncestorSingle() (string, string) {
	return m.Similarity.AncestorID, m.Similarity.AncestorLabel
}

type monarchCompareResponse struct {
	BestScore          float64                     `json:"best_score"`
	AverageScore       float64                     `json:"average_score"`
	SubjectBestMatches map[string]monarchBestMatch `json:"subject_best_matches"`
}

type monarchAssociation struct {
	Subject                string `json:"subject"`
	SubjectLabel           string `json:"subject_label"`
	Object                 string `json:"object"`
	ObjectLabel            string `json:"object_label"`
	Category               string `json:"category"`
	PrimaryKnowledgeSource string `json:"primary_knowledge_source"`
}

// groundedTerm is one HPO/MONDO/etc. term extracted from an annotate response's
// <span data-sciGraph="Label,CURIE,Category,..."> markup.
type groundedTerm struct {
	text  string
	id    string
	label string
}

// parseAnnotateSpans extracts grounded terms from the annotate endpoint's HTML
// response without a general HTML parser dependency — the markup is a fixed,
// narrow shape (<span class="sciCrunchAnnotation" data-sciGraph="...">text</span>),
// not arbitrary attacker-controlled HTML (it originates from Monarch's own
// annotation service over our own submitted text).
func parseAnnotateSpans(html string) []groundedTerm {
	var terms []groundedTerm
	const dataAttr = `data-sciGraph="`
	rest := html
	for {
		idx := strings.Index(rest, dataAttr)
		if idx == -1 {
			break
		}
		rest = rest[idx+len(dataAttr):]
		end := strings.Index(rest, `"`)
		if end == -1 {
			break
		}
		attrValue := rest[:end]
		rest = rest[end:]

		// Text between the end of the opening tag and the closing </span>.
		tagEnd := strings.Index(rest, ">")
		if tagEnd == -1 {
			break
		}
		afterTag := rest[tagEnd+1:]
		closeIdx := strings.Index(afterTag, "</span>")
		text := ""
		if closeIdx != -1 {
			text = afterTag[:closeIdx]
			rest = afterTag[closeIdx+len("</span>"):]
		} else {
			rest = afterTag
		}

		fields := strings.Split(attrValue, ",")
		if len(fields) >= 2 {
			terms = append(terms, groundedTerm{text: text, label: fields[0], id: fields[1]})
		}
	}
	return terms
}

var _ MonarchProvider = (*MonarchAPIProvider)(nil)

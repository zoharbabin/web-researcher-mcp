package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// Corporate ownership enrichment (#248). The verify_recommendation tool's
// self-promotion signal is lexical-only: it checks whether the domain's brand
// token appears as the rank-1 item on its own page. That misses indirect
// ownership — e.g. adobe.com recommending "Marketo" #1 in a listicle: Marketo
// is Adobe-owned, but the token "adobe" never appears in the item name, so the
// lexical check returns nil. Wikidata's P749 ("parent organization") property
// provides a keyless, authoritative fallback for this pattern.
//
// Like Unpaywall/Crossref (see unpaywall.go, retraction.go), this is an
// ENRICHMENT layer, not a search provider — a two-step lookup (wbsearchentities
// for the QID, then a SPARQL P749 query for the parent) invoked by the tool
// layer when the lexical check finds nothing. Best-effort by contract: any
// failure (network, 429, parse, no entity, no parent) leaves the signal absent
// and never fails the audit.
const (
	wikidataSearchURL = "https://www.wikidata.org/w/api.php"
	wikidataSPARQLURL = "https://query.wikidata.org/sparql"
)

// wikidataQIDPattern validates a Wikidata entity ID before it is substituted
// into the SPARQL query template — the search API's response is external
// input, so the QID is validated defense-in-depth even though it originates
// from Wikidata's own JSON, not a raw user string.
var wikidataQIDPattern = regexp.MustCompile(`^Q[0-9]+$`)

// OwnershipResult is the resolved corporate parent for a brand token.
type OwnershipResult struct {
	OwnerLabel string `json:"ownerLabel"` // e.g. "Adobe Inc."
	OwnerQID   string `json:"ownerQID"`   // e.g. "Q8"
	EntityQID  string `json:"entityQID"`  // QID of the brand itself, e.g. "Q123"
}

// OwnershipResolver resolves a domain brand token to its corporate parent.
// Implemented by WikidataOwnershipResolver; an interface so tools hold a
// nil-able dependency and tests can substitute a fake.
type OwnershipResolver interface {
	// Resolve returns the corporate parent for brandToken. found=false (nil
	// error) means "no entity / no P749 parent" — callers treat that as a
	// clean no-op, never an error.
	Resolve(ctx context.Context, brandToken string) (result *OwnershipResult, found bool, err error)
	Name() string
}

// WikidataOwnershipResolver is the Wikidata implementation of OwnershipResolver:
//  1. wbsearchentities API → QID for brandToken
//  2. SPARQL P749 query → ownerLabel + ownerQID
type WikidataOwnershipResolver struct {
	searchURL string
	sparqlURL string
	deps      Deps
}

// NewWikidataOwnershipResolver constructs the resolver. Both Wikidata APIs are
// keyless, so this is always non-nil.
func NewWikidataOwnershipResolver(deps Deps) *WikidataOwnershipResolver {
	return &WikidataOwnershipResolver{searchURL: wikidataSearchURL, sparqlURL: wikidataSPARQLURL, deps: deps}
}

// SetBaseURLs overrides the API base URLs (testing).
func (w *WikidataOwnershipResolver) SetBaseURLs(searchURL, sparqlURL string) {
	w.searchURL = searchURL
	w.sparqlURL = sparqlURL
}

func (w *WikidataOwnershipResolver) Name() string { return "wikidata-ownership" }

func (w *WikidataOwnershipResolver) Resolve(ctx context.Context, brandToken string) (*OwnershipResult, bool, error) {
	brandToken = strings.TrimSpace(brandToken)
	if brandToken == "" {
		return nil, false, nil
	}

	entityQID, err := w.searchEntity(ctx, brandToken)
	if err != nil {
		return nil, false, err
	}
	if entityQID == "" {
		return nil, false, nil
	}

	ownerLabel, ownerQID, err := w.queryParent(ctx, entityQID)
	if err != nil {
		return nil, false, err
	}
	if ownerLabel == "" || ownerQID == "" {
		return nil, false, nil
	}

	return &OwnershipResult{OwnerLabel: ownerLabel, OwnerQID: ownerQID, EntityQID: entityQID}, true, nil
}

// searchEntity resolves brandToken to a QID via wbsearchentities. Returns ""
// (no error) when Wikidata has no matching entity.
func (w *WikidataOwnershipResolver) searchEntity(ctx context.Context, brandToken string) (string, error) {
	q := url.Values{}
	q.Set("action", "wbsearchentities")
	q.Set("search", brandToken)
	q.Set("language", "en")
	q.Set("format", "json")
	q.Set("limit", "1")
	reqURL := w.searchURL + "?" + q.Encode()

	var qid string
	err := w.deps.Breaker.Execute(func() error {
		req, er := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if er != nil {
			return er
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "web-researcher-mcp")

		resp, er := w.deps.HTTPClient.Do(req)
		if er != nil {
			return er
		}
		defer resp.Body.Close()

		switch {
		case resp.StatusCode == 429:
			return fmt.Errorf("wikidata: search rate limited: %w", circuit.ErrRateLimit)
		case resp.StatusCode >= 400:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("wikidata: search API error %d: %s", resp.StatusCode, string(body))
		}

		data, er := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		if er != nil {
			return er
		}
		var parsed wikidataSearchResponse
		if er := json.Unmarshal(data, &parsed); er != nil {
			return fmt.Errorf("wikidata: search parse: %w", er)
		}
		if len(parsed.Search) == 0 {
			return nil
		}
		candidate := strings.ToUpper(strings.TrimSpace(parsed.Search[0].ID))
		if !wikidataQIDPattern.MatchString(candidate) {
			return nil
		}
		qid = candidate
		return nil
	})
	if err != nil {
		return "", err
	}
	return qid, nil
}

// queryParent runs the P749 SPARQL query for entityQID (already validated
// against wikidataQIDPattern by searchEntity) and returns the parent
// organization's label and QID. Returns ("", "", nil) when there is no P749
// parent.
func (w *WikidataOwnershipResolver) queryParent(ctx context.Context, entityQID string) (label, qid string, err error) {
	if !wikidataQIDPattern.MatchString(entityQID) {
		return "", "", nil
	}
	sparql := fmt.Sprintf(`SELECT ?owner ?ownerLabel WHERE {
  wd:%s wdt:P749 ?owner .
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". }
} LIMIT 1`, entityQID)

	q := url.Values{}
	q.Set("query", sparql)
	q.Set("format", "json")
	reqURL := w.sparqlURL + "?" + q.Encode()

	err = w.deps.Breaker.Execute(func() error {
		req, er := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if er != nil {
			return er
		}
		req.Header.Set("Accept", "application/sparql-results+json")
		req.Header.Set("User-Agent", "web-researcher-mcp")

		resp, er := w.deps.HTTPClient.Do(req)
		if er != nil {
			return er
		}
		defer resp.Body.Close()

		switch {
		case resp.StatusCode == 429:
			return fmt.Errorf("wikidata: sparql rate limited: %w", circuit.ErrRateLimit)
		case resp.StatusCode >= 400:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("wikidata: sparql API error %d: %s", resp.StatusCode, string(body))
		}

		data, er := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		if er != nil {
			return er
		}
		var parsed wikidataSPARQLResponse
		if er := json.Unmarshal(data, &parsed); er != nil {
			return fmt.Errorf("wikidata: sparql parse: %w", er)
		}
		if len(parsed.Results.Bindings) == 0 {
			return nil
		}
		binding := parsed.Results.Bindings[0]
		ownerURI := binding.Owner.Value
		idx := strings.LastIndex(ownerURI, "/")
		if idx < 0 || idx+1 >= len(ownerURI) {
			return nil
		}
		candidate := strings.ToUpper(strings.TrimSpace(ownerURI[idx+1:]))
		if !wikidataQIDPattern.MatchString(candidate) {
			return nil
		}
		qid = candidate
		label = binding.OwnerLabel.Value
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return label, qid, nil
}

type wikidataSearchResponse struct {
	Search []struct {
		ID string `json:"id"`
	} `json:"search"`
}

type wikidataSPARQLResponse struct {
	Results struct {
		Bindings []struct {
			Owner struct {
				Value string `json:"value"`
			} `json:"owner"`
			OwnerLabel struct {
				Value string `json:"value"`
			} `json:"ownerLabel"`
		} `json:"bindings"`
	} `json:"results"`
}

var _ OwnershipResolver = (*WikidataOwnershipResolver)(nil)

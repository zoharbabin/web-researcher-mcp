package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// Authoritative DOI existence check (#226). Crossref's /works/{doi} only knows
// DOIs registered through Crossref; arXiv preprint DOIs (10.48550/*) are
// registered through DataCite and 404 in Crossref while being entirely real.
// OpenAlex likewise no longer carries those arXiv DOIs (it re-mints its own).
//
// The DOI Handle System (doi.org) is the registration-agency-agnostic source of
// truth: every DOI from every registrar resolves there. A single keyless GET to
// the handle API answers "is this DOI registered at all?" regardless of which
// agency minted it — so a real arXiv/DataCite DOI reads as existing and a
// fabricated DOI (valid prefix, nonexistent suffix) reads as not-found, with no
// dependence on any one indexer's coverage.
//
// Like the retraction resolver, this is an ENRICHMENT/verification layer, not a
// search provider, and is best-effort by contract: any transport failure leaves
// existence unknown (registered=false, err!=nil) and never fabricates a verdict.
const doiHandleBaseURL = "https://doi.org/api/handles"

// DOIRegistry answers whether a DOI is registered with any DOI registration
// agency. Implemented by HandleDOIRegistry; an interface so verify_citation holds
// a nil-able dependency and tests can substitute a fake.
type DOIRegistry interface {
	// IsRegistered reports whether the DOI resolves in the global DOI handle
	// system. A non-nil err means the check could not be completed (caller treats
	// existence as unknown, never as absence). registered=false with err==nil is
	// an AUTHORITATIVE "not registered" (the handle API returned not-found).
	IsRegistered(ctx context.Context, doi string) (registered bool, err error)
	Name() string
}

// HandleDOIRegistry is the doi.org handle-API implementation of DOIRegistry.
type HandleDOIRegistry struct {
	baseURL string
	deps    Deps
}

// NewHandleDOIRegistry constructs the resolver. The handle API is keyless, so
// this is always non-nil.
func NewHandleDOIRegistry(deps Deps) *HandleDOIRegistry {
	return &HandleDOIRegistry{baseURL: doiHandleBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (testing).
func (h *HandleDOIRegistry) SetBaseURL(base string) { h.baseURL = base }

func (h *HandleDOIRegistry) Name() string { return "doi-handle" }

// handleResponse is the minimal shape of the doi.org handle-API JSON. The
// responseCode is authoritative: 1 = success (registered), 100 = handle not
// found (NOT registered). HTTP status mirrors this (200 vs 404) but we read the
// body code too so a proxied 200 with code!=1 isn't misread as registered.
type handleResponse struct {
	ResponseCode int `json:"responseCode"`
}

// doiHandleRetryBackoffs bounds the existence check to at most 1+len(...)
// attempts on a transport error or 5xx, modeled on linkverify.go's
// spnRetryBackoffs / github.go's githubAPIRetryBackoffs pattern (issue #549):
// a single transient hiccup on this best-effort verification call must not
// report a genuinely registered DOI as existence-unknown. Never retried on
// 404/429/other 4xx — those are authoritative, not transient.
var doiHandleRetryBackoffs = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}

// doRequest issues a single GET and returns the raw status+body (or a
// transport error). Status-code interpretation happens in the caller so this
// stays a plain, retryable unit.
func (h *HandleDOIRegistry) doRequest(ctx context.Context, reqURL string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := h.deps.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// doRequestWithRetry retries doRequest up to len(doiHandleRetryBackoffs)
// additional times on a transport error or a 5xx response — never on 4xx (a
// 404/429 will not succeed on retry and retrying a rate-limited response
// risks compounding the throttle).
func (h *HandleDOIRegistry) doRequestWithRetry(ctx context.Context, reqURL string) (int, []byte, error) {
	var status int
	var body []byte
	var err error
	for attempt := 0; ; attempt++ {
		status, body, err = h.doRequest(ctx, reqURL)
		if err == nil && status < 500 {
			return status, body, nil
		}
		if attempt >= len(doiHandleRetryBackoffs) {
			return status, body, err
		}
		select {
		case <-ctx.Done():
			return status, body, err
		case <-time.After(doiHandleRetryBackoffs[attempt]):
		}
	}
}

func (h *HandleDOIRegistry) IsRegistered(ctx context.Context, doi string) (bool, error) {
	doi = normalizeDOI(doi)
	if doi == "" {
		return false, nil
	}

	var registered bool
	err := h.deps.Breaker.Execute(func() error {
		// Path-escape each DOI segment while preserving the identifier's own
		// slashes (same defense-in-depth as crossrefEscapeDOI): a "../"-style
		// segment becomes a literal escaped segment that simply 404s.
		reqURL := h.baseURL + "/" + crossrefEscapeDOI(doi)

		statusCode, body, err := h.doRequestWithRetry(ctx, reqURL)
		if err != nil {
			return err
		}

		switch {
		case statusCode == 404:
			// Authoritative "this DOI is not registered" — a clean negative, not
			// an error. (A fabricated DOI lands here.)
			registered = false
			return nil
		case statusCode == 429:
			return fmt.Errorf("doi-handle: rate limited: %w", circuit.ErrRateLimit)
		case statusCode >= 400:
			return fmt.Errorf("doi-handle: API error %d: %s", statusCode, string(body[:min(len(body), 512)]))
		}

		var hr handleResponse
		if er := json.Unmarshal(body, &hr); er != nil {
			return fmt.Errorf("doi-handle: parse: %w", er)
		}
		registered = hr.ResponseCode == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return registered, nil
}

var _ DOIRegistry = (*HandleDOIRegistry)(nil)

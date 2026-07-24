package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// Unpaywall (by OurResearch, the OpenAlex team) maps a DOI to a legal
// open-access copy. It is NOT a search provider — it's an ENRICHMENT layer over
// the academic pipeline: after academic_search returns DOI-bearing results, each
// is optionally resolved to populate pdfUrl + openAccess WHEN the underlying
// provider didn't already supply them. Best-effort by contract: any Unpaywall
// failure leaves the result unenriched and never fails the search.
//
// Live-verified (2026-06-04): GET /v2/{DOI}?email= → {is_oa, oa_status,
// best_oa_location{url_for_pdf, url_for_landing_page}}. Email-only auth (reuses
// the OpenAlex/CrossRef email convention). 100k requests/day.
const unpaywallBaseURL = "https://api.unpaywall.org/v2"

// OAResolver resolves a DOI to its best open-access location. Implemented by
// UnpaywallResolver; an interface so the academic tool can hold a nil-able
// dependency and tests can substitute a fake.
type OAResolver interface {
	// Resolve returns the open-access status and a best-effort PDF URL for a DOI.
	// found=false (with nil error) means "no OA copy / not resolvable" — callers
	// must treat that as a no-op, never an error.
	Resolve(ctx context.Context, doi string) (oa bool, pdfURL string, found bool, err error)
	Name() string
}

// UnpaywallResolver is the Unpaywall implementation of OAResolver.
type UnpaywallResolver struct {
	email   string
	baseURL string
	deps    Deps
}

// NewUnpaywallResolver constructs the resolver. email is required by Unpaywall
// (polite identification); an empty email yields a nil resolver so the caller
// simply skips enrichment.
func NewUnpaywallResolver(email string, deps Deps) *UnpaywallResolver {
	if strings.TrimSpace(email) == "" {
		return nil
	}
	return &UnpaywallResolver{email: email, baseURL: unpaywallBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (used in testing).
func (u *UnpaywallResolver) SetBaseURL(base string) { u.baseURL = base }

func (u *UnpaywallResolver) Name() string { return "unpaywall" }

func (u *UnpaywallResolver) Resolve(ctx context.Context, doi string) (bool, string, bool, error) {
	doi = strings.TrimSpace(doi)
	if doi == "" {
		return false, "", false, nil
	}
	// Normalize: accept a bare DOI or a doi.org URL.
	doi = strings.TrimPrefix(doi, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")

	var oa bool
	var pdf string
	var found bool
	err := u.deps.Breaker.Execute(func() error {
		reqURL := fmt.Sprintf("%s/%s?email=%s", u.baseURL, url.PathEscape(doi), url.QueryEscape(u.email))
		req, er := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if er != nil {
			return er
		}
		req.Header.Set("Accept", "application/json")

		resp, er := u.deps.HTTPClient.Do(req)
		if er != nil {
			return er
		}
		defer resp.Body.Close()

		if resp.StatusCode == 404 {
			// DOI unknown to Unpaywall — a legitimate "no OA info", not an error.
			return nil
		}
		if resp.StatusCode == 429 {
			return fmt.Errorf("unpaywall: rate limited: %w", circuit.ErrRateLimit)
		}
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("unpaywall: API error %d: %s", resp.StatusCode, string(body))
		}

		data, er := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
		if er != nil {
			return er
		}
		var ur unpaywallResponse
		if er := json.Unmarshal(data, &ur); er != nil {
			return fmt.Errorf("unpaywall: parse: %w", er)
		}
		found = true
		oa = ur.IsOA
		if ur.BestOALocation != nil {
			if ur.BestOALocation.URLForPDF != "" {
				pdf = ur.BestOALocation.URLForPDF
			} else {
				pdf = ur.BestOALocation.URLForLanding
			}
		}
		return nil
	})
	if err != nil {
		return false, "", false, err
	}
	return oa, pdf, found, nil
}

type unpaywallResponse struct {
	IsOA           bool   `json:"is_oa"`
	OAStatus       string `json:"oa_status"`
	BestOALocation *struct {
		URLForPDF     string `json:"url_for_pdf"`
		URLForLanding string `json:"url_for_landing_page"`
	} `json:"best_oa_location"`
}

// EnrichOpenAccess fills pdfUrl/openAccess on DOI-bearing results that lack them,
// using the resolver. It NEVER overwrites a provider-supplied PDFUrl, and it
// degrades gracefully: a resolver error on any one DOI leaves that result
// unchanged and does not abort the batch. A nil resolver is a no-op (returns the
// input unchanged), so callers needn't branch on configuration. Bounded: only
// results that are missing a PDFUrl are resolved.
func EnrichOpenAccess(ctx context.Context, resolver OAResolver, results []AcademicResult) []AcademicResult {
	if resolver == nil {
		return results
	}
	for i := range results {
		r := &results[i]
		if r.DOI == "" || r.PDFUrl != "" {
			continue // nothing to resolve, or provider already supplied it (never overwrite)
		}
		oa, pdf, found, err := resolver.Resolve(ctx, r.DOI)
		if err != nil || !found {
			continue // best-effort: leave unenriched
		}
		if oa {
			r.OpenAccess = true
		}
		if pdf != "" {
			r.PDFUrl = pdf
		}
	}
	return results
}

var _ OAResolver = (*UnpaywallResolver)(nil)

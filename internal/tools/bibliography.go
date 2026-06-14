package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// format_bibliography (#86) renders a citations list in APA, MLA, or BibTeX.
// Sources come either from a sequential_search session (its recorded sources) or
// from an explicit list the caller supplies — useful for assembling a
// bibliography from academic_search / citation_graph results. Read-only,
// idempotent: same inputs → byte-identical output. De-duplicates by URL and
// orders deterministically.

type bibliographySource struct {
	URL    string `json:"url" jsonschema:"Source URL (required for an entry to be included)."`
	Title  string `json:"title,omitempty" jsonschema:"Title of the work."`
	Author string `json:"author,omitempty" jsonschema:"Author(s); separate multiple with ';' or ' and '. First surname is used for the BibTeX cite key."`
	Site   string `json:"site,omitempty" jsonschema:"Publication, site, or journal name."`
	Date   string `json:"date,omitempty" jsonschema:"Publication date or year (used for the year field / cite key)."`
	DOI    string `json:"doi,omitempty" jsonschema:"Digital Object Identifier (e.g. 10.1038/nature12373). Emitted into bibtex/ris/csl-json so a reference manager keeps the persistent id. Pass academic_search/citation_graph results' doi here."`
}

type formatBibliographyInput struct {
	Style     string               `json:"style,omitempty" jsonschema:"Citation style: apa (default), mla, bibtex, ris, or csl-json. apa/mla are human-readable; bibtex/ris/csl-json are reference-manager interchange formats."`
	SessionID string               `json:"sessionId,omitempty" jsonschema:"Build the bibliography from this sequential_search session's recorded sources. Provide this OR sources."`
	Sources   []bibliographySource `json:"sources,omitempty" jsonschema:"Explicit list of sources to format. Provide this OR sessionId. Each needs at least a url."`
}

func registerFormatBibliography(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "format_bibliography",
		Description:  "Turn a set of sources into a formatted bibliography. Choose a human-readable style (apa, mla) or a reference-manager interchange format (bibtex, ris, csl-json) that imports straight into Zotero, EndNote, or Mendeley. Give it either a sequential_search sessionId (it uses the session's recorded sources) or an explicit list of sources (url, title, author, site, date, doi) — for example the results of academic_search or citation_graph (pass their doi so the persistent id survives). Entries are de-duplicated by URL and ordered deterministically, so the same inputs always produce byte-identical output (no network, no timestamps). Read-only and idempotent. Use research_export for the full narrative report and verify_citation to confirm a citation before you rely on it; this builds the citations section. Returns the bibliography as a single string plus the entry count.",
		Annotations:  readOnlyAnnotations(true, false),
		OutputSchema: formatBibliographyOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input formatBibliographyInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		style := strings.ToLower(strings.TrimSpace(input.Style))
		if style == "" {
			style = "apa"
		}
		valid := false
		for _, s := range content.SupportedBibStyles {
			if s == style {
				valid = true
				break
			}
		}
		if !valid {
			return toolError(fmt.Sprintf("invalid style %q; use apa, mla, bibtex, ris, or csl-json", input.Style)), nil, nil
		}

		if input.SessionID == "" && len(input.Sources) == 0 {
			return toolError("provide either a sessionId or a non-empty sources list"), nil, nil
		}

		var entries []content.BibEntry

		if input.SessionID != "" {
			tenantID := auth.TenantIDFromContext(ctx)
			userID := auth.UserIDFromContext(ctx)
			sess, err := deps.Sessions.GetFull(tenantID, userID, input.SessionID)
			if err != nil || sess == nil {
				recordToolCall(deps, "format_bibliography", time.Since(start), err, "upstream_error", false)
				auditToolCall(ctx, deps, "format_bibliography", time.Since(start), err, "upstream_error")
				return toolError("Session not found or expired. Sessions last 4 hours from last activity."), nil, nil
			}
			for _, s := range sess.Sources {
				entries = append(entries, content.BibEntry{URL: s.URL, Title: s.Title})
			}
		}

		for i, s := range input.Sources {
			// Validate the URL at the boundary: a bibliography URL must be a real
			// http(s) URL (or a bare DOI), never a dangerous scheme like
			// "javascript:" or arbitrary junk that would land verbatim in a citation
			// the user may paste elsewhere.
			if err := validateBibliographyURL(s.URL); err != nil {
				return toolError(fmt.Sprintf("sources[%d]: %v", i, err)), nil, nil
			}
			entry := content.BibEntry{
				URL:    s.URL,
				Title:  s.Title,
				Author: s.Author,
				Site:   s.Site,
				Date:   s.Date,
				DOI:    s.DOI,
			}
			// DOI enrichment: when a DOI is present but the title is empty,
			// attempt an exact-entity lookup so the formatted citation is
			// complete rather than reduced to a bare URL. The network call is
			// best-effort — a miss leaves the entry as-is and never errors.
			if entry.DOI != "" && entry.Title == "" {
				if rec := resolveBibEntryByDOI(ctx, deps.AcademicProviders, entry.DOI); rec != nil {
					if rec.Title != "" {
						entry.Title = rec.Title
					}
					if entry.Author == "" && len(rec.Authors) > 0 {
						entry.Author = strings.Join(rec.Authors, "; ")
					}
					if entry.Site == "" && rec.Journal != "" {
						entry.Site = rec.Journal
					}
					if entry.Date == "" && rec.Year > 0 {
						entry.Date = fmt.Sprintf("%d", rec.Year)
					}
				}
			}
			entries = append(entries, entry)
		}

		// entryCount comes back authoritative from the formatter (unique entries
		// post-dedup) — never re-derived from the string, which a malformed title
		// containing a blank line could inflate.
		biblio, entryCount := content.FormatBibliography(entries, style)

		output := map[string]any{
			"style":        style,
			"entryCount":   entryCount,
			"bibliography": biblio,
			"trust":        untrustedContentTrust,
		}
		if input.SessionID != "" {
			output["sessionId"] = input.SessionID
		}

		jsonBytes, _ := json.Marshal(output)
		recordToolCall(deps, "format_bibliography", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "format_bibliography", time.Since(start), nil, "")
		return structuredResult(jsonBytes), nil, nil
	})
}

// resolveBibEntryByDOI performs an exact-entity DOI lookup against the first
// configured academic provider that implements search.DOIResolver.  Returns nil
// when no provider is available, the DOI has no record, or any call errors — the
// caller always treats a nil as "use the entry as-is" so the tool never errors
// on a failed enrichment.
//
// The canonical provider order from search.SupportedAcademicProviders is used so
// the enrichment is deterministic across calls: openalex → crossref → pubmed → ….
func resolveBibEntryByDOI(ctx context.Context, providers map[string]search.AcademicProvider, doi string) *search.AcademicResult {
	for _, name := range search.SupportedAcademicProviders {
		ap, ok := providers[name]
		if !ok {
			continue
		}
		dr, ok := ap.(search.DOIResolver)
		if !ok {
			continue
		}
		rec, err := dr.ResolveByDOI(ctx, doi)
		if err == nil && rec != nil {
			return rec
		}
	}
	return nil
}

// validateBibliographyURL accepts a well-formed http(s) URL or a bare DOI (the two
// legitimate forms a citation's identifier takes), and rejects empty, malformed,
// or dangerous-scheme values (e.g. "javascript:") that would otherwise land
// verbatim in a generated citation. A DOI is allowed because academic_search /
// citation_graph results carry one as the persistent id.
func validateBibliographyURL(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fmt.Errorf("url is required")
	}
	if detectDOI(s) != "" {
		return nil // a bare or doi.org DOI is a valid citation identifier
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid url %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("url %q must be an http(s) URL or a DOI", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("url %q has no host", raw)
	}
	return nil
}

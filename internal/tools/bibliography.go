package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
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
	Author string `json:"author,omitempty" jsonschema:"Author(s); first surname is used for the BibTeX cite key."`
	Site   string `json:"site,omitempty" jsonschema:"Publication, site, or journal name."`
	Date   string `json:"date,omitempty" jsonschema:"Publication date or year (used for the year field / cite key)."`
}

type formatBibliographyInput struct {
	Style     string               `json:"style,omitempty" jsonschema:"Citation style: apa (default), mla, or bibtex."`
	SessionID string               `json:"sessionId,omitempty" jsonschema:"Build the bibliography from this sequential_search session's recorded sources. Provide this OR sources."`
	Sources   []bibliographySource `json:"sources,omitempty" jsonschema:"Explicit list of sources to format. Provide this OR sessionId. Each needs at least a url."`
}

func registerFormatBibliography(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "format_bibliography",
		Description:  "Turn a set of sources into a formatted bibliography in APA, MLA, or BibTeX. Give it either a sequential_search sessionId (it uses the session's recorded sources) or an explicit list of sources (url, title, author, site, date) — for example the results of academic_search or citation_graph. Entries are de-duplicated by URL and ordered deterministically, so the same inputs always produce the same list. Use research_export for the full narrative report and this for the citations section. Returns the bibliography as a single string plus the entry count.",
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
			return toolError(fmt.Sprintf("invalid style %q; use apa, mla, or bibtex", input.Style)), nil, nil
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

		for _, s := range input.Sources {
			entries = append(entries, content.BibEntry{
				URL:    s.URL,
				Title:  s.Title,
				Author: s.Author,
				Site:   s.Site,
				Date:   s.Date,
			})
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

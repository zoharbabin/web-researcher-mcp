package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Large tool payloads (a full raw scrape, a search_and_scrape over many pages, a
// research_export bundle) can be tens to hundreds of KB. Inlining them straight
// into the model's context is wasteful when the client supports the 2025-06-18
// MCP `resource_link` content type: the heavy body is stored once as a
// retrievable resource and the tool returns a small inline summary plus a link
// the client fetches on demand (reported up to ~99% context-payload reduction).
//
// Design (KISS, zero new deps, local/cloud parity):
//   - The artifact body lives in the EXISTING deps.Cache (memory + AES-encrypted
//     disk, or Redis in HTTP multi-pod) under a content-addressed key, with a
//     bounded TTL. No new store, no new persistence path to secure.
//   - It is exposed read-only via the `research://artifact/{id}` resource
//     template (registered in RegisterAll). The id is the SHA-256 of the body, so
//     the same payload de-dupes and the URI is stable/idempotent.
//   - A tool over the threshold returns a resource_link + a small inline JSON
//     summary; below the threshold it inlines exactly as before (no regression).

// artifactURIScheme/Prefix define the resource URI namespace for linked payloads.
const (
	artifactURIPrefix = "research://artifact/"
	// artifactURITemplate is the RFC 6570 template registered with the SDK.
	artifactURITemplate = "research://artifact/{id}"
	// artifactTTL bounds how long a linked payload remains fetchable. Long enough
	// for a client to follow the link within a research turn, short enough that the
	// cache isn't a long-term blob store.
	artifactTTL = 30 * time.Minute
	// artifactCacheKeyPrefix namespaces artifact bodies in the shared cache so they
	// never collide with tool response caches.
	artifactCacheKeyPrefix = "artifact|v1|"
	// linkThresholdBytes is the inline/link cutoff. Payloads at or above this are
	// linked; smaller ones inline unchanged. Sized so typical structured results
	// (search hits, a single scrape summary) stay inline while raw bodies and
	// multi-page bundles link.
	linkThresholdBytes = 24 * 1024
	// artifactMIMEType is the stored body's media type (all artifacts are JSON).
	artifactMIMEType = "application/json"
)

// artifactID returns the content-addressed id (hex SHA-256) for a payload.
func artifactID(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// artifactCacheKey maps an artifact id to its key in the shared cache.
func artifactCacheKey(id string) string { return artifactCacheKeyPrefix + id }

// storeArtifact writes payload to the shared cache under its content-addressed
// key and returns the resource URI to link. Idempotent: the same bytes always
// yield the same id/URI. A nil cache disables artifacts (caller inlines instead).
func storeArtifact(ctx context.Context, deps Dependencies, payload []byte) (uri, id string, ok bool) {
	if deps.Cache == nil {
		return "", "", false
	}
	id = artifactID(payload)
	deps.Cache.Set(ctx, artifactCacheKey(id), payload, artifactTTL)
	return artifactURIPrefix + id, id, true
}

// linkSummary is the small inline body returned alongside a resource_link. It
// carries enough provenance for the model to decide whether to fetch the full
// artifact: what it is, how big, where to get it, and when it expires.
type linkSummary struct {
	Resource  string `json:"resource"`          // the research://artifact/{id} URI to fetch
	Bytes     int    `json:"bytes"`             // size of the linked payload
	MIMEType  string `json:"mimeType"`          // always application/json
	Summary   string `json:"summary,omitempty"` // human-facing one-liner about the contents
	ExpiresAt string `json:"expiresAt"`         // RFC 3339 — when the link stops resolving
	Linked    bool   `json:"linked"`            // always true on this path (provenance marker)
}

// largeResultOrInline returns the right CallToolResult for a payload:
//   - below linkThresholdBytes (or when artifacts are unavailable): the payload
//     inlined exactly as structuredResult would (no behavior change).
//   - at/above the threshold: a resource_link to the stored artifact plus a small
//     inline summary (the `summary` one-liner + size + URI + expiry), so the heavy
//     body stays out of context until the client fetches it.
//
// summary is a short human-facing description of the linked contents (e.g.
// "raw page content for https://… (142 KB)"); it MUST NOT embed the full body.
func largeResultOrInline(ctx context.Context, deps Dependencies, payload []byte, summary string) *mcp.CallToolResult {
	if len(payload) < linkThresholdBytes {
		return structuredResult(payload)
	}
	uri, _, ok := storeArtifact(ctx, deps, payload)
	if !ok {
		// No cache to back the link — fall back to inlining (correctness over size).
		return structuredResult(payload)
	}
	size := len(payload)
	expires := time.Now().UTC().Add(artifactTTL).Format(time.RFC3339)
	sumBytes, _ := json.Marshal(linkSummary{
		Resource:  uri,
		Bytes:     size,
		MIMEType:  artifactMIMEType,
		Summary:   summary,
		ExpiresAt: expires,
		Linked:    true,
	})
	sizeInt := int64(size)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			// The inline summary first (small, structured), then the link the client
			// can follow to fetch the full body on demand.
			&mcp.TextContent{Text: string(sumBytes)},
			&mcp.ResourceLink{
				URI:         uri,
				Name:        "research-artifact",
				Title:       summary,
				Description: "Full tool payload (" + humanBytes(size) + "), linked to keep it out of context until needed. Fetch this resource to read it.",
				MIMEType:    artifactMIMEType,
				Size:        &sizeInt,
			},
		},
		StructuredContent: json.RawMessage(sumBytes),
	}
}

// registerArtifactResource wires the read side of the artifact link: a resource
// template that serves a stored payload by id from the shared cache. Read-only,
// no auth beyond the transport's (artifacts are content-addressed and short-lived,
// carry only data the same caller just produced). A miss (expired/unknown id)
// returns a not-found error, never another tool's data.
func registerArtifactResource(srv *mcp.Server, deps Dependencies) {
	if deps.Cache == nil {
		return // no backing store → no artifacts → nothing to serve
	}
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: artifactURITemplate,
		Name:        "research-artifact",
		Title:       "Linked research artifact",
		Description: "A large tool payload (raw scrape, multi-page search_and_scrape, or research_export bundle) linked instead of inlined to save context. Short-lived and content-addressed.",
		MIMEType:    artifactMIMEType,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id := strings.TrimPrefix(req.Params.URI, artifactURIPrefix)
		if id == "" || strings.Contains(id, "/") {
			return nil, fmt.Errorf("invalid artifact URI: %s", req.Params.URI)
		}
		body, ok := deps.Cache.Get(ctx, artifactCacheKey(id))
		if !ok {
			// Expired or never existed. Mirror the SDK's not-found shape.
			return nil, fmt.Errorf("artifact not found (it may have expired): %s", req.Params.URI)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: artifactMIMEType,
					Text:     string(body),
				},
			},
		}, nil
	})
}

// humanBytes renders a byte count compactly for descriptions (e.g. "142 KB").
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

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
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// Wayback Machine CDX enrichment (#323): a keyless index of every URL the
// Internet Archive has ever captured for a domain, with timestamp/status/mime
// per capture. Used by company_recon to build a historical URL inventory
// (exposed endpoints, dead paths, old subdomains) without crawling anything
// live. This is a SEARCH-LAYER CLIENT, consumed directly by the tool.
//
// Verified API contract (issue #323): GET /cdx/search/cdx?url={domain}/*
// &output=json&collapse=urlkey&fl=original,timestamp,statuscode,mimetype
// returns an array-of-arrays: the FIRST row is the field-name header
// (["original","timestamp","statuscode","mimetype"]), every row after is data
// in that same column order.
const waybackCDXBaseURL = "https://web.archive.org/cdx/search/cdx"

// waybackDomainPattern validates a domain before interpolation into the CDX
// url= query param — mirrors ctDomainPattern (ct_logs.go); defense in depth on
// top of the caller's canonicalDomain() validation.
var waybackDomainPattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

// waybackMinSpacing follows the issue's documented ~1 req/2sec politeness
// recommendation for anonymous CDX callers.
const waybackMinSpacing = 2100 * time.Millisecond

// waybackThrottle serializes Wayback CDX requests process-wide. Same
// reservation-then-wait shape as s2Throttle (semanticscholar.go) and
// ctLogsThrottle (ct_logs.go); a distinct var because the rate limit is
// independent of both.
var waybackThrottle struct {
	mu   sync.Mutex
	next time.Time
}

func waybackWait(ctx context.Context) error {
	waybackThrottle.mu.Lock()
	now := time.Now()
	slot := waybackThrottle.next
	if slot.Before(now) {
		slot = now
	}
	waybackThrottle.next = slot.Add(waybackMinSpacing)
	waybackThrottle.mu.Unlock()

	d := time.Until(slot)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ArchiveEntry is one Wayback CDX row, with an inferred Category so callers
// can triage a large inventory without re-parsing every URL themselves.
type ArchiveEntry struct {
	URL        string `json:"url"`
	Timestamp  string `json:"timestamp"` // yyyyMMddHHmmss
	StatusCode string `json:"status_code"`
	MimeType   string `json:"mime_type"`
	Category   string `json:"category"` // login|api|admin|asset|doc|other
}

// ArchiveResolver looks up historical Wayback Machine captures for a domain.
// Implemented by WaybackCDXResolver; an interface so company_recon holds a
// nil-able dependency and tests can substitute a fake.
type ArchiveResolver interface {
	Lookup(ctx context.Context, domain string, maxResults int) ([]ArchiveEntry, error)
	Name() string
}

// WaybackCDXResolver is the Wayback CDX implementation of ArchiveResolver.
type WaybackCDXResolver struct {
	baseURL string
	deps    Deps
}

// NewWaybackCDXResolver constructs the resolver. The Wayback CDX API is
// keyless, so this is always non-nil.
func NewWaybackCDXResolver(deps Deps) *WaybackCDXResolver {
	return &WaybackCDXResolver{baseURL: waybackCDXBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (testing).
func (w *WaybackCDXResolver) SetBaseURL(base string) { w.baseURL = base }

func (w *WaybackCDXResolver) Name() string { return "wayback-cdx" }

// Lookup returns ArchiveEntry rows for a validated domain, filtered to
// 200/301/302 status codes by default (issue #323 acceptance criterion:
// "skips non-200/301/302 entries by default"). maxResults is passed through to
// the CDX `limit` param, clamped to a sane range.
func (w *WaybackCDXResolver) Lookup(ctx context.Context, domain string, maxResults int) ([]ArchiveEntry, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !waybackDomainPattern.MatchString(domain) {
		return nil, fmt.Errorf("wayback: invalid domain %q", domain)
	}
	limit := clamp(maxResults, 1, 1000)

	var entries []ArchiveEntry
	err := w.deps.Breaker.Execute(func() error {
		if er := waybackWait(ctx); er != nil {
			return er
		}

		q := url.Values{}
		q.Set("url", domain+"/*")
		q.Set("output", "json")
		q.Set("collapse", "urlkey")
		q.Set("limit", strconv.Itoa(limit))
		q.Set("fl", "original,timestamp,statuscode,mimetype")
		reqURL := w.baseURL + "?" + q.Encode()

		req, er := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if er != nil {
			return er
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "web-researcher-mcp (company_recon)")

		resp, er := w.deps.HTTPClient.Do(req)
		if er != nil {
			return er
		}
		defer resp.Body.Close()

		if resp.StatusCode == 429 {
			return fmt.Errorf("wayback: rate limited: %w", circuit.ErrRateLimit)
		}
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("wayback: API error %d: %s", resp.StatusCode, string(body))
		}

		data, er := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
		if er != nil {
			return er
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" || trimmed == "[]" {
			return nil
		}
		var rows [][]string
		if er := json.Unmarshal(data, &rows); er != nil {
			return fmt.Errorf("wayback: parse: %w", er)
		}
		entries = waybackRowsToEntries(rows)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// waybackRowsToEntries converts the CDX array-of-arrays (header row + data
// rows) into typed entries, skipping the header itself, any short/malformed
// row, and any non-200/301/302 status (per the issue's default-filter
// acceptance criterion). The header's column order is honored rather than
// assumed positional, since the caller controls fl= but a future edit to the
// query params shouldn't silently misalign fields.
func waybackRowsToEntries(rows [][]string) []ArchiveEntry {
	if len(rows) < 2 {
		return nil
	}
	header := rows[0]
	col := make(map[string]int, len(header))
	for i, name := range header {
		col[name] = i
	}
	urlIdx, okURL := col["original"]
	tsIdx, okTS := col["timestamp"]
	statusIdx, okStatus := col["statuscode"]
	mimeIdx, okMime := col["mimetype"]
	if !okURL || !okTS {
		return nil
	}

	var out []ArchiveEntry
	for _, row := range rows[1:] {
		if urlIdx >= len(row) || tsIdx >= len(row) {
			continue
		}
		status := ""
		if okStatus && statusIdx < len(row) {
			status = row[statusIdx]
		}
		if status != "200" && status != "301" && status != "302" {
			continue
		}
		mime := ""
		if okMime && mimeIdx < len(row) {
			mime = row[mimeIdx]
		}
		entryURL := row[urlIdx]
		out = append(out, ArchiveEntry{
			URL:        entryURL,
			Timestamp:  row[tsIdx],
			StatusCode: status,
			MimeType:   mime,
			Category:   categorizeArchiveURL(entryURL),
		})
	}
	return out
}

// categorizeArchiveURL infers a coarse category from URL path patterns so
// callers can triage a large historical inventory (e.g. surface login/admin
// endpoints first) without re-parsing every URL themselves.
func categorizeArchiveURL(rawURL string) string {
	lower := strings.ToLower(rawURL)
	path := lower
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		path = strings.ToLower(u.Path)
	}
	switch {
	case strings.Contains(path, "/login") || strings.Contains(path, "/signin") || strings.Contains(path, "/auth"):
		return "login"
	case strings.Contains(path, "/api/") || strings.HasPrefix(path, "/api"):
		return "api"
	case strings.Contains(path, "/admin") || strings.Contains(path, "/wp-admin") || strings.Contains(path, "/dashboard"):
		return "admin"
	case strings.HasSuffix(path, ".pdf") || strings.HasSuffix(path, ".doc") || strings.HasSuffix(path, ".docx") ||
		strings.HasSuffix(path, ".xls") || strings.HasSuffix(path, ".xlsx") || strings.HasSuffix(path, ".ppt") || strings.HasSuffix(path, ".pptx"):
		return "doc"
	case strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".png") ||
		strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") || strings.HasSuffix(path, ".gif") ||
		strings.HasSuffix(path, ".svg") || strings.HasSuffix(path, ".woff") || strings.HasSuffix(path, ".woff2") || strings.HasSuffix(path, ".ico"):
		return "asset"
	default:
		return "other"
	}
}

var _ ArchiveResolver = (*WaybackCDXResolver)(nil)

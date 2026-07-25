package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// Certificate Transparency log enrichment (#323) via crt.sh. Every publicly
// trusted TLS certificate is logged to CT, and crt.sh indexes the logs and
// exposes a keyless JSON search — the cheapest way to enumerate a domain's
// subdomains without brute-forcing DNS. This is a SEARCH-LAYER CLIENT (not an
// AcademicProvider/etc.) consumed directly by the company_recon tool.
//
// Verified API contract (issue #323): GET /?q=%25.{domain}&output=json returns
// a JSON array of certificate rows; each row's name_value field holds one or
// more SANs, "\n"-separated when a single cert covers multiple names.
const ctLogsBaseURL = "https://crt.sh/"

// ctDomainPattern validates a domain before it is interpolated into the crt.sh
// query string — defense in depth against injection via a malformed Target,
// on top of the caller's own canonicalDomain() validation.
var ctDomainPattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

// ctLogsMinSpacing keeps us within crt.sh's informally recommended ~1 req/sec
// for anonymous callers (there is no documented hard limit, but crt.sh is a
// shared community resource run on donated infrastructure).
const ctLogsMinSpacing = 1100 * time.Millisecond

// ctLogsThrottle serializes crt.sh requests process-wide. See s2Throttle
// (semanticscholar.go) for the identical reservation-then-wait pattern this
// mirrors; kept as a distinct package-level var since the two APIs have
// independent, unrelated rate limits.
var ctLogsThrottle struct {
	mu   sync.Mutex
	next time.Time
}

func ctLogsWait(ctx context.Context) error {
	ctLogsThrottle.mu.Lock()
	now := time.Now()
	slot := ctLogsThrottle.next
	if slot.Before(now) {
		slot = now
	}
	ctLogsThrottle.next = slot.Add(ctLogsMinSpacing)
	ctLogsThrottle.mu.Unlock()

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

// CertEntry is one crt.sh certificate row, reduced to the fields company_recon
// needs. Domain is a single SAN (crt.sh's name_value split on "\n").
type CertEntry struct {
	Domain    string `json:"domain"`
	Issuer    string `json:"issuer"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
	LoggedAt  string `json:"logged_at"`
}

// CTLogResolver looks up Certificate Transparency log entries for a domain.
// Implemented by CrtShResolver; an interface so company_recon holds a nil-able
// dependency and tests can substitute a fake.
type CTLogResolver interface {
	Lookup(ctx context.Context, domain string, maxResults int) ([]CertEntry, error)
	Name() string
}

// CrtShResolver is the crt.sh implementation of CTLogResolver.
type CrtShResolver struct {
	baseURL string
	deps    Deps
}

// NewCrtShResolver constructs the resolver. crt.sh is keyless, so this is
// always non-nil — unlike NewUnpaywallResolver, there is no required
// credential that could leave it unconfigured.
func NewCrtShResolver(deps Deps) *CrtShResolver {
	return &CrtShResolver{baseURL: ctLogsBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (testing).
func (c *CrtShResolver) SetBaseURL(base string) { c.baseURL = base }

func (c *CrtShResolver) Name() string { return "crt.sh" }

// Lookup returns deduplicated CertEntry rows for a validated domain, most
// recently logged first. maxResults caps the number of ENTRIES returned (not
// the number of raw crt.sh rows, since one row can expand to many SANs);
// non-positive means "no cap."
func (c *CrtShResolver) Lookup(ctx context.Context, domain string, maxResults int) ([]CertEntry, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !ctDomainPattern.MatchString(domain) {
		return nil, fmt.Errorf("crt.sh: invalid domain %q", domain)
	}

	var entries []CertEntry
	err := c.deps.Breaker.Execute(func() error {
		if er := ctLogsWait(ctx); er != nil {
			return er
		}

		q := url.Values{}
		q.Set("q", "%."+domain)
		q.Set("output", "json")
		reqURL := c.baseURL + "?" + q.Encode()

		req, er := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if er != nil {
			return er
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "web-researcher-mcp (company_recon)")

		resp, er := c.deps.HTTPClient.Do(req)
		if er != nil {
			return er
		}
		defer resp.Body.Close()

		if resp.StatusCode == 429 {
			return fmt.Errorf("crt.sh: rate limited: %w", circuit.ErrRateLimit)
		}
		if resp.StatusCode >= 500 {
			// crt.sh's shared infra is known to intermittently 502/503; degrade to
			// a plain error (best-effort call site treats this as "no data").
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("crt.sh: server error %d: %s", resp.StatusCode, string(body))
		}
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("crt.sh: API error %d: %s", resp.StatusCode, string(body))
		}

		data, er := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
		if er != nil {
			return er
		}
		var rows []ctShRow
		// crt.sh returns "" (empty body) for a domain with zero logged certs,
		// which is not valid JSON — treat that as "no entries," not an error.
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return nil
		}
		if er := json.Unmarshal(data, &rows); er != nil {
			return fmt.Errorf("crt.sh: parse: %w", er)
		}
		entries = ctRowsToEntries(rows, maxResults)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

type ctShRow struct {
	IssuerName string `json:"issuer_name"`
	NameValue  string `json:"name_value"`
	NotBefore  string `json:"not_before"`
	NotAfter   string `json:"not_after"`
	EntryTime  string `json:"entry_timestamp"`
}

// ctRowsToEntries expands each row's "\n"-separated name_value into individual
// CertEntry rows, deduplicated by (domain, notBefore) so a cert reused across
// rows doesn't produce a duplicate entry per SAN it shares with another cert.
// Ordered by LoggedAt descending (crt.sh's own order, most recent first) so
// callers see the freshest issuances without re-sorting.
func ctRowsToEntries(rows []ctShRow, maxResults int) []CertEntry {
	seen := make(map[string]bool)
	var out []CertEntry
	for _, row := range rows {
		for _, name := range strings.Split(row.NameValue, "\n") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			key := name + "|" + row.NotBefore
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, CertEntry{
				Domain:    name,
				Issuer:    row.IssuerName,
				NotBefore: row.NotBefore,
				NotAfter:  row.NotAfter,
				LoggedAt:  row.EntryTime,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LoggedAt > out[j].LoggedAt })
	if maxResults > 0 && len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}

var _ CTLogResolver = (*CrtShResolver)(nil)

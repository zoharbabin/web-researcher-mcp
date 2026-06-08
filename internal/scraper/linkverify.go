package scraper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Link verification (#157): confirm that a citation/source URL still resolves,
// and when it doesn't, find an Internet Archive (Wayback) snapshot so the
// citation stays usable. This makes the project's "verifiable citations"
// promise literal — a dead link is the failure users notice most.
//
// It lives in the scraper package because it reuses the SSRF-safe client (every
// outbound fetch of a user/result URL must be IP-validated). It is session-free
// by design (operates on plain LinkStatus values) so the tool layer and
// verify_citation can both use it without an import cycle.

// LinkStatus is the liveness result for one URL. Zero HTTPStatus means the
// request never completed (DNS/network failure / SSRF rejection).
type LinkStatus struct {
	URL         string
	HTTPStatus  int
	Live        bool   // resolved to a 2xx/3xx
	ArchivedURL string // Wayback snapshot, set only when Live is false and one exists
}

// LinkVerifier checks URL liveness with bounded concurrency and a short per-URL
// timeout, falling back to the Wayback availability API for dead links.
type LinkVerifier struct {
	client      *http.Client
	waybackBase string // overridable in tests; default Wayback availability API
	maxConc     int
	perURL      time.Duration
}

// LinkVerifierConfig configures a verifier. Zero values get safe defaults.
type LinkVerifierConfig struct {
	AllowPrivateIPs bool          // mirror the scrape SSRF posture
	MaxConcurrency  int           // default 8
	PerURLTimeout   time.Duration // default 8s
}

const waybackAvailabilityBase = "https://archive.org/wayback/available"

// NewLinkVerifier builds a verifier over an SSRF-safe client. Bounded by design:
// a short timeout and a concurrency cap so verification never dominates a
// request's latency.
func NewLinkVerifier(cfg LinkVerifierConfig) *LinkVerifier {
	maxConc := cfg.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 8
	}
	perURL := cfg.PerURLTimeout
	if perURL <= 0 {
		perURL = 8 * time.Second
	}
	// A dedicated client with a short timeout — independent of the scrape client's
	// longer budget. Still SSRF-safe (validates resolved IPs before connecting).
	c := NewSSRFSafeClient(cfg.AllowPrivateIPs)
	c.Timeout = perURL
	return &LinkVerifier{client: c, waybackBase: waybackAvailabilityBase, maxConc: maxConc, perURL: perURL}
}

// SetWaybackBase overrides the Wayback availability endpoint (testing).
func (v *LinkVerifier) SetWaybackBase(base string) { v.waybackBase = base }

// VerifyAll checks every URL concurrently (bounded) and returns the statuses in
// input order. Best-effort: a URL that errors is reported with Live=false and a
// zero/observed status; the call never fails as a whole.
func (v *LinkVerifier) VerifyAll(ctx context.Context, urls []string) []LinkStatus {
	out := make([]LinkStatus, len(urls))
	sem := make(chan struct{}, v.maxConc)
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				out[i] = LinkStatus{URL: u}
				return
			}
			out[i] = v.verifyOne(ctx, u)
		}(i, u)
	}
	wg.Wait()
	return out
}

// verifyOne checks a single URL: HEAD first (cheap), falling back to a ranged GET
// when HEAD is unsupported (405/501), then a Wayback lookup if still not live.
func (v *LinkVerifier) verifyOne(ctx context.Context, rawURL string) LinkStatus {
	st := LinkStatus{URL: rawURL}
	if rawURL == "" {
		return st
	}

	status := v.probe(ctx, http.MethodHead, rawURL)
	// Some servers reject HEAD — retry with GET before declaring the link dead.
	if status == 405 || status == 501 || status == 0 {
		if g := v.probe(ctx, http.MethodGet, rawURL); g != 0 {
			status = g
		}
	}
	st.HTTPStatus = status
	st.Live = status >= 200 && status < 400

	if !st.Live {
		if snap := v.wayback(ctx, rawURL); snap != "" {
			st.ArchivedURL = snap
		}
	}
	return st
}

// probe issues one request and returns the status code (0 on transport error).
func (v *LinkVerifier) probe(ctx context.Context, method, rawURL string) int {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "web-researcher-mcp link-verifier")
	resp, err := v.client.Do(req)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// wayback queries the Internet Archive availability API for a snapshot of url.
// Returns "" when none exists or the lookup fails (best-effort).
func (v *LinkVerifier) wayback(ctx context.Context, rawURL string) string {
	reqURL := v.waybackBase + "?url=" + url.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return ""
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ""
	}
	var wr waybackResponse
	if json.Unmarshal(data, &wr) != nil {
		return ""
	}
	snap := wr.ArchivedSnapshots.Closest
	if snap.Available && snap.URL != "" {
		return snap.URL
	}
	return ""
}

type waybackResponse struct {
	ArchivedSnapshots struct {
		Closest struct {
			Available bool   `json:"available"`
			URL       string `json:"url"`
			Status    string `json:"status"`
		} `json:"closest"`
	} `json:"archived_snapshots"`
}

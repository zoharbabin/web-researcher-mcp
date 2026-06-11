package scraper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	Live        bool   // resolved to 2xx/3xx, or a bot-wall (403/429/503) — the resource EXISTS
	Blocked     bool   // true when the URL exists but refused the verifier (403/429/503)
	ArchivedURL string // Wayback snapshot, set only when Live is false and one exists
}

// LinkVerifier checks URL liveness with bounded concurrency and a short per-URL
// timeout, falling back to the Wayback availability API for dead links. It can
// also CREATE a fresh Internet Archive snapshot via Save Page Now (#196).
type LinkVerifier struct {
	client        *http.Client
	archiveClient *http.Client // longer timeout for Save Page Now (slow); SSRF-safe
	waybackBase   string       // overridable in tests; default Wayback availability API
	saveBase      string       // overridable in tests; default Save Page Now endpoint
	iaAccessKey   string       // optional IA S3 access key (raises SPN reliability)
	iaSecretKey   string       // optional IA S3 secret key
	maxConc       int
	perURL        time.Duration
}

// LinkVerifierConfig configures a verifier. Zero values get safe defaults.
type LinkVerifierConfig struct {
	AllowPrivateIPs bool          // mirror the scrape SSRF posture
	MaxConcurrency  int           // default 8
	PerURLTimeout   time.Duration // default 8s
	// IAAccessKey/IASecretKey are optional Internet Archive S3-style credentials.
	// When both are set, Save Page Now requests are authenticated (higher rate /
	// reliability); keyless SPN still works without them. Never logged.
	IAAccessKey string
	IASecretKey string
}

const waybackAvailabilityBase = "https://archive.org/wayback/available"
const savePageNowBase = "https://web.archive.org/save/"

// archiveBudget is the total wall-clock budget for one archive_source call,
// covering all SPN attempts plus the Wayback fallback. SPN can take many seconds
// per attempt, so the retry loop times itself against this ceiling rather than
// calling time.Sleep (which would block the budget).
const archiveBudget = 25 * time.Second

// spnRetryBackoffs is the sequence of waits between SPN poll attempts. Three
// attempts within the 25 s budget: initial try, then 3 s, then 7 s — leaving
// ~12 s for the final attempt and the Wayback fallback.
var spnRetryBackoffs = []time.Duration{3 * time.Second, 7 * time.Second}

// ArchiveResult is the outcome of a Save Page Now request. Best-effort: an empty
// SnapshotURL means no snapshot was confirmed (SPN slow/declined/throttled and no
// existing snapshot found). Evidence, never a verdict.
type ArchiveResult struct {
	RequestedURL string
	SnapshotURL  string // https://web.archive.org/web/<ts>/<url> when known
	Timestamp    string // RFC3339; when THIS call confirmed a fresh snapshot
	HTTPStatus   int    // SPN endpoint status (0 = transport error / SSRF reject / timeout)
	Captured     bool   // true only when THIS call produced a fresh snapshot
	// PollURL is the canonical Wayback URL pattern to check manually when SPN
	// did not confirm a snapshot within the call budget. Non-empty only when
	// SnapshotURL is empty (i.e. status=pending). Format:
	// https://web.archive.org/web/*/https://example.com/page
	PollURL string
}

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
	// A separate, longer-budget SSRF-safe client for Save Page Now (it is slow).
	ac := NewSSRFSafeClient(cfg.AllowPrivateIPs)
	ac.Timeout = archiveBudget
	return &LinkVerifier{
		client:        c,
		archiveClient: ac,
		waybackBase:   waybackAvailabilityBase,
		saveBase:      savePageNowBase,
		iaAccessKey:   cfg.IAAccessKey,
		iaSecretKey:   cfg.IASecretKey,
		maxConc:       maxConc,
		perURL:        perURL,
	}
}

// SetWaybackBase overrides the Wayback availability endpoint (testing).
func (v *LinkVerifier) SetWaybackBase(base string) { v.waybackBase = base }

// SetSaveBase overrides the Save Page Now endpoint (testing).
func (v *LinkVerifier) SetSaveBase(base string) { v.saveBase = base }

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
	// 403/429/503 mean the resource EXISTS but refuses robots — treat as live/blocked,
	// not dead. A bot-wall is not a missing page; wayback lookup would only mislead.
	st.Blocked = status == 403 || status == 429 || status == 503
	st.Live = (status >= 200 && status < 400) || st.Blocked

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

// snapshotPrefix is the canonical Wayback snapshot URL prefix.
const snapshotPrefix = "https://web.archive.org/web/"

// Archive requests a fresh Internet Archive snapshot of rawURL via Save Page Now
// (#196). Best-effort and honest: on success it reports the new snapshot URL with
// Captured=true; on any failure/timeout/throttle it falls back to the most recent
// EXISTING snapshot (Captured=false), and returns an empty SnapshotURL only when
// neither is available. It never returns an error — the caller maps the result to
// a status. The outbound connection is to the fixed web.archive.org host through
// the SSRF-safe client (every redirect hop IP-revalidated); the user URL is the
// path suffix, not a separately-fetched target.
func (v *LinkVerifier) Archive(ctx context.Context, rawURL string) ArchiveResult {
	res := ArchiveResult{RequestedURL: rawURL}
	if rawURL == "" {
		return res
	}

	// Total budget for all SPN attempts + the Wayback fallback. The retry loop
	// checks the deadline before each backoff so we never overshoot.
	ctx, cancel := context.WithTimeout(ctx, archiveBudget)
	defer cancel()

	deadline, _ := ctx.Deadline()

	// Poll loop: try SPN up to 1+len(spnRetryBackoffs) times. Each attempt is
	// independent (GET to saveBase+rawURL). On the first confirmed snapshot we
	// return immediately; on a non-confirmation we wait the next backoff duration
	// (bounded by the remaining budget) before re-trying. The retry matters most
	// for never-archived URLs: SPN accepts the job on the first call but the
	// response only carries the snapshot URL after ingestion completes (typically
	// a few seconds later).
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.saveBase+rawURL, nil)
		if err != nil {
			break
		}
		req.Header.Set("User-Agent", "web-researcher-mcp link-verifier")
		// Authenticated SPN when both IA keys are configured (never logged).
		if v.iaAccessKey != "" && v.iaSecretKey != "" {
			req.Header.Set("Authorization", "LOW "+v.iaAccessKey+":"+v.iaSecretKey)
		}
		if resp, derr := v.archiveClient.Do(req); derr == nil {
			res.HTTPStatus = resp.StatusCode
			// The SSRF-safe client auto-follows redirects, so the captured snapshot
			// URL is the FINAL request URL (validated to be a /web/ snapshot).
			final := resp.Request.URL.String()
			if !strings.HasPrefix(final, snapshotPrefix) {
				// No-redirect responses sometimes carry the snapshot in a header.
				if cl := resp.Header.Get("Content-Location"); cl != "" {
					if strings.HasPrefix(cl, "/web/") {
						cl = "https://web.archive.org" + cl
					}
					if strings.HasPrefix(cl, snapshotPrefix) {
						final = cl
					}
				}
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
			_ = resp.Body.Close()
			if strings.HasPrefix(final, snapshotPrefix) {
				res.SnapshotURL = final
				res.Captured = true
				res.Timestamp = time.Now().UTC().Format(time.RFC3339)
				return res
			}
		}

		// No snapshot confirmed yet — back off if budget allows and more retries remain.
		if attempt >= len(spnRetryBackoffs) {
			break
		}
		backoff := spnRetryBackoffs[attempt]
		remaining := time.Until(deadline)
		if remaining <= backoff+2*time.Second {
			// Not enough budget for another full attempt after the backoff.
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		if ctx.Err() != nil {
			break
		}
	}

	// SPN did not confirm a snapshot within budget — fall back to the most recent
	// EXISTING snapshot so the caller still gets something usable (Captured stays false).
	if snap := v.wayback(ctx, rawURL); snap != "" {
		res.SnapshotURL = snap
	} else {
		// No existing snapshot either — give the caller an actionable poll URL so
		// they can check back manually once SPN's in-flight ingestion completes.
		res.PollURL = "https://web.archive.org/web/*/" + rawURL
	}
	return res
}

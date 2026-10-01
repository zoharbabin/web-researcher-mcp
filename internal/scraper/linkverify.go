package scraper

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
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

// LinkOutcome classifies one link check. Only LinkOutcomeLive is evidence that
// the cited page exists; blocked, redirected_to_root and unreachable are
// "unverified" (not evidence either way) and only dead is evidence of absence.
type LinkOutcome string

const (
	LinkOutcomeLive             LinkOutcome = "live"               // final 2xx/3xx on the requested page
	LinkOutcomeDead             LinkOutcome = "dead"               // HTTP error (e.g. 404/410/5xx) or DNS failure
	LinkOutcomeBlocked          LinkOutcome = "blocked"            // 403/429/503: the server refused the verifier; existence unknown
	LinkOutcomeRedirectedToRoot LinkOutcome = "redirected_to_root" // deep URL ended at a site root/generic landing page
	LinkOutcomeUnreachable      LinkOutcome = "unreachable"        // no HTTP response (timeout/reset/refused/TLS/policy); may be flaky
)

// Transport-failure causes reported in LinkStatus.FailureCause when no HTTP
// response was received (HTTPStatus 0).
const (
	LinkFailureDNS               = "dns_failure"
	LinkFailureTimeout           = "timeout"
	LinkFailureConnectionReset   = "connection_reset"
	LinkFailureConnectionRefused = "connection_refused"
	LinkFailureTLS               = "tls_error"
	LinkFailureSSRFBlocked       = "ssrf_blocked"
	LinkFailureOther             = "other"
)

// LinkStatus is the liveness result for one URL. Zero HTTPStatus means the
// request never completed (see FailureCause).
type LinkStatus struct {
	URL         string
	HTTPStatus  int
	Outcome     LinkOutcome
	Live        bool   // true only for LinkOutcomeLive: resolved 2xx/3xx and did not end at a site root
	Blocked     bool   // true when the server refused the verifier (403/429/503); existence is unknown, not confirmed
	ArchivedURL string // Wayback snapshot, set only when the URL is neither live nor blocked and one exists
	// FinalURL is the URL after redirects ("" when no response was received).
	FinalURL string
	// RedirectedToRoot is true when a non-root URL ended at the host root or a
	// generic landing page (e.g. a removed article redirecting to the homepage).
	RedirectedToRoot bool
	// FailureCause names why no HTTP response arrived (Link Failure* consts); ""
	// whenever HTTPStatus != 0.
	FailureCause string
}

// Dead reports evidence of absence: an HTTP error response or a DNS failure.
// Blocked, redirected-to-root and flaky-transport outcomes are NOT dead.
func (s LinkStatus) Dead() bool { return s.Outcome == LinkOutcomeDead }

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
// when HEAD is unsupported (405/501) or failed at the transport level, then a
// Wayback lookup if the URL is neither live nor blocked.
func (v *LinkVerifier) verifyOne(ctx context.Context, rawURL string) LinkStatus {
	st := LinkStatus{URL: rawURL}
	if rawURL == "" {
		return st
	}

	p := v.probe(ctx, http.MethodHead, rawURL)
	// Some servers reject HEAD, retry with GET before declaring the link dead.
	if p.status == 405 || p.status == 501 || p.status == 0 {
		if g := v.probe(ctx, http.MethodGet, rawURL); g.status != 0 || p.status == 0 {
			p = g
		}
	}
	st.HTTPStatus = p.status
	st.FinalURL = p.finalURL
	st.FailureCause = p.failure

	switch {
	case p.status == 0:
		// A DNS "no such host" is evidence the host is gone; every other
		// transport failure (timeout, reset, refused, TLS, policy) may be flaky.
		if p.failure == LinkFailureDNS {
			st.Outcome = LinkOutcomeDead
		} else {
			st.Outcome = LinkOutcomeUnreachable
		}
	case p.status == 403 || p.status == 429 || p.status == 503:
		// The server refused the verifier. That is not evidence the page exists
		// (a WAF answers 403 for invented paths too) and not evidence it is gone.
		st.Outcome = LinkOutcomeBlocked
		st.Blocked = true
	case p.status >= 200 && p.status < 400:
		if redirectedToRoot(rawURL, p.finalURL) {
			st.Outcome = LinkOutcomeRedirectedToRoot
			st.RedirectedToRoot = true
		} else {
			st.Outcome = LinkOutcomeLive
			st.Live = true
		}
	default:
		st.Outcome = LinkOutcomeDead
	}

	if !st.Live && !st.Blocked {
		if snap := v.wayback(ctx, rawURL); snap != "" {
			st.ArchivedURL = snap
		}
	}
	return st
}

// probeResult is one request's outcome: an HTTP status plus the post-redirect
// URL, or (status 0) the classified transport failure.
type probeResult struct {
	status   int
	finalURL string
	failure  string
}

// probe issues one request.
func (v *LinkVerifier) probe(ctx context.Context, method, rawURL string) probeResult {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return probeResult{failure: LinkFailureOther}
	}
	req.Header.Set("User-Agent", "web-researcher-mcp link-verifier")
	resp, err := v.client.Do(req)
	if err != nil {
		return probeResult{failure: classifyLinkError(err)}
	}
	_ = resp.Body.Close()
	return probeResult{status: resp.StatusCode, finalURL: resp.Request.URL.String()}
}

// classifyLinkError maps a transport error to one of the LinkFailure* causes.
func classifyLinkError(err error) string {
	var dnsErr *net.DNSError
	var certErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var tlsRecErr tls.RecordHeaderError
	switch {
	case errors.Is(err, ErrSSRFBlocked):
		return LinkFailureSSRFBlocked
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return LinkFailureDNS
		}
		if dnsErr.IsTimeout {
			return LinkFailureTimeout
		}
		return LinkFailureOther
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return LinkFailureTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return LinkFailureConnectionRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return LinkFailureConnectionReset
	case errors.As(err, &certErr), errors.As(err, &hostErr), errors.As(err, &tlsRecErr):
		return LinkFailureTLS
	}
	return LinkFailureOther
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// genericLandingPaths are post-redirect paths that mean "the site's front door",
// not the cited page, in addition to the bare root.
var genericLandingPaths = map[string]bool{
	"": true, "/": true,
	"/index.html": true, "/index.htm": true, "/index.php": true,
	"/home": true, "/404": true, "/not-found": true, "/error": true,
}

// redirectedToRoot reports whether a non-root requested URL ended at a site
// root or generic landing page. A request that was already for a landing page
// is never flagged, and neither is a URL that did not move.
func redirectedToRoot(requested, final string) bool {
	if final == "" {
		return false
	}
	req, err1 := url.Parse(requested)
	fin, err2 := url.Parse(final)
	if err1 != nil || err2 != nil {
		return false
	}
	reqPath := strings.ToLower(strings.TrimRight(req.Path, "/"))
	finPath := strings.ToLower(strings.TrimRight(fin.Path, "/"))
	if genericLandingPaths[reqPath] || genericLandingPaths[reqPath+"/"] {
		return false
	}
	return genericLandingPaths[finPath] || genericLandingPaths[finPath+"/"]
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

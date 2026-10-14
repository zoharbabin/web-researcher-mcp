package tools

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/cookievault"
)

type siteLoginInput struct {
	URL            string `json:"url" jsonschema:"The login page URL (http or https) of the site you want scrape_page to read while signed in.,required"`
	UseLoginWindow bool   `json:"use_login_window,omitempty" jsonschema:"Opens a separate visible browser window where you sign in by hand, then close the window. The sign-in is saved in a private browser profile that scrape_page reuses. Use it for LinkedIn and other sites that reject copied cookies, if you do not use Chrome, Edge or Brave, or cannot turn on remote debugging. Always used over HTTP. Default false: read the existing signed-in session from your own browser (STDIO only)."`
	Replace        bool   `json:"replace,omitempty" jsonschema:"Set true to sign in again when a valid saved login for the site already exists (for example when scrape_page returns loginRequired=true). Default false: site_login returns already_signed_in with the expiry and saves nothing."`
}

type siteLogoutInput struct {
	Domain string `json:"domain" jsonschema:"The site to forget: a hostname (e.g. example.com) or a URL on it. Removes the saved login for that site's registrable domain.,required"`
}

// vaultSubject returns the caller's vault subject, or false when the request has
// no identified user. Identity comes from the request context only (#241).
func vaultSubject(ctx context.Context) (cookievault.Subject, bool) {
	s := cookievault.Subject{TenantID: auth.TenantIDFromContext(ctx), UserID: auth.UserIDFromContext(ctx)}
	if s.TenantID == "" || s.UserID == "" || s.UserID == "anonymous" {
		return s, false
	}
	return s, true
}

// vaultCookiesFor returns the caller's saved login cookies for rawURL's site, or
// nil. It returns nil, and never reads the vault, unless the vault is enabled, the
// caller is identified and the caller has consented to session_capture. A missing,
// expired or corrupt bundle also reads as nil, so the scrape degrades to anonymous.
func vaultCookiesFor(ctx context.Context, deps Dependencies, rawURL string) []cookievault.Cookie {
	if deps.CookieVault == nil {
		return nil
	}
	subj, ok := vaultSubject(ctx)
	if !ok {
		return nil
	}
	if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeSessionCapture) {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	b, found := deps.CookieVault.Get(ctx, subj, u.Hostname())
	if !found {
		return nil
	}
	return b.Cookies
}

// profileLeaseFor leases the caller's saved browser profile for rawURL's site.
// It returns (nil, nil) when there is none to use, under the same gates as
// vaultCookiesFor: store configured, identified caller, session_capture consent.
// ErrProfileBusy is returned so the caller can tell the agent to retry.
func profileLeaseFor(ctx context.Context, deps Dependencies, rawURL string) (*cookievault.ProfileLease, error) {
	sub, host, ok := profileTarget(ctx, deps, rawURL)
	if !ok {
		return nil, nil
	}
	lease, err := deps.ProfileStore.Acquire(ctx, sub, host)
	if errors.Is(err, cookievault.ErrNoProfile) {
		return nil, nil // none, expired or unreadable: scrape anonymously
	}
	if err != nil {
		return nil, err // busy, cancelled or a disk failure: do not hide it behind an anonymous page
	}
	return lease, nil
}

// hasSavedProfile reports whether profileLeaseFor would find a profile, without
// waiting for or unpacking it.
func hasSavedProfile(ctx context.Context, deps Dependencies, rawURL string) bool {
	sub, host, ok := profileTarget(ctx, deps, rawURL)
	return ok && deps.ProfileStore.Has(sub, host)
}

// profileTarget applies the guards shared by every profile lookup: store
// configured, identified caller, session_capture consent, parseable URL.
func profileTarget(ctx context.Context, deps Dependencies, rawURL string) (cookievault.Subject, string, bool) {
	if deps.ProfileStore == nil {
		return cookievault.Subject{}, "", false
	}
	subj, ok := vaultSubject(ctx)
	if !ok {
		return cookievault.Subject{}, "", false
	}
	if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeSessionCapture) {
		return cookievault.Subject{}, "", false
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return cookievault.Subject{}, "", false
	}
	return subj, u.Hostname(), true
}

// recordSiteSession counts one saved-login event in Prometheus.
func recordSiteSession(deps Dependencies, event, kind, status string) {
	if deps.Metrics != nil {
		deps.Metrics.RecordSiteSession(event, kind, status)
	}
}

// registerSiteLogin registers the WRITE tool that captures a login (#241). A
// human signs in inside a visible browser; only the resulting cookies are kept,
// encrypted, for scrape_page. Consent-gated on the session_capture purpose.
func registerSiteLogin(srv *mcp.Server, deps Dependencies) {
	addTool(srv, &mcp.Tool{
		Name: "site_login",
		Description: "Create or renew a saved login so scrape_page can read pages that need one. Call it when scrape_page returns loginRequired=true, or when the user asks to sign in or sign in again. If a valid login is already saved it returns already_signed_in with the expiry and saves nothing; add replace=true to sign in again. To read a signed-in page (LinkedIn, etc.), call scrape_page on the page URL first: a login saved earlier, even in a past session, is applied automatically. " +
			"Two ways. Default (STDIO only), best for most sites such as Facebook, X, Instagram and YouTube: reads the site's cookies from your own running, already signed-in Chrome, Edge or Brave. Turn on remote debugging once (chrome://inspect/#remote-debugging), call this tool, and click Allow. It reads only that one site's cookies, disconnects, and never closes your browser. " +
			"With use_login_window=true (always used over HTTP, where the window opens on the machine running the server): a separate visible browser opens on the URL; you sign in by hand (including 2FA or CAPTCHA) and close the window. The sign-in is saved as an encrypted private browser profile that scrape_page reopens headless. Use this for LinkedIn and other sites that reject copied cookies. " +
			"If the browser is not reachable or you are not signed in there, the result is a status (browser_unavailable or not_signed_in) with the steps to fix it; no window is opened. If the site did not keep the sign-in (for example you did not tick 'remember me'), the result is profile_not_saved and nothing is stored. " +
			"The result's kind says what was saved (cookies or profile) and source says which path was used (browser or login_window). A new login replaces an older one for the same site. Only cookies and the browser profile are saved, never your password. Everything is stored encrypted, scoped to that site's registrable domain, " +
			"used only for that site by scrape_page, expires on a short default TTL, and can be removed any time with site_logout or the data-subject erasure endpoint. " +
			"Blocks until the cookies are read, you close the login window, or the capture timeout passes (then nothing is saved). " +
			"Opt-in and consent-gated: requires the 'session_capture' consent purpose, which is never granted by default (the operator must opt in explicitly). " +
			"Use only on accounts and sites whose terms of service allow automated access on your behalf; you are responsible for that. Some sites, such as LinkedIn, forbid it.",
		Annotations:  writeAnnotations(false),
		OutputSchema: siteLoginOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input siteLoginInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		input.URL = strings.TrimSpace(input.URL)
		if input.URL == "" {
			return toolError("url is required"), nil, nil
		}
		subj, ok := vaultSubject(ctx)
		if !ok {
			auditToolDenial(ctx, deps, "site_login", time.Since(start), "unauthenticated")
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "site_login needs to know who you are. Over STDIO set STDIO_USER_ID; over HTTP sign in with OAuth."})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeSessionCapture) {
			auditToolDenial(ctx, deps, "site_login", time.Since(start), "no_consent")
			return structuredResult(mustJSON(map[string]any{"status": "no_consent", "reason": "no recorded consent for the 'session_capture' purpose; nothing is captured. In STDIO the operator grants it by setting COOKIE_VAULT_STDIO_CONSENT=true"})), nil, nil
		}

		if !input.Replace {
			if kind, domain, expires, found := savedLoginFor(ctx, deps, subj, input.URL); found {
				recordToolCall(deps, "site_login", time.Since(start), nil, "", false)
				auditToolCall(ctx, deps, "site_login", time.Since(start), nil, "")
				return structuredResult(mustJSON(map[string]any{
					"status": "already_signed_in", "kind": kind, "domain": domain, "expiresAt": expires,
					"reason": "a valid login for " + domain + " is already saved. Call scrape_page on the page to use it. If scrape_page returns loginRequired=true, call site_login again with replace=true.",
				})), nil, nil
			}
		}

		// Over HTTP there is no browser to attach to, so the window is the only path.
		if input.UseLoginWindow || !deps.CookieCapturer.CanAttach() {
			sum, err := deps.CookieCapturer.LoginWindow(ctx, subj, input.URL)
			if err != nil {
				return siteLoginFailure(ctx, deps, start, "profile", err), nil, nil
			}
			// One login per site: drop an older copied-cookie bundle so scrape_page
			// cannot mix the two.
			_, _ = deps.CookieVault.Delete(ctx, subj, sum.Domain)
			siteLoginDone(ctx, deps, start, "profile", sum.Domain)
			return structuredResult(mustJSON(map[string]any{
				"status": "ok", "kind": "profile", "domain": sum.Domain,
				"capturedAt": sum.CreatedAt, "expiresAt": sum.ExpiresAt, "source": string(cookievault.SourceLoginWindow),
			})), nil, nil
		}

		domain, cookies, err := deps.CookieCapturer.CaptureFromBrowser(ctx, input.URL)
		if err != nil {
			return siteLoginFailure(ctx, deps, start, "cookies", err), nil, nil
		}
		sum, err := deps.CookieVault.Put(ctx, subj, domain, cookies)
		if err != nil {
			if errors.Is(err, cookievault.ErrNoCookies) {
				return siteLoginFailure(ctx, deps, start, "cookies", cookievault.ErrNothingCaptured), nil, nil
			}
			recordToolCall(deps, "site_login", time.Since(start), err, "upstream_error", false)
			auditToolCall(ctx, deps, "site_login", time.Since(start), err, "upstream_error")
			recordSiteSession(deps, "login", "cookies", "failed")
			return toolError("could not save the login: " + err.Error()), nil, nil
		}
		if deps.ProfileStore != nil {
			_, _ = deps.ProfileStore.Delete(ctx, subj, domain)
		}
		siteLoginDone(ctx, deps, start, "cookies", sum.Domain)
		return structuredResult(mustJSON(map[string]any{
			"status": "ok", "kind": "cookies", "domain": sum.Domain, "cookieCount": sum.CookieCount,
			"capturedAt": sum.CapturedAt, "expiresAt": sum.ExpiresAt, "source": string(cookievault.SourceBrowser),
		})), nil, nil
	})
}

// savedLoginFor reports the caller's live saved login (browser profile first, as
// scrape_page prefers it, else cookies) for rawURL's registrable domain, with its
// kind and expiry. It reads only redacted summaries, never cookie or profile content.
func savedLoginFor(ctx context.Context, deps Dependencies, subj cookievault.Subject, rawURL string) (kind, domain, expiresAt string, found bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", "", "", false
	}
	domain = cookievault.RegistrableDomain(u.Hostname())
	if domain == "" {
		return "", "", "", false
	}
	if deps.ProfileStore != nil {
		for _, p := range deps.ProfileStore.List(ctx, subj) {
			if p.Domain == domain {
				return "profile", domain, p.ExpiresAt, true
			}
		}
	}
	if deps.CookieVault != nil {
		for _, c := range deps.CookieVault.List(ctx, subj) {
			if c.Domain == domain {
				return "cookies", domain, c.ExpiresAt, true
			}
		}
	}
	return "", "", "", false
}

// siteLoginDone records a saved login in metrics and the audit trail. The audit
// event carries the kind and the registrable domain, never cookie or profile content.
func siteLoginDone(ctx context.Context, deps Dependencies, start time.Time, kind, domain string) {
	recordToolCall(deps, "site_login", time.Since(start), nil, "", false)
	auditToolCallQuery(ctx, deps, "site_login", time.Since(start), nil, "", "", map[string]any{"kind": kind, "domain": domain})
	recordSiteSession(deps, "login", kind, "ok")
}

// siteLoginFailure maps a capture error to a tool result and records the call.
// Messages are fixed strings: no URL query, cookie or upstream detail is echoed.
func siteLoginFailure(ctx context.Context, deps Dependencies, start time.Time, kind string, err error) *mcp.CallToolResult {
	status, msg := "failed", "login capture failed"
	switch {
	case errors.Is(err, cookievault.ErrCaptureBusy):
		status, msg = "busy", "another login window is already open; close it and try again"
	case errors.Is(err, cookievault.ErrProfileBusy):
		status, msg = "busy", "a scrape or login is using this site's saved browser profile; try again in a minute"
	case errors.Is(err, cookievault.ErrCaptureTimeout):
		status, msg = "timeout", "the login window was not closed in time; nothing was saved"
	case errors.Is(err, cookievault.ErrInvalidLoginURL):
		status, msg = "invalid_url", "the login URL must be an http(s) URL on a site with a registrable domain, without credentials"
	case errors.Is(err, cookievault.ErrAttachUnavailable):
		status, msg = "browser_unavailable", "could not reach your browser. For most sites (Facebook, X, Instagram, YouTube): in Chrome, Edge or Brave open chrome://inspect/#remote-debugging, turn it on, keep the browser running, call site_login again and click Allow. For LinkedIn, or if you cannot turn that on: call site_login again with use_login_window=true to sign in in a separate window. If a login was saved earlier, scrape_page already uses it"
	case errors.Is(err, cookievault.ErrNotSignedIn):
		status, msg = "not_signed_in", "your browser has no cookies for that site. Sign in to the site in that browser, then call site_login again. To sign in in a separate window instead, call site_login with use_login_window=true"
	case errors.Is(err, cookievault.ErrNoSignInSeen):
		status, msg = "not_signed_in", "no sign-in was seen in the window (only the site's own tracking cookies); nothing was saved. Call site_login again and sign in before you close the window"
	case errors.Is(err, cookievault.ErrNothingCaptured):
		status, msg = "nothing_captured", "no cookies were captured for that site; nothing was saved. Sign in before you close the window"
	case errors.Is(err, cookievault.ErrProfileNotSaved):
		status, msg = "profile_not_saved", "the site did not keep your sign-in, so a later scrape would not be signed in; nothing was saved. Sign in again and tick 'remember me' or 'stay signed in' if the site offers it"
	case errors.Is(err, cookievault.ErrBrowserLaunch):
		status, msg = "browser_launch_failed", "could not start a browser. Install Chrome, Edge or Brave, or set CHROME_PATH. In a container the browser sandbox may need CHROME_NO_SANDBOX=true"
	case errors.Is(err, cookievault.ErrTooManyProfiles):
		status, msg = "too_many_profiles", "too many sites have a saved sign-in"+savedProfileDomains(ctx, deps)+"; call site_logout for one you no longer need"
	case errors.Is(err, cookievault.ErrProfileStoreFull):
		status, msg = "profile_store_full", "the server's storage for saved sign-ins is full; call site_logout for a site you no longer need, or ask the operator to raise COOKIE_VAULT_PROFILE_MAX_TOTAL_MB"
	case errors.Is(err, cookievault.ErrProfileTooLarge):
		status, msg = "profile_too_large", "the browser profile is too large to store; nothing was saved"
		var tooLarge *cookievault.TooLargeError
		if errors.As(err, &tooLarge) && tooLarge.Detail != "" {
			msg += " (largest: " + tooLarge.Detail + ")"
		}
	case errors.Is(err, cookievault.ErrProfilesUnavailable):
		status, msg = "profiles_unavailable", "saved browser profiles are not available on this server"
	case errors.Is(err, cookievault.ErrLoginURLRejected):
		status, msg = "blocked", "that host is not allowed (private or internal address)"
	}
	recordToolCall(deps, "site_login", time.Since(start), err, "capture_"+status, false)
	auditToolCallQuery(ctx, deps, "site_login", time.Since(start), errors.New(status), "capture_"+status, "", map[string]any{"kind": kind})
	recordSiteSession(deps, "login", kind, status)
	return structuredResult(mustJSON(map[string]any{"status": status, "reason": msg}))
}

// savedProfileDomains returns " (a.com, b.com)" for the caller's saved browser
// profiles, or "" when none can be listed. Domains only, no profile content.
func savedProfileDomains(ctx context.Context, deps Dependencies) string {
	subj, ok := vaultSubject(ctx)
	if !ok || deps.ProfileStore == nil {
		return ""
	}
	var names []string
	for _, p := range deps.ProfileStore.List(ctx, subj) {
		names = append(names, p.Domain)
	}
	if len(names) == 0 {
		return ""
	}
	return " (" + strings.Join(names, ", ") + ")"
}

// registerSiteLogout registers the WRITE tool that removes a saved login (#241).
// It needs an identified user but no consent: erasure is always allowed.
func registerSiteLogout(srv *mcp.Server, deps Dependencies) {
	addTool(srv, &mcp.Tool{
		Name:         "site_logout",
		Description:  "Forget the saved login for a site (saved earlier with site_login): deletes the stored cookies and the saved browser profile for that site's registrable domain. Idempotent: reports removed=false when nothing was saved. removedCookies and removedProfile say which were deleted. Scrapes made with a saved login are never cached, so there is no cached page content to clear. Needs no consent.",
		Annotations:  writeAnnotations(true),
		OutputSchema: siteLogoutOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input siteLogoutInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		host := siteHost(input.Domain)
		if host == "" {
			return toolError("domain is required (a hostname such as example.com, or a URL on it)"), nil, nil
		}
		subj, ok := vaultSubject(ctx)
		if !ok {
			auditToolDenial(ctx, deps, "site_logout", time.Since(start), "unauthenticated")
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "site_logout needs to know who you are. Over STDIO set STDIO_USER_ID; over HTTP sign in with OAuth."})), nil, nil
		}
		removedCookies, err := deps.CookieVault.Delete(ctx, subj, host)
		removedProfile := false
		if err == nil && deps.ProfileStore != nil {
			removedProfile, err = deps.ProfileStore.Delete(ctx, subj, host)
		}
		if err != nil {
			if errors.Is(err, cookievault.ErrInvalidDomain) {
				return toolError("that host has no registrable domain"), nil, nil
			}
			recordToolCall(deps, "site_logout", time.Since(start), err, "upstream_error", false)
			auditToolCall(ctx, deps, "site_logout", time.Since(start), err, "upstream_error")
			recordSiteSession(deps, "logout", "any", "failed")
			return upstreamErrorResponse("site_logout", err), nil, nil
		}
		domain := cookievault.RegistrableDomain(host)
		recordToolCall(deps, "site_logout", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "site_logout", time.Since(start), nil, "", "", map[string]any{
			"domain": domain, "removedCookies": removedCookies, "removedProfile": removedProfile,
		})
		recordSiteSession(deps, "logout", "any", "ok")
		return structuredResult(mustJSON(map[string]any{
			"status": "ok", "domain": domain, "removed": removedCookies || removedProfile,
			"removedCookies": removedCookies, "removedProfile": removedProfile,
		})), nil, nil
	})
}

// siteHost reduces a hostname or URL to its hostname ("" when unusable).
func siteHost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i] // scheme-less input such as "linkedin.com/feed"
	}
	return strings.TrimSuffix(strings.ToLower(s), ".")
}

package tools

import (
	"net/url"
	"regexp"

	"github.com/zoharbabin/web-researcher-mcp/internal/cookievault"
)

// loginWallMaxWords is the most words a page may have and still count as a
// login wall. Real login walls are a form and a footer.
const loginWallMaxWords = 150

var (
	loginPathRe  = regexp.MustCompile(`(?i)/(log-?in|sign-?in|sign-?up|auth|checkpoint)\b`)
	signInCueRe  = regexp.MustCompile(`(?i)\b(sign[ -]?in|log[ -]?in)\b`)
	passwordCue  = regexp.MustCompile(`(?i)\bpassword\b`)
	loginWallTop = 2000 // bytes of body inspected; a wall is thin, so this covers it
)

// looksLikeLoginWall reports whether a scraped page is probably a sign-in page
// instead of the content asked for. It is a heuristic: a thin page whose title
// names sign-in, or whose text asks for a password next to a sign-in prompt.
// A request for a login page itself is never flagged.
func looksLikeLoginWall(requestedURL, title, body string, words int) bool {
	if words >= loginWallMaxWords {
		return false
	}
	if u, err := url.Parse(requestedURL); err == nil && loginPathRe.MatchString(u.Path) {
		return false
	}
	if signInCueRe.MatchString(title) {
		return true
	}
	if len(body) > loginWallTop {
		body = body[:loginWallTop]
	}
	return passwordCue.MatchString(body) && signInCueRe.MatchString(body)
}

// loginHintFor is loginHintForKind for the cookie bundle (authed) or no login.
func loginHintFor(deps Dependencies, authed bool, rawURL string) string {
	return loginHintForKind(deps, rawAuthKind(authed), rawURL)
}

// savedLoginSite names the site a login is saved for: the registrable domain of
// rawURL, or rawURL itself when it has none.
func savedLoginSite(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		if d := cookievault.RegistrableDomain(u.Hostname()); d != "" {
			return d
		}
	}
	return rawURL
}

// siteLoginAvailable reports whether site_login is registered. It needs a vault
// and a capturer, so hints that point at it are omitted without them.
func siteLoginAvailable(deps Dependencies) bool {
	return deps.CookieVault != nil && deps.CookieCapturer != nil
}

// loginHintForKind returns the next step for an agent that hit a login wall, or
// "" when site_login is not available (it registers only with a vault and a
// capturer). authKind says which saved login was already used ("cookies",
// "profile") or "" for none, so the hint says expired rather than missing. A
// profile can only be renewed in a login window.
func loginHintForKind(deps Dependencies, authKind, rawURL string) string {
	if !siteLoginAvailable(deps) {
		return ""
	}
	site := savedLoginSite(rawURL)
	switch authKind {
	case "profile":
		return "The saved sign-in for " + site + " looks expired or was rejected. Call site_login with use_login_window=true, replace=true and the site's login URL to sign in again, then retry."
	case "cookies":
		return "The saved login for " + site + " looks expired or was rejected. Call site_login with replace=true and the site's login URL to sign in again, then retry. When it is still rejected, add use_login_window=true."
	}
	return "This page needs a login and none is saved for " + site + ". Call site_login with " + site + "'s login URL, then retry. The default reads your signed-in Chrome, Edge or Brave and suits Facebook, X, Instagram, YouTube and most untested sites. For LinkedIn, or when a site still asks for a login after the default, call site_login again with use_login_window=true and replace=true."
}

// paywallHintForKind returns the next step for an agent that got the free
// preview of a paywalled article, or "" when site_login is not available. When a
// saved login was already sent and still got the preview, it is the login-wall
// hint: the login looks expired. Otherwise it points at site_login for a reader
// who has a subscription.
func paywallHintForKind(deps Dependencies, authKind, rawURL string) string {
	if authKind != "" {
		return loginHintForKind(deps, authKind, rawURL)
	}
	if !siteLoginAvailable(deps) {
		return ""
	}
	site := savedLoginSite(rawURL)
	return "This looks like the free preview of a paywalled article, not the full text. If the user has a subscription to " + site + ", call site_login with " + site + "'s home page, then retry. The default reads their signed-in Chrome, Edge or Brave. Do not use use_login_window unless the default fails: some publishers block automated login windows."
}

package tools

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// articleSite serves one article page. words sets the body length and ld, when
// not empty, is embedded as a JSON-LD block. The returned URL uses host, which
// must be registered with useFakeDNS.
func articleSite(t *testing.T, host string, words int, ld string) string {
	t.Helper()
	body := strings.Repeat("Markets moved on Tuesday as investors weighed new data. ", words/9+1)
	script := ""
	if ld != "" {
		script = `<script type="application/ld+json">` + ld + `</script>`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<html><head><title>Markets rally on data</title>%s</head><body><article><h1>Markets rally on data</h1><p>%s</p></article></body></html>`, script, body)
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://" + host + ":" + port + "/articles/markets-rally"
}

const (
	ldPaywalled = `{"@type":"NewsArticle","isAccessibleForFree":false}`
	ldFree      = `{"@type":"NewsArticle","isAccessibleForFree":true}`
)

// TestScrapePaywallSignalGoldSet is the labeled eval for paywallSuspected.
// Any false positive fails: flagging a full read would send users to log in
// for nothing.
func TestScrapePaywallSignalGoldSet(t *testing.T) {
	e := newVaultEnv(t)
	useFakeDNS(t, "www.wsj.com", "www.example-news.com")
	bob := e.client(t, "default", "bob")
	cases := []struct {
		name  string
		host  string
		words int
		ld    string
		want  bool
	}{
		{"known publisher, teaser", "www.wsj.com", 40, "", true},
		{"known publisher, 155-word preview (live WSJ case)", "www.wsj.com", 155, "", true},
		{"known publisher, full article", "www.wsj.com", 800, "", false},
		{"known publisher, markup says free", "www.wsj.com", 40, ldFree, false},
		{"unknown site, markup says paywalled, teaser", "www.example-news.com", 40, ldPaywalled, true},
		{"unknown site, markup says paywalled, full text", "www.example-news.com", 800, ldPaywalled, false},
		{"unknown site, teaser, no markup", "www.example-news.com", 40, "", false},
		{"unknown site, markup says free, teaser", "www.example-news.com", 40, ldFree, false},
		{"unknown site, full article", "www.example-news.com", 800, "", false},
	}
	for _, c := range cases {
		out, text := callVaultTool(t, bob, "scrape_page", map[string]any{"url": articleSite(t, c.host, c.words, c.ld)})
		got := out["paywallSuspected"] == true
		if got != c.want {
			t.Errorf("%s: paywallSuspected=%v, want %v: %.300s", c.name, got, c.want, text)
		}
		if _, hinted := out["loginHint"]; hinted != c.want {
			t.Errorf("%s: loginHint present=%v, want %v", c.name, hinted, c.want)
		}
		if out["loginRequired"] == true {
			t.Errorf("%s: a preview must not be reported as a login wall", c.name)
		}
	}
}

func TestScrapePaywallHintWithoutVault(t *testing.T) {
	e := newVaultEnv(t)
	e.deps.CookieVault, e.deps.CookieCapturer = nil, nil
	useFakeDNS(t, "www.wsj.com")
	out, text := callVaultTool(t, e.client(t, "default", "bob"), "scrape_page", map[string]any{"url": articleSite(t, "www.wsj.com", 40, "")})
	if out["paywallSuspected"] != true {
		t.Fatalf("the signal must not depend on the vault: %s", text)
	}
	if _, ok := out["loginHint"]; ok {
		t.Fatalf("without site_login the hint must be absent: %s", text)
	}
}

// loginWallSite serves a short "Sign in" page on a paywalled publisher's host.
func loginWallSite(t *testing.T, host string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Sign In - Reader</title></head><body><article><p>Please sign in to read this story with your subscriber account today.</p></article></body></html>`))
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://" + host + ":" + port + "/articles/story"
}

// A login wall is reported once. With site_login it is loginRequired. Without
// it there is no hint to give, so the paywall signal still tells the agent the
// page needs a login.
func TestScrapeLoginWallAndPaywallAreNotDoubled(t *testing.T) {
	e := newVaultEnv(t)
	useFakeDNS(t, "www.wsj.com")
	bob := e.client(t, "default", "bob")
	out, text := callVaultTool(t, bob, "scrape_page", map[string]any{"url": loginWallSite(t, "www.wsj.com")})
	if out["loginRequired"] != true || out["paywallSuspected"] == true {
		t.Fatalf("with site_login: want loginRequired only: %s", text)
	}

	e.deps.CookieVault, e.deps.CookieCapturer = nil, nil
	out, text = callVaultTool(t, e.client(t, "default", "bob"), "scrape_page", map[string]any{"url": loginWallSite(t, "www.wsj.com")})
	if out["loginRequired"] != nil || out["paywallSuspected"] != true {
		t.Fatalf("without site_login: want paywallSuspected only: %s", text)
	}
}

func TestScrapePaywallWithSavedLoginSaysExpired(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	// vaulttest.com holds alice's saved login, so this read is signed in.
	url := articleSite(t, vaultTestHost, 40, ldPaywalled)
	out, text := callVaultTool(t, e.client(t, "default", "alice"), "scrape_page", map[string]any{"url": url})
	if out["authenticated"] != true || out["paywallSuspected"] != true {
		t.Fatalf("want authenticated + paywallSuspected: %s", text)
	}
	if h, _ := out["loginHint"].(string); !strings.Contains(h, "expired") {
		t.Fatalf("a preview after a signed-in read means the login expired: %q", h)
	}
}

func TestPaywallHintForKind(t *testing.T) {
	t.Parallel()
	if got := paywallHintForKind(Dependencies{}, "", "https://www.wsj.com/a"); got != "" {
		t.Fatalf("no vault: hint = %q, want empty", got)
	}
	e := newVaultEnv(t)
	preview := paywallHintForKind(e.deps, "", "https://www.wsj.com/a")
	for _, want := range []string{"free preview", "site_login", "wsj.com", "Do not use use_login_window"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview hint %q lacks %q", preview, want)
		}
	}
	if stale := paywallHintForKind(e.deps, "cookies", "https://www.wsj.com/a"); !strings.Contains(stale, "expired") {
		t.Errorf("stale hint %q must say expired", stale)
	}
}

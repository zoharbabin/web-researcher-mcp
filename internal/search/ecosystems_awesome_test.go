package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newEcosystemsTestProvider(t *testing.T, handler http.HandlerFunc) *EcosystemsAwesomeProvider {
	t.Helper()
	return newEcosystemsTestProviderWithAuth(t, "", "", handler)
}

// emptyGitHubSearchHandler stubs the tier-3 GitHub topic-search fallback with
// a no-results response, so tests that exercise the ecosyste.ms tiers only
// (tiers 1/2) aren't perturbed by tier 3 hitting the real GitHub API.
func emptyGitHubSearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(`{"total_count":0,"items":[]}`))
}

func newEcosystemsTestProviderWithAuth(t *testing.T, apiKey, email string, handler http.HandlerFunc) *EcosystemsAwesomeProvider {
	t.Helper()
	return newEcosystemsTestProviderWithGitHub(t, apiKey, email, "", handler, emptyGitHubSearchHandler)
}

func newEcosystemsTestProviderWithGitHub(t *testing.T, apiKey, email, githubToken string, handler, githubHandler http.HandlerFunc) *EcosystemsAwesomeProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	ghSrv := httptest.NewServer(githubHandler)
	t.Cleanup(ghSrv.Close)
	p := NewEcosystemsAwesomeProvider(apiKey, email, githubToken, Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	p.SetGitHubBaseURL(ghSrv.URL)
	return p
}

func TestEcosystemsAwesomeKeyless(t *testing.T) {
	if p := NewAwesomeListProviderByName("ecosystems", AwesomeListProviderConfig{}, Deps{}); p == nil {
		t.Error("ecosystems should construct without any key")
	}
	if p := NewAwesomeListProviderByName("unknown", AwesomeListProviderConfig{}, Deps{}); p != nil {
		t.Error("unknown awesome-list provider should be nil")
	}
}

func TestEcosystemsAwesomeSendsKeyWhenConfigured(t *testing.T) {
	var gotAuth string
	p := newEcosystemsTestProviderWithAuth(t, "secret-key", "", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`[]`))
	})
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-key")
	}
}

func TestEcosystemsAwesomeSendsMailtoWhenConfigured(t *testing.T) {
	var gotMailto string
	p := newEcosystemsTestProviderWithAuth(t, "", "you@example.com", func(w http.ResponseWriter, r *http.Request) {
		gotMailto = r.URL.Query().Get("mailto")
		w.Write([]byte(`[]`))
	})
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMailto != "you@example.com" {
		t.Errorf("mailto param = %q, want %q", gotMailto, "you@example.com")
	}
}

func TestEcosystemsAwesomeSendsDescriptiveUserAgent(t *testing.T) {
	// ecosyste.ms hard-blocks the literal string "Go-http-client/*" with a
	// 429 regardless of rate-limit tier or quota remaining (confirmed via
	// direct curl A/B testing); any other User-Agent, descriptive or not,
	// passes this specific gate.
	var gotUA string
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte(`[]`))
	})
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(gotUA, "web-researcher-mcp/") {
		t.Errorf("User-Agent = %q, want a descriptive UA (not Go's default)", gotUA)
	}
}

func TestEcosystemsAwesomeEmbedsEmailInUserAgentWhenConfigured(t *testing.T) {
	// ecosyste.ms's APISIX tier classifier detects an email in the mailto
	// param OR the User-Agent header. mailto is already sent (see
	// TestEcosystemsAwesomeSendsMailtoWhenConfigured) and is sufficient on
	// its own for polite-tier classification; mirroring it into the UA too
	// matches ecosyste.ms's own documented example verbatim ("User-Agent:
	// MyApp/1.0 (contact: user@example.com)") as a redundant second signal.
	var gotUA string
	p := newEcosystemsTestProviderWithAuth(t, "", "you@example.com", func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte(`[]`))
	})
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotUA, "you@example.com") {
		t.Errorf("User-Agent = %q, want it to contain the configured email", gotUA)
	}
}

func TestEcosystemsAwesomeOmitsMailtoWhenUnset(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("mailto") {
			t.Errorf("mailto should be omitted when email is unset, got %q", r.URL.Query().Get("mailto"))
		}
		w.Write([]byte(`[]`))
	})
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// osintListsResponse mirrors the verified live shape of
// GET /api/v1/lists?topic=osint for jivoi/awesome-osint.
const osintListsResponse = `[{
	"name": "awesome-osint",
	"url": "https://awesome.ecosyste.ms/lists/jivoi%2Fawesome-osint",
	"description": "A curated list of amazingly awesome OSINT",
	"projects_count": 1431,
	"last_synced_at": "2026-07-02T07:00:27.731Z",
	"repository": {
		"full_name": "jivoi/awesome-osint",
		"archived": false,
		"stargazers_count": 27176,
		"topics": ["osint", "awesome-list"]
	}
}]`

func TestEcosystemsAwesomeSearchByTopic(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/lists") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("topic") != "osint" {
			t.Errorf("topic not passed: %q", q.Get("topic"))
		}
		if q.Get("per_page") != "5" {
			t.Errorf("per_page not passed: %q", q.Get("per_page"))
		}
		w.Write([]byte(osintListsResponse))
	})

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "osint", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 list, got %d", len(res))
	}
	l := res[0]
	if l.Name != "awesome-osint" || l.FullName != "jivoi/awesome-osint" {
		t.Errorf("identification mapping wrong: %+v", l)
	}
	if l.Stars != 27176 || l.ProjectsCount != 1431 {
		t.Errorf("stars/projects mapping wrong: %+v", l)
	}
	if len(l.Topics) != 2 || l.Topics[0] != "osint" {
		t.Errorf("topics mapping wrong: %+v", l.Topics)
	}
	if l.LastSyncedAt != "2026-07-02T07:00:27.731Z" {
		t.Errorf("lastSyncedAt mapping wrong: %+v", l)
	}
	if l.Archived {
		t.Error("archived should be false")
	}
	if l.Source != "ecosystems" {
		t.Errorf("source should be ecosystems: %s", l.Source)
	}
}

func TestEcosystemsAwesomeQueryFallsBackToTopic(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("topic") != "go" {
			t.Errorf("query should feed the topic param when Topic is empty, got %q", r.URL.Query().Get("topic"))
		}
		w.Write([]byte(`[]`))
	})
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Query: "go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEcosystemsAwesomeMissingTopicAndQueryErrors(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not make an HTTP request when topic and query are both empty")
	})
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{}); err == nil {
		t.Error("expected an error when topic and query are both empty")
	}
}

func TestEcosystemsAwesomeMinStarsFilter(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"name":"big","url":"https://x/big","projects_count":10,"repository":{"full_name":"a/big","archived":false,"stargazers_count":5000}},
			{"name":"small","url":"https://x/small","projects_count":10,"repository":{"full_name":"a/small","archived":false,"stargazers_count":10}}
		]`))
	})
	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x", MinStars: 1000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Name != "big" {
		t.Errorf("min_stars filter wrong: %+v", res)
	}
}

func TestEcosystemsAwesomeMinProjectsFilter(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"name":"many","url":"https://x/many","projects_count":500,"repository":{"full_name":"a/many","archived":false,"stargazers_count":100}},
			{"name":"few","url":"https://x/few","projects_count":3,"repository":{"full_name":"a/few","archived":false,"stargazers_count":100}}
		]`))
	})
	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x", MinProjects: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Name != "many" {
		t.Errorf("min_projects filter wrong: %+v", res)
	}
}

func TestEcosystemsAwesomeArchivedExcluded(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"name":"live","url":"https://x/live","repository":{"full_name":"a/live","archived":false,"stargazers_count":100}},
			{"name":"dead","url":"https://x/dead","repository":{"full_name":"a/dead","archived":true,"stargazers_count":9999}}
		]`))
	})
	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Name != "live" {
		t.Errorf("archived list should be excluded: %+v", res)
	}
}

func TestEcosystemsAwesomeSortByProjectsAndUpdated(t *testing.T) {
	body := `[
		{"name":"a","url":"https://x/a","projects_count":10,"last_synced_at":"2020-01-01T00:00:00Z","repository":{"full_name":"o/a","archived":false,"stargazers_count":999}},
		{"name":"b","url":"https://x/b","projects_count":500,"last_synced_at":"2026-01-01T00:00:00Z","repository":{"full_name":"o/b","archived":false,"stargazers_count":1}}
	]`
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	})

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x", SortBy: "projects", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 || res[0].Name != "b" {
		t.Errorf("sort_by=projects wrong order: %+v", res)
	}

	res, err = p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x", SortBy: "updated", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 || res[0].Name != "b" {
		t.Errorf("sort_by=updated wrong order: %+v", res)
	}

	res, err = p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 || res[0].Name != "a" {
		t.Errorf("default sort_by=stars wrong order: %+v", res)
	}
}

func TestEcosystemsAwesomeNoMatchEmpty(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "zzzznomatch"})
	if err != nil {
		t.Fatalf("no-match should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("no-match should be empty, got %+v", res)
	}
}

func TestEcosystemsAwesome404IsEmpty(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	})
	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"})
	if err != nil {
		t.Errorf("404 should map to empty, not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("404 should be empty: %+v", res)
	}
}

func TestEcosystemsAwesomeBadRequestErrors(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	})
	_, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500 should surface as an error, got %v", err)
	}
}

func TestEcosystemsAwesomeRateLimited(t *testing.T) {
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	})
	_, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"})
	if err == nil {
		t.Error("429 should surface as an error")
	}
}

func TestEcosystemsAwesomeInterface(t *testing.T) {
	var _ AwesomeListProvider = (*EcosystemsAwesomeProvider)(nil)
}

// TestEcosystemsAwesomeGitHubTopicFallbackRecoversTaxonomyMiss verifies tier 3
// (issue #394): when ecosyste.ms's own taxonomy has no matching slug at all
// (tier 1 empty, and a single-word topic has nothing left for tier 2's
// per-word retry to split), the GitHub topic-search fallback recovers the
// result independently.
func TestEcosystemsAwesomeGitHubTopicFallbackRecoversTaxonomyMiss(t *testing.T) {
	var gotQuery string
	p := newEcosystemsTestProviderWithGitHub(t, "", "", "",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) },
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query().Get("q")
			w.Write([]byte(`{"total_count":1,"items":[
				{"full_name":"a/awesome-parenting","html_url":"https://github.com/a/awesome-parenting","description":"d","stargazers_count":42,"topics":["parenting","awesome"],"archived":false,"pushed_at":"2026-01-01T00:00:00Z"}
			]}`))
		},
	)

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "parenting", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "topic:awesome") || !strings.Contains(gotQuery, "topic:parenting") {
		t.Errorf("GitHub search query = %q, want topic:awesome + topic:parenting", gotQuery)
	}
	if len(res) != 1 || res[0].FullName != "a/awesome-parenting" || res[0].Source != "github" {
		t.Errorf("unexpected result: %+v", res)
	}
}

// TestEcosystemsAwesomeGitHubTopicFallbackSkippedWhenPriorTiersNonEmpty
// verifies the fallback is only tried when tiers 1/2 are both empty — a
// primary-tier hit must never trigger an extra GitHub API call.
func TestEcosystemsAwesomeGitHubTopicFallbackSkippedWhenPriorTiersNonEmpty(t *testing.T) {
	githubCalled := false
	p := newEcosystemsTestProviderWithGitHub(t, "", "", "",
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`[{"name":"awesome-osint","url":"https://x/osint","repository":{"full_name":"a/osint","archived":false,"stargazers_count":10}}]`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			githubCalled = true
			w.Write([]byte(`{"total_count":0,"items":[]}`))
		},
	)

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "osint", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if githubCalled {
		t.Error("GitHub fallback should not be called when a prior tier already has results")
	}
	if len(res) != 1 || res[0].Source != "ecosystems" {
		t.Errorf("unexpected result: %+v", res)
	}
}

// TestEcosystemsAwesomeGitHubTopicFallbackSkipsOnError verifies the fallback
// is best-effort: a GitHub API failure degrades to the same empty result the
// caller would have seen without tier 3, not an error.
func TestEcosystemsAwesomeGitHubTopicFallbackSkipsOnError(t *testing.T) {
	p := newEcosystemsTestProviderWithGitHub(t, "", "", "",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) },
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	)

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "zzzznomatch", NumResults: 5})
	if err != nil {
		t.Fatalf("GitHub fallback failure should degrade to empty, not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("want empty result, got %+v", res)
	}
}

// TestEcosystemsAwesomeGitHubTopicFallbackExcludesMinProjects verifies a
// GitHub-sourced result is dropped entirely (not returned unfiltered) when
// MinProjects is set, since GitHub's Search API carries no equivalent field.
func TestEcosystemsAwesomeGitHubTopicFallbackExcludesMinProjects(t *testing.T) {
	p := newEcosystemsTestProviderWithGitHub(t, "", "", "",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) },
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"total_count":1,"items":[
				{"full_name":"a/b","html_url":"https://github.com/a/b","stargazers_count":100,"archived":false}
			]}`))
		},
	)

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x", MinProjects: 10, NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("MinProjects set should exclude GitHub-sourced results entirely, got %+v", res)
	}
}

// TestEcosystemsAwesomeGitHubTopicFallbackExcludesArchived mirrors the
// primary-tier archived filter for GitHub-sourced results.
func TestEcosystemsAwesomeGitHubTopicFallbackExcludesArchived(t *testing.T) {
	p := newEcosystemsTestProviderWithGitHub(t, "", "", "",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) },
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"total_count":2,"items":[
				{"full_name":"a/live","html_url":"https://github.com/a/live","stargazers_count":10,"archived":false},
				{"full_name":"a/dead","html_url":"https://github.com/a/dead","stargazers_count":999,"archived":true}
			]}`))
		},
	)

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].FullName != "a/live" {
		t.Errorf("archived GitHub result should be excluded: %+v", res)
	}
}

// TestEcosystemsAwesomeGitHubTokenSentWhenConfigured verifies the optional
// GITHUB_TOKEN (#282/#394/#395) is forwarded to the fallback's Authorization
// header exactly like FRED_API_KEY/BRANDFETCH_API_KEY — present when set,
// absent when not, never logged.
func TestEcosystemsAwesomeGitHubTokenSentWhenConfigured(t *testing.T) {
	var gotAuth string
	p := newEcosystemsTestProviderWithGitHub(t, "", "", "gh-secret-token",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) },
		func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Write([]byte(`{"total_count":0,"items":[]}`))
		},
	)
	if _, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer gh-secret-token" {
		t.Errorf("Authorization = %q, want Bearer gh-secret-token", gotAuth)
	}
}

// TestMultiInstanceEcosystemsProviderIsolation proves rule 1 (issue #396):
// two EcosystemsAwesomeProvider instances constructed with different
// githubToken/apiKey/email values in the same process never leak state
// across instances — no shared mutable state, only per-instance fields.
func TestMultiInstanceEcosystemsProviderIsolation(t *testing.T) {
	var gotAuthA, gotAuthB string
	pa := newEcosystemsTestProviderWithGitHub(t, "key-a", "a@example.com", "gh-token-a",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) },
		func(w http.ResponseWriter, r *http.Request) {
			gotAuthA = r.Header.Get("Authorization")
			w.Write([]byte(`{"total_count":0,"items":[]}`))
		},
	)
	pb := newEcosystemsTestProviderWithGitHub(t, "key-b", "b@example.com", "gh-token-b",
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) },
		func(w http.ResponseWriter, r *http.Request) {
			gotAuthB = r.Header.Get("Authorization")
			w.Write([]byte(`{"total_count":0,"items":[]}`))
		},
	)

	if _, err := pa.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := pb.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuthA != "Bearer gh-token-a" {
		t.Errorf("instance A leaked/lost its own token: got %q", gotAuthA)
	}
	if gotAuthB != "Bearer gh-token-b" {
		t.Errorf("instance B leaked/lost its own token: got %q", gotAuthB)
	}
	if pa.githubToken == pb.githubToken || pa.apiKey == pb.apiKey || pa.email == pb.email {
		t.Error("two instances constructed with different config must not share field values")
	}
}

// TestEcosystemsAwesomeNormalizesSpacesAndCase verifies the topic
// normalization fix: ecosyste.ms topic matching is case-sensitive and
// space-vs-hyphen-sensitive (confirmed live: "Mental Health" and
// "mental health" both 404 while "mental-health" returns 5 results), so the
// provider must lowercase and hyphenate before ever hitting the wire.
func TestEcosystemsAwesomeNormalizesSpacesAndCase(t *testing.T) {
	var gotTopic string
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotTopic = r.URL.Query().Get("topic")
		w.Write([]byte(`[{"name":"awesome-mental-health","url":"https://x/mh","repository":{"full_name":"a/mh","archived":false,"stargazers_count":3561}}]`))
	})
	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "Mental Health", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTopic != "mental-health" {
		t.Errorf("topic should be normalized to %q, got %q", "mental-health", gotTopic)
	}
	if len(res) != 1 || res[0].Name != "awesome-mental-health" {
		t.Errorf("unexpected results: %+v", res)
	}
}

// TestEcosystemsAwesomeWordFallbackOnCompoundMiss verifies the retry logic:
// a compound topic that misses as one slug (e.g. "personal-finance" — no
// such slug exists upstream, confirmed live) retries each substantive word
// independently and merges hits, recovering results that the raw compound
// query would never find.
func TestEcosystemsAwesomeWordFallbackOnCompoundMiss(t *testing.T) {
	var topicsQueried []string
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		topic := r.URL.Query().Get("topic")
		topicsQueried = append(topicsQueried, topic)
		switch topic {
		case "personal-finance":
			w.Write([]byte(`[]`))
		case "personal":
			w.Write([]byte(`[{"name":"awesome","url":"https://x/personal","repository":{"full_name":"a/personal","archived":false,"stargazers_count":27}}]`))
		case "finance":
			w.Write([]byte(`[{"name":"awesome-quant","url":"https://x/quant","repository":{"full_name":"a/quant","archived":false,"stargazers_count":27000}}]`))
		default:
			t.Errorf("unexpected topic queried: %q", topic)
			w.Write([]byte(`[]`))
		}
	})

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "personal-finance", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(topicsQueried) != 3 || topicsQueried[0] != "personal-finance" {
		t.Errorf("expected compound query then per-word fallback, got %v", topicsQueried)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 merged results, got %d: %+v", len(res), res)
	}
	if res[0].Name != "awesome-quant" || res[0].Stars != 27000 {
		t.Errorf("results should be sorted by stars desc after merge, got %+v", res)
	}
}

// TestEcosystemsAwesomeWordFallbackSkipsShortWordsAndStopwords verifies the
// fallback doesn't burn API calls on words with no topical signal.
func TestEcosystemsAwesomeWordFallbackSkipsShortWordsAndStopwords(t *testing.T) {
	var topicsQueried []string
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		topicsQueried = append(topicsQueried, r.URL.Query().Get("topic"))
		w.Write([]byte(`[]`))
	})
	_, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "a of go", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// compound "a-of-go" then only "go" (>=3 chars, not a stopword) — "a" and
	// "of" are skipped.
	if len(topicsQueried) != 2 || topicsQueried[0] != "a-of-go" || topicsQueried[1] != "go" {
		t.Errorf("expected compound then only 'go' retried, got %v", topicsQueried)
	}
}

// TestEcosystemsAwesomeNoFallbackForSingleWordMiss verifies a genuine
// single-word miss (e.g. "parenting" — the real upstream slug is "parent", an
// unrecoverable stemming gap, confirmed live) makes exactly one call and
// stays empty rather than retrying itself.
func TestEcosystemsAwesomeNoFallbackForSingleWordMiss(t *testing.T) {
	calls := 0
	p := newEcosystemsTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`[]`))
	})
	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "parenting", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("single-word miss should not retry, got %d calls", calls)
	}
	if len(res) != 0 {
		t.Errorf("want empty result, got %+v", res)
	}
}

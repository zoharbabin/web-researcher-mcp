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
	return newEcosystemsTestProviderWithKey(t, "", handler)
}

func newEcosystemsTestProviderWithKey(t *testing.T, apiKey string, handler http.HandlerFunc) *EcosystemsAwesomeProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewEcosystemsAwesomeProvider(apiKey, Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
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
	p := newEcosystemsTestProviderWithKey(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
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

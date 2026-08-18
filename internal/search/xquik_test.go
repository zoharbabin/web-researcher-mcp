package search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newXQuikTestProvider(t *testing.T, handler http.HandlerFunc) *XQuikProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider := NewXQuikProvider("test-key", Deps{
		HTTPClient: server.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(server.URL)
	return provider
}

const xquikTestResponse = `{"tweets":[
	{"id":"123","text":"Current contract","author":{"username":"alice","name":"Alice","verified":true},"createdAt":"2026-08-18T10:00:00Z","likeCount":7,"retweetCount":5,"replyCount":3,"quoteCount":2,"viewCount":100,"bookmarkCount":1},
	{"tweetId":"456","text":"Legacy contract","author":{"username":"bob","displayName":"Bob"},"createdAt":"2026-08-18T11:00:00Z","likeCount":4,"retweetCount":2,"replyCount":1,"viewCount":50},
	{"id":"","text":"Missing ID","author":{"username":"skip"}},
	{"id":"789","text":"Missing username","author":{"username":""}}
]}`

func TestXQuikWebMapsRequestAndResponse(t *testing.T) {
	t.Parallel()
	var gotHeader, gotQueryType, gotQuery, gotLimit string
	provider := newXQuikTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-api-key")
		gotQueryType = r.URL.Query().Get("queryType")
		gotQuery = r.URL.Query().Get("q")
		gotLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(xquikTestResponse))
	})

	results, err := provider.Web(context.Background(), WebSearchParams{Query: "go & agents", NumResults: 4})
	if err != nil {
		t.Fatalf("Web() error = %v", err)
	}
	if gotHeader != "test-key" || gotQueryType != "Top" || gotQuery != "go & agents" || gotLimit != "4" {
		t.Errorf("request = header %q, queryType %q, q %q, limit %q", gotHeader, gotQueryType, gotQuery, gotLimit)
	}
	if len(results) != 2 {
		t.Fatalf("Web() returned %d results, want 2 valid entries", len(results))
	}
	if results[0].URL != "https://x.com/alice/status/123" || results[1].URL != "https://x.com/bob/status/456" {
		t.Errorf("unexpected URLs: %q, %q", results[0].URL, results[1].URL)
	}
	if results[0].DisplayLink != "x.com" || results[0].PublishedAt != "2026-08-18T10:00:00Z" {
		t.Errorf("unexpected result metadata: %+v", results[0])
	}
	if results[0].Engagement == nil || results[0].Engagement.LikeCount != 7 || results[0].Engagement.RepostCount != 5 || results[0].Engagement.ReplyCount != 3 || results[0].Engagement.ViewCount != 100 {
		t.Errorf("unexpected engagement mapping: %+v", results[0].Engagement)
	}
}

func TestXQuikNewsUsesLatestAndClampsLimit(t *testing.T) {
	t.Parallel()
	var gotQueryType, gotLimit string
	provider := newXQuikTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotQueryType = r.URL.Query().Get("queryType")
		gotLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(xquikTestResponse))
	})

	results, err := provider.News(context.Background(), NewsSearchParams{Query: "breaking", NumResults: 999})
	if err != nil {
		t.Fatalf("News() error = %v", err)
	}
	if gotQueryType != "Latest" || gotLimit != strconv.Itoa(xquikMaxResults) {
		t.Errorf("queryType = %q, limit = %q", gotQueryType, gotLimit)
	}
	if len(results) != 2 || results[0].Source != "@alice" || results[0].Snippet != "Current contract" {
		t.Errorf("unexpected news results: %+v", results)
	}
}

func TestXQuikDefaultLimit(t *testing.T) {
	t.Parallel()
	var gotLimit string
	provider := newXQuikTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`{"tweets":[]}`))
	})
	if _, err := provider.Web(context.Background(), WebSearchParams{Query: "x"}); err != nil {
		t.Fatalf("Web() error = %v", err)
	}
	if gotLimit != strconv.Itoa(xquikMaxResults) {
		t.Errorf("limit = %q, want %d", gotLimit, xquikMaxResults)
	}
}

func TestXQuikImagesReturnsNilWithoutRequest(t *testing.T) {
	t.Parallel()
	called := false
	provider := newXQuikTestProvider(t, func(http.ResponseWriter, *http.Request) { called = true })
	results, err := provider.Images(context.Background(), ImageSearchParams{Query: "cats"})
	if err != nil || results != nil {
		t.Errorf("Images() = %v, %v; want nil, nil", results, err)
	}
	if called {
		t.Error("Images() made an HTTP request")
	}
}

func TestXQuikRateLimit(t *testing.T) {
	t.Parallel()
	provider := newXQuikTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := provider.Web(context.Background(), WebSearchParams{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "rate limited") || !errors.Is(err, circuit.ErrRateLimit) {
		t.Errorf("429 error = %v, want wrapped circuit.ErrRateLimit", err)
	}
}

func TestXQuikErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		query   string
		status  int
		body    string
		wantErr string
	}{
		{name: "empty query", wantErr: "query is required"},
		{name: "upstream", query: "x", status: http.StatusPaymentRequired, wantErr: "HTTP 402"},
		{name: "invalid JSON", query: "x", status: http.StatusOK, body: "not-json", wantErr: "parse response"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider := newXQuikTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				_, _ = w.Write([]byte(test.body))
			})
			_, err := provider.Web(context.Background(), WebSearchParams{Query: test.query})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("Web() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestXQuikNameAndInterface(t *testing.T) {
	t.Parallel()
	provider := NewXQuikProvider("key", Deps{})
	if provider.Name() != "xquik" {
		t.Errorf("Name() = %q, want xquik", provider.Name())
	}
	var _ Provider = provider
}

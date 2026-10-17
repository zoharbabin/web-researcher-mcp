package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

const parallelSample = `{"results":[{"title":"Go documentation","url":"https://go.dev/doc/","publish_date":"2026-01-02","excerpts":["Go is an open source programming language.","Read the documentation."]},{"title":null,"url":"https://example.org","excerpts":[]}]}`

type parallelTestTransport struct {
	base http.RoundTripper
	url  string
	t    *testing.T
}

func (r parallelTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != parallelMCPURL || req.UserAgent() != parallelUserAgent || req.Header.Get("Authorization") != "" {
		r.t.Errorf("unexpected destination or headers: %s %v", req.URL, req.Header)
	}
	return (&rewriteTransport{baseURL: r.url, inner: r.base}).RoundTrip(req)
}

func parallelTestDeps(t *testing.T, call func(http.ResponseWriter, *http.Request, json.RawMessage) any) (Deps, *atomic.Int32) {
	t.Helper()
	requests := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch req.Method {
		case "server/discover":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			return
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		case "notifications/initialized", "notifications/cancelled":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			result = call(w, r, req.Params)
			if result == nil {
				return
			}
		default:
			t.Errorf("unexpected method %s", req.Method)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.Transport = parallelTestTransport{base: client.Transport, url: srv.URL, t: t}
	return newTestDeps(client), requests
}

func TestParallelFactoryAndWeb(t *testing.T) {
	t.Parallel()
	deps, _ := parallelTestDeps(t, func(w http.ResponseWriter, r *http.Request, args json.RawMessage) any {
		var call struct {
			Name      string `json:"name"`
			Arguments struct {
				Objective string   `json:"objective"`
				Queries   []string `json:"search_queries"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(args, &call); err != nil {
			t.Error(err)
			return nil
		}
		if call.Name != "web_search" || len(call.Arguments.Queries) != 1 || call.Arguments.Queries[0] != "Go documentation site:go.dev" || !strings.Contains(call.Arguments.Objective, "last week") {
			t.Errorf("unexpected call: %+v", call)
		}
		return map[string]any{"content": []any{}, "structuredContent": json.RawMessage(parallelSample)}
	})
	cfg := config.SearchConfig{Provider: "parallel"}
	for _, p := range []Provider{NewProvider(cfg, deps), NewProviderByName("parallel", cfg, deps), AvailableProviders(cfg, deps)["parallel"]} {
		if p == nil || p.Name() != "parallel" {
			t.Fatalf("factory did not select Parallel: %T", p)
		}
		results, err := p.Web(context.Background(), WebSearchParams{Query: "Go documentation", Site: "go.dev", TimeRange: "week", NumResults: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].DisplayLink != "go.dev" || results[0].PublishedAt != "2026-01-02T00:00:00Z" || !strings.Contains(results[0].Snippet, "Read the documentation.") {
			t.Fatalf("unexpected results: %+v", results)
		}
	}
	if got := NewProvider(config.SearchConfig{}, deps).Name(); got != "duckduckgo" {
		t.Fatalf("default changed to %s", got)
	}
	if got := NewProvider(config.SearchConfig{GoogleAPIKey: "key", GoogleCX: "cx"}, deps).Name(); got != "google" {
		t.Fatalf("configured Google changed to %s", got)
	}
}

func TestParallelResponseVariants(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body any
		want int
		err  bool
	}{
		{"text", map[string]any{"content": []any{map[string]any{"type": "text", "text": parallelSample}}}, 2, false},
		{"empty", map[string]any{"structuredContent": map[string]any{"results": []any{}}, "content": []any{}}, 0, false},
		{"missing", map[string]any{"structuredContent": map[string]any{}, "content": []any{}}, 0, true},
		{"malformed", map[string]any{"content": []any{map[string]any{"type": "text", "text": "not JSON"}}}, 0, true},
		{"tool error", map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "upstream failure"}}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _ := parallelTestDeps(t, func(http.ResponseWriter, *http.Request, json.RawMessage) any { return tc.body })
			results, err := NewProviderByName("parallel", config.SearchConfig{}, deps).Web(context.Background(), WebSearchParams{Query: "Go"})
			if (err != nil) != tc.err || len(results) != tc.want {
				t.Fatalf("got %v, %v", results, err)
			}
		})
	}
}

func TestParallelRateLimitAndCancellation(t *testing.T) {
	t.Parallel()
	t.Run("rate limit", func(t *testing.T) {
		deps, _ := parallelTestDeps(t, func(w http.ResponseWriter, r *http.Request, args json.RawMessage) any {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		})
		_, err := NewParallelProvider(deps).Web(context.Background(), WebSearchParams{Query: "Go"})
		if !errors.Is(err, circuit.ErrRateLimit) {
			t.Fatalf("expected typed rate limit, got %v", err)
		}
		var limited *circuit.RateLimitError
		if !errors.As(err, &limited) || limited.After != 120*time.Second {
			t.Fatalf("Retry-After was lost: %v", err)
		}
	})
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			deps, _ := parallelTestDeps(t, func(w http.ResponseWriter, r *http.Request, args json.RawMessage) any {
				close(started)
				<-r.Context().Done()
				return nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if mode == "cancel" {
				go func() { <-started; cancel() }()
			}
			_, err := NewParallelProvider(deps).Web(ctx, WebSearchParams{Query: "Go"})
			if err == nil || ctx.Err() == nil {
				t.Fatalf("expected context error, got %v", err)
			}
		})
	}
}

func TestParallelUnsupportedAndLimit(t *testing.T) {
	t.Parallel()
	deps, requests := parallelTestDeps(t, func(http.ResponseWriter, *http.Request, json.RawMessage) any { t.Error("unexpected call"); return nil })
	p := NewParallelProvider(deps)
	if r, err := p.Images(context.Background(), ImageSearchParams{}); r != nil || err != nil {
		t.Fatal(r, err)
	}
	if r, err := p.News(context.Background(), NewsSearchParams{}); r != nil || err != nil {
		t.Fatal(r, err)
	}
	if _, err := p.Web(context.Background(), WebSearchParams{}); err == nil {
		t.Fatal("empty query accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("unsupported capability made a request")
	}
	items := make([]any, 20)
	for i := range items {
		items[i] = map[string]any{"url": fmt.Sprintf("https://example.com/%d", i)}
	}
	result := &mcp.CallToolResult{StructuredContent: map[string]any{"results": items}}
	for count, want := range map[int]int{0: 5, 1: 1, 100: 10} {
		got, err := parallelResults(result, count)
		if err != nil || len(got) != want {
			t.Fatalf("count %d: got %d, %v", count, len(got), err)
		}
	}
}

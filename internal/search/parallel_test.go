package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

const parallelSample = `{"results":[{"title":"Go documentation","url":"https://go.dev/doc/","publish_date":"2026-01-02","excerpts":["Go is an open source programming language.","Read the documentation."]},{"title":null,"url":"https://example.org","excerpts":[]}]}`

type parallelTestTransport struct {
	base http.RoundTripper
	url  string
	auth string // expected Authorization header; "" means anonymous
	t    *testing.T
}

func (r parallelTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != parallelMCPURL || req.UserAgent() != parallelUserAgent || req.Header.Get("Authorization") != r.auth {
		r.t.Errorf("unexpected destination or headers: %s %v", req.URL, req.Header)
	}
	return (&rewriteTransport{baseURL: r.url, inner: r.base}).RoundTrip(req)
}

func parallelTestDeps(t *testing.T, call func(http.ResponseWriter, *http.Request, json.RawMessage) any) (Deps, *atomic.Int32) {
	return parallelTestDepsAuth(t, "", call)
}

func parallelTestDepsAuth(t *testing.T, auth string, call func(http.ResponseWriter, *http.Request, json.RawMessage) any) (Deps, *atomic.Int32) {
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
	client.Transport = parallelTestTransport{base: client.Transport, url: srv.URL, auth: auth, t: t}
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
	cfg := config.SearchConfig{Provider: "parallel", ParallelEnabled: true}
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
			results, err := NewProviderByName("parallel", config.SearchConfig{ParallelEnabled: true}, deps).Web(context.Background(), WebSearchParams{Query: "Go"})
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
		_, err := NewParallelProvider("", deps).Web(context.Background(), WebSearchParams{Query: "Go"})
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
			var once sync.Once
			deps, _ := parallelTestDeps(t, func(w http.ResponseWriter, r *http.Request, args json.RawMessage) any {
				once.Do(func() { close(started) })
				// Bounded so the test server can always shut down, even if the
				// client never drops the connection.
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
				return nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if mode == "cancel" {
				go func() { <-started; cancel() }()
			}
			_, err := NewParallelProvider("", deps).Web(ctx, WebSearchParams{Query: "Go"})
			if err == nil || ctx.Err() == nil {
				t.Fatalf("expected context error, got %v", err)
			}
		})
	}
}

// TestParallelRequiresOptIn covers the operator egress gate: without
// PARALLEL_ENABLED the provider must not be constructible by any route, so
// provider:"parallel" has nothing to resolve to.
func TestParallelRequiresOptIn(t *testing.T) {
	t.Parallel()
	deps, requests := parallelTestDeps(t, func(http.ResponseWriter, *http.Request, json.RawMessage) any {
		t.Error("disabled provider made a request")
		return nil
	})
	off := config.SearchConfig{Provider: "parallel"}
	if p := NewProviderByName("parallel", off, deps); p != nil {
		t.Fatalf("NewProviderByName built a disabled provider: %T", p)
	}
	if _, ok := AvailableProviders(off, deps)["parallel"]; ok {
		t.Fatal("AvailableProviders registered a disabled provider")
	}
	// SEARCH_PROVIDER=parallel is rejected at config load, but the factory
	// must still degrade to the zero-config default rather than opening the
	// new egress path.
	if got := NewProvider(off, deps).Name(); got != "duckduckgo" {
		t.Fatalf("disabled parallel resolved to %s", got)
	}
	if got := NewProvider(config.SearchConfig{Provider: "parallel", GoogleAPIKey: "key", GoogleCX: "cx"}, deps).Name(); got != "google" {
		t.Fatalf("disabled parallel resolved to %s", got)
	}
	if requests.Load() != 0 {
		t.Fatal("disabled provider reached the network")
	}
}

// TestParallelAPIKey checks the optional Bearer credential reaches every
// request, including the MCP initialize handshake.
func TestParallelAPIKey(t *testing.T) {
	t.Parallel()
	deps, _ := parallelTestDepsAuth(t, "Bearer secret-key", func(http.ResponseWriter, *http.Request, json.RawMessage) any {
		return map[string]any{"content": []any{}, "structuredContent": json.RawMessage(parallelSample)}
	})
	cfg := config.SearchConfig{ParallelEnabled: true, ParallelAPIKey: "secret-key"}
	if _, err := NewProviderByName("parallel", cfg, deps).Web(context.Background(), WebSearchParams{Query: "Go"}); err != nil {
		t.Fatal(err)
	}
}

// TestParallelSessionID checks the stable free-tier rate-limit identifier is
// sent and does not change between calls on one provider instance.
func TestParallelSessionID(t *testing.T) {
	t.Parallel()
	var seen []string
	var mu sync.Mutex
	deps, _ := parallelTestDeps(t, func(_ http.ResponseWriter, _ *http.Request, args json.RawMessage) any {
		var call struct {
			Arguments struct {
				SessionID string `json:"session_id"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(args, &call); err != nil {
			t.Error(err)
			return nil
		}
		mu.Lock()
		seen = append(seen, call.Arguments.SessionID)
		mu.Unlock()
		return map[string]any{"content": []any{}, "structuredContent": json.RawMessage(parallelSample)}
	})
	p := NewParallelProvider("", deps)
	for range 2 {
		if _, err := p.Web(context.Background(), WebSearchParams{Query: "Go"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != seen[1] || len(seen[0]) != 32 {
		t.Fatalf("session_id was not a stable 32-char value: %q", seen)
	}
	if other := NewParallelProvider("", deps).sessionID; other == seen[0] {
		t.Fatal("session_id is not per-provider random")
	}
}

func TestParallelRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"120", 120 * time.Second},
		{"  60 ", 60 * time.Second},
		{"0", 0},
		{"-30", 0},
		{"not-a-duration", 0},
		{"99999999", parallelMaxRetryAfter},
		{"9223372036854775807", parallelMaxRetryAfter}, // would overflow time.Duration
		{now.Add(2 * time.Minute).Format(http.TimeFormat), 2 * time.Minute},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{now.Add(72 * time.Hour).Format(http.TimeFormat), parallelMaxRetryAfter},
	} {
		if got := parallelRetryAfter(tc.header, now); got != tc.want {
			t.Errorf("parallelRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

// TestParallelBoundsUntrustedText checks that unbounded upstream text cannot
// flow through verbatim, and that neither cap can split a multi-byte rune.
func TestParallelBoundsUntrustedText(t *testing.T) {
	t.Parallel()
	excerpt := strings.Repeat("é", 4000)
	result := &mcp.CallToolResult{StructuredContent: map[string]any{
		"results": []any{map[string]any{"url": "https://example.com", "excerpts": []string{excerpt}}},
	}}
	got, err := parallelResults(result, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(got[0].Snippet); n != parallelMaxSnippetRunes+len("...") {
		t.Fatalf("snippet was not capped: %d runes", n)
	}
	if !utf8.ValidString(got[0].Snippet) {
		t.Fatal("snippet truncation split a rune")
	}

	deps, _ := parallelTestDeps(t, func(http.ResponseWriter, *http.Request, json.RawMessage) any {
		return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": strings.Repeat("é", 4000)}}}
	})
	_, err = NewParallelProvider("", deps).Web(context.Background(), WebSearchParams{Query: "Go"})
	if err == nil {
		t.Fatal("expected a tool error")
	}
	if !utf8.ValidString(err.Error()) {
		t.Fatal("error truncation split a rune")
	}
	if n := utf8.RuneCountInString(err.Error()); n > parallelMaxErrLen+100 {
		t.Fatalf("error message was not capped: %d runes", n)
	}
}

// TestParallelExcludedFromImplicitPriority covers allProviderNames(): with no
// list configured for an operation the router falls back to every registered
// provider, and Parallel must stay out of that implicit set for every
// operation while an explicit list still reaches it.
func TestParallelExcludedFromImplicitPriority(t *testing.T) {
	t.Parallel()
	providers := map[string]Provider{
		"parallel": NewParallelProvider("", newTestDeps(http.DefaultClient)),
		"legacy":   newChaosProvider("legacy", true),
	}
	r := NewRouter(providers, RouterConfig{})
	for _, op := range []Operation{OpWeb, OpImages, OpNews, OpPatents, OpAcademic} {
		got := r.priorityFor(op)
		if len(got) != 1 || got[0] != "legacy" {
			t.Errorf("implicit %s priority included an opt-in provider: %v", op, got)
		}
	}

	routing, err := ParseRoutingConfig(`{"web":"parallel,legacy","images":"parallel","news":"parallel","patents":"parallel","academic":"parallel"}`)
	if err != nil {
		t.Fatal(err)
	}
	explicit := NewRouter(providers, RouterConfig{Routing: routing})
	for _, op := range []Operation{OpWeb, OpImages, OpNews, OpPatents, OpAcademic} {
		if got := explicit.priorityFor(op); len(got) == 0 || got[0] != "parallel" {
			t.Errorf("explicit %s priority dropped the configured provider: %v", op, got)
		}
	}
}

func TestParallelUnsupportedAndLimit(t *testing.T) {
	t.Parallel()
	deps, requests := parallelTestDeps(t, func(http.ResponseWriter, *http.Request, json.RawMessage) any { t.Error("unexpected call"); return nil })
	p := NewParallelProvider("", deps)
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

func TestParallelResultsRejectsOversizedPayload(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", parallelMaxBytes+1)
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: big}}}
	if _, err := parallelResults(result, 5); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error, got %v", err)
	}
}

type parallelRoundTripFunc func(*http.Request) (*http.Response, error)

func (f parallelRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestParallelAPIKeyStaysOnParallelHost: the Bearer key goes only to the Parallel
// endpoint. A redirect hop to another host, or to plain http on the same host,
// must not carry it (http.Client sends every hop through the transport).
func TestParallelAPIKeyStaysOnParallelHost(t *testing.T) {
	t.Parallel()
	for _, location := range []string{
		"https://other.example/x",
		"http://search.parallel.ai/mcp",
		"https://search.parallel.ai.evil.example/mcp",
	} {
		t.Run(location, func(t *testing.T) {
			t.Parallel()
			seen := map[string]string{} // request URL -> Authorization header
			base := parallelRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				seen[r.URL.String()] = r.Header.Get("Authorization")
				resp := &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}, Request: r}
				if r.URL.String() == parallelMCPURL {
					resp.StatusCode = http.StatusFound
					resp.Header.Set("Location", location)
				}
				return resp, nil
			})
			client := &http.Client{Transport: parallelTransport{base: base, apiKey: "secret-key"}}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, parallelMCPURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if got := seen[parallelMCPURL]; got != "Bearer secret-key" {
				t.Errorf("Parallel endpoint Authorization = %q, want the key", got)
			}
			if got, ok := seen[location]; !ok || got != "" {
				t.Errorf("redirect target %s: seen=%v Authorization=%q, want no key", location, ok, got)
			}
		})
	}
}

func TestParallelBuildsCleanQuery(t *testing.T) {
	t.Parallel()
	var got struct {
		Objective string
		Queries   []string
	}
	deps, _ := parallelTestDeps(t, func(_ http.ResponseWriter, _ *http.Request, args json.RawMessage) any {
		var call struct {
			Arguments struct {
				Objective string   `json:"objective"`
				Queries   []string `json:"search_queries"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(args, &call); err != nil {
			t.Error(err)
			return nil
		}
		got.Objective, got.Queries = call.Arguments.Objective, call.Arguments.Queries
		return map[string]any{"content": []any{}, "structuredContent": json.RawMessage(parallelSample)}
	})
	p := NewParallelProvider("", deps)
	_, err := p.Web(context.Background(), WebSearchParams{
		Query: "  go \n modules ", ExactTerms: "a \"b\"\n é", ExcludeTerms: "-foo bar --baz -",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `go modules "a b é" -foo -bar -baz`
	if len(got.Queries) != 1 || got.Queries[0] != want || got.Objective != want {
		t.Fatalf("query = %q, objective = %q, want %q", got.Queries, got.Objective, want)
	}
	if _, err := p.Web(context.Background(), WebSearchParams{Query: "\x00\n "}); err == nil {
		t.Fatal("control-only query must be rejected")
	}
}

func TestParallelResultsDropsNonHTTPURLs(t *testing.T) {
	t.Parallel()
	payload := `{"results":[
		{"url":"javascript:alert(1)","title":"a"},
		{"url":"file:///etc/passwd","title":"b"},
		{"url":"","title":"c"},
		{"url":"https:///nohost","title":"d"},
		{"url":"https://example.com/ok","title":"e"}]}`
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: payload}}}
	results, err := parallelResults(result, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/ok" {
		t.Fatalf("expected only the https result, got %+v", results)
	}
}

func TestParallelLeavesSharedClientAlone(t *testing.T) {
	t.Parallel()
	deps, _ := parallelTestDepsAuth(t, "Bearer key", func(http.ResponseWriter, *http.Request, json.RawMessage) any {
		return map[string]any{"content": []any{}, "structuredContent": json.RawMessage(parallelSample)}
	})
	before := deps.HTTPClient.Transport
	if _, err := NewParallelProvider("key", deps).Web(context.Background(), WebSearchParams{Query: "Go"}); err != nil {
		t.Fatal(err)
	}
	if deps.HTTPClient.Transport != before {
		t.Fatal("Parallel replaced the shared client's transport")
	}
}

func TestParallelTransportBoundsResponseBody(t *testing.T) {
	t.Parallel()
	base := parallelRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", parallelMaxBytes+100))),
			Request:    r,
		}, nil
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, parallelMCPURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := parallelTransport{base: base}.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != parallelMaxBytes {
		t.Fatalf("body = %d bytes, want it capped at %d", len(body), parallelMaxBytes)
	}
}

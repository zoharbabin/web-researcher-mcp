package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type mockProvider struct{}

func (m *mockProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Test Result", URL: "https://example.com", Snippet: "A test snippet"},
	}, nil
}

func (m *mockProvider) Images(_ context.Context, params search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{
		{Title: "Test Image", Link: "https://example.com/img.png", DisplayLink: "example.com"},
	}, nil
}

func (m *mockProvider) News(_ context.Context, params search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{
		{Title: "Test News", URL: "https://news.example.com/story", Source: "Example News", Snippet: "News snippet"},
	}, nil
}

func (m *mockProvider) Name() string { return "mock" }

type mockProviderWithURL struct {
	url string
}

func (m *mockProviderWithURL) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Test Result", URL: m.url, Snippet: "A test snippet"},
	}, nil
}

func (m *mockProviderWithURL) Images(_ context.Context, params search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{}, nil
}

func (m *mockProviderWithURL) News(_ context.Context, params search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{}, nil
}

func (m *mockProviderWithURL) Name() string { return "mock-with-url" }

func newTestBreaker() *circuit.Breaker {
	return circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})
}

func setupTestDeps() Dependencies {
	return Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
}

func createTestServer(deps Dependencies) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	RegisterAll(srv, deps)
	return srv
}

func connectTestClient(ctx context.Context, t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	return session
}

func TestRegisterAllDoesNotPanic(t *testing.T) {
	deps := setupTestDeps()
	createTestServer(deps)
}

func TestWebSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "golang testing"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "golang testing" {
		t.Fatalf("expected query 'golang testing', got %v", output["query"])
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestWebSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if text != "query is required" {
		t.Fatalf("expected 'query is required', got %q", text)
	}
}

func TestWebSearchLongQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	longQuery := strings.Repeat("x", 501)
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": longQuery},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for query exceeding 500 chars")
	}
}

func TestImageSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "image_search",
		Arguments: map[string]any{"query": "cats"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestNewsSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "news_search",
		Arguments: map[string]any{"query": "technology"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestAcademicSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "academic_search",
		Arguments: map[string]any{"query": "machine learning"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["query"] != "machine learning" {
		t.Fatalf("expected query 'machine learning', got %v", output["query"])
	}
}

func TestSequentialSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	// Step 1: Create session
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "Initial search for topic X",
			"stepNumber":     float64(1),
			"nextStepNeeded": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	sessionID, ok := output["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatal("expected sessionId in response")
	}

	// Step 2: Continue session
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "Found relevant paper on topic X",
			"stepNumber":     float64(2),
			"nextStepNeeded": false,
			"sessionId":      sessionID,
		},
	})
	if err != nil {
		t.Fatalf("CallTool step 2 failed: %v", err)
	}

	text = res.Content[0].(*mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse step 2 output: %v", err)
	}
	if output["isComplete"] != true {
		t.Fatal("expected isComplete=true on final step")
	}
}

// TestSequentialSearchMissingSessionOnStep2 verifies that a step > 1 with no
// sessionId is rejected with guidance rather than silently forking a new,
// orphaned session (which would abandon the real research trail after a caller
// loses its sessionId mid-research).
func TestSequentialSearchMissingSessionOnStep2(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "Step two with no session id",
			"stepNumber":     float64(2),
			"nextStepNeeded": true,
			// sessionId deliberately omitted
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for step 2 without a sessionId")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "missing sessionId") {
		t.Errorf("expected missing-sessionId guidance, got: %s", text)
	}
}

func TestPatentSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/US20200012345A1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":       "neural network acceleration",
			"search_type": "prior_art",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "neural network acceleration" {
		t.Fatalf("expected query 'neural network acceleration', got %v", output["query"])
	}
	if output["searchType"] != "prior_art" {
		t.Fatalf("expected searchType 'prior_art', got %v", output["searchType"])
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestPatentSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "patent_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestPatentSearchWithFilters(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/US20200012345A1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":         "machine learning",
			"assignee":      "Google Inc",
			"patent_office": "US",
			"year_from":     float64(2020),
			"year_to":       float64(2024),
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterRejectsNonMatching(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/EP1234567B1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":         "machine learning",
			"patent_office": "US",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 0 {
		t.Fatalf("expected 0 results (EP patent filtered by US office), got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterAllowsAll(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/EP1234567B1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":         "machine learning",
			"patent_office": "all",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result with 'all' office, got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterNoOffice(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/WO2021123456A1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query": "machine learning",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result with no office filter, got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterMultipleOffices(t *testing.T) {
	offices := []struct {
		office string
		url    string
		expect float64
	}{
		{"US", "https://patents.google.com/patent/US10123456B2/en", 1},
		{"EP", "https://patents.google.com/patent/EP3456789A1/en", 1},
		{"WO", "https://patents.google.com/patent/WO2022000001A1/en", 1},
		{"JP", "https://patents.google.com/patent/JP6789012B2/en", 1},
		{"CN", "https://patents.google.com/patent/CN112345678A/en", 1},
		{"KR", "https://patents.google.com/patent/KR20200012345A/en", 1},
		{"US", "https://patents.google.com/patent/CN112345678A/en", 0},
		{"EP", "https://patents.google.com/patent/US10123456B2/en", 0},
	}

	for _, tt := range offices {
		t.Run(tt.office+"_"+tt.url, func(t *testing.T) {
			ctx := context.Background()
			deps := setupTestDeps()
			deps.Search = &mockProviderWithURL{url: tt.url}
			srv := createTestServer(deps)
			session := connectTestClient(ctx, t, srv)
			defer session.Close()

			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "patent_search",
				Arguments: map[string]any{
					"query":         "test",
					"patent_office": tt.office,
				},
			})
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}

			text := res.Content[0].(*mcp.TextContent).Text
			var output map[string]any
			if err := json.Unmarshal([]byte(text), &output); err != nil {
				t.Fatalf("failed to parse output: %v", err)
			}
			if output["resultCount"].(float64) != tt.expect {
				t.Fatalf("office=%s url=%s: expected %v results, got %v", tt.office, tt.url, tt.expect, output["resultCount"])
			}
		})
	}
}

func TestScrapePageTool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Test Page</title><meta property="og:title" content="Test Title"/></head>
<body><article>
<h1>Main Heading</h1>
<p>This is test content from the httptest server. It contains enough text to be extracted properly by the scraping pipeline and should pass the minimum content length threshold of 100 characters for successful extraction.</p>
<p>Additional paragraph with more relevant information for the scraper to pick up during testing.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["url"] != ts.URL {
		t.Fatalf("expected url %q, got %v", ts.URL, output["url"])
	}
	if output["contentType"] != "html" {
		t.Fatalf("expected contentType 'html', got %v", output["contentType"])
	}
	contentStr, _ := output["content"].(string)
	if !strings.Contains(contentStr, "Main Heading") {
		t.Fatal("expected content to contain 'Main Heading'")
	}
}

func TestScrapePageEmptyURL(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty url")
	}
}

func TestScrapePageHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestScrapePageRawMode(t *testing.T) {
	const rawBody = `<html><head><title>Raw</title></head><body><script>alert('xss')</script><style>.x{}</style><p>visible</p></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(rawBody))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL, "mode": "raw"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	// Raw mode must NOT sanitize: active <script>/<style> stay verbatim.
	contentStr, _ := output["content"].(string)
	if contentStr != rawBody {
		t.Fatalf("raw content should be byte-for-byte; got %q", contentStr)
	}
	if !strings.Contains(contentStr, "<script>") || !strings.Contains(contentStr, "<style>") {
		t.Fatal("raw mode must preserve <script>/<style> tags unsanitized")
	}
	// Real MIME from Content-Type header, not the normalized "html" classifier.
	if ct, _ := output["contentType"].(string); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected real MIME contentType, got %v", output["contentType"])
	}
	if raw, _ := output["raw"].(bool); !raw {
		t.Fatal("expected raw flag true")
	}
}

func TestScrapePageRawVsFullDistinctCache(t *testing.T) {
	const body = `<!DOCTYPE html><html><head><title>Cache Test</title></head><body><article>
<h1>Heading</h1><p>This is the main article body with sufficient length to be extracted by the cleaning pipeline so that full mode returns sanitized readable text rather than the raw markup.</p>
<p>A second paragraph adds more extractable prose for the content processor.</p></article></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(body))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	call := func(mode string) map[string]any {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "scrape_page",
			Arguments: map[string]any{"url": ts.URL, "mode": mode},
		})
		if err != nil {
			t.Fatalf("CallTool(%s) failed: %v", mode, err)
		}
		if res.IsError {
			t.Fatalf("CallTool(%s) error: %v", mode, res.Content)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatalf("parse(%s): %v", mode, err)
		}
		return out
	}

	full := call("full")
	raw := call("raw")

	// Distinct cache entries: full is sanitized (no <h1> markup), raw is verbatim.
	fullContent, _ := full["content"].(string)
	rawContent, _ := raw["content"].(string)
	if fullContent == rawContent {
		t.Fatal("raw and full must produce distinct content (separate cache keys)")
	}
	if strings.Contains(fullContent, "<h1>") {
		t.Fatal("full mode should be sanitized, not contain raw <h1> markup")
	}
	if !strings.Contains(rawContent, "<h1>") {
		t.Fatal("raw mode should contain verbatim <h1> markup")
	}
}

func TestSearchAndScrapeTool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Scraped Page</title></head>
<body><article>
<h1>Search Result Content</h1>
<p>This page was found by search and then scraped. It has enough content to pass the minimum threshold for content extraction and should be included in the combined output of search_and_scrape.</p>
<p>Second paragraph with additional detail about the topic being researched via the combined pipeline.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: ts.URL}
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test topic"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "test topic" {
		t.Fatalf("expected query 'test topic', got %v", output["query"])
	}

	combined, _ := output["combinedContent"].(string)
	if combined == "" {
		t.Fatal("expected non-empty combinedContent")
	}
	if !strings.Contains(combined, "Search Result Content") {
		t.Fatal("expected combinedContent to include scraped content")
	}
}

func TestSearchAndScrapeEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestWebSearchCaching(t *testing.T) {
	ctx := context.Background()
	metricsCollector := metrics.NewCollector()
	deps := Dependencies{
		Cache:    cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1}),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metricsCollector,
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	// First call: should not be from cache
	_, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "cache test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	stats := metricsCollector.GetToolStats()
	s := stats["web_search"]
	if s.CacheHits != 0 {
		t.Fatalf("expected 0 cache hits after first call, got %d", s.CacheHits)
	}

	// Second call with same query: should hit cache
	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "cache test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	stats = metricsCollector.GetToolStats()
	s = stats["web_search"]
	if s.CacheHits != 1 {
		t.Fatalf("expected 1 cache hit after second call, got %d", s.CacheHits)
	}
}

func TestImageSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "image_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestNewsSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "news_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestAcademicSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "academic_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestSequentialSearchEmptyStep(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "",
			"stepNumber":     float64(1),
			"nextStepNeeded": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty searchStep")
	}
}

func TestToolError(t *testing.T) {
	result := toolError("something went wrong")
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected *TextContent")
	}
	if tc.Text != "something went wrong" {
		t.Fatalf("expected error text, got %q", tc.Text)
	}
}

// capturingAuditor records every logged event and reports a configurable
// IncludeRequestBody value, for asserting the query-gating + request-id wiring.
type capturingAuditor struct {
	mu             sync.Mutex
	events         []audit.AuditEvent
	includeReqBody bool
}

func (c *capturingAuditor) Log(ev audit.AuditEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}
func (c *capturingAuditor) IncludeRequestBody() bool { return c.includeReqBody }
func (c *capturingAuditor) Close()                   {}
func (c *capturingAuditor) last() audit.AuditEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.events[len(c.events)-1]
}

func TestAuditQueryGatingOmitsRawQuery(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: false}
	deps := setupTestDeps()
	deps.Auditor = cap

	auditToolCallQuery(context.Background(), deps, "web_search", time.Millisecond, nil, "", "secret research topic", nil)

	ev := cap.last()
	if _, ok := ev.Metadata["query"]; ok {
		t.Error("raw query must NOT be present when IncludeRequestBody=false")
	}
	ql, ok := ev.Metadata["query_length"]
	if !ok {
		t.Fatal("expected query_length in metadata when IncludeRequestBody=false")
	}
	if ql.(int) != len("secret research topic") {
		t.Errorf("query_length = %v, want %d", ql, len("secret research topic"))
	}
}

func TestAuditQueryGatingIncludesRawQuery(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: true}
	deps := setupTestDeps()
	deps.Auditor = cap

	auditToolCallQuery(context.Background(), deps, "web_search", time.Millisecond, nil, "", "open research topic", nil)

	ev := cap.last()
	q, ok := ev.Metadata["query"]
	if !ok {
		t.Fatal("expected raw query in metadata when IncludeRequestBody=true")
	}
	if q.(string) != "open research topic" {
		t.Errorf("query = %v, want 'open research topic'", q)
	}
	if _, ok := ev.Metadata["query_length"]; ok {
		t.Error("query_length must not be set when raw query is included")
	}
}

func TestAuditMasksQueryAndError(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: true}
	deps := setupTestDeps()
	deps.Auditor = cap

	// Synthetic key-shaped values assembled at runtime so no contiguous
	// credential literal lands in source (keeps secret scanners quiet); the
	// google-key and token= query-param rules still fire on the joined string.
	googleKey := "AIza" + "0123456789abcdefghijklmnopqrstuv012"
	tokenVal := "val-" + "0123456789abcdef"
	secretQuery := "lookup key=" + googleKey
	err := errorString("provider failed: token=" + tokenVal)
	auditToolCallQuery(context.Background(), deps, "web_search", time.Millisecond, err, "upstream_error", secretQuery, nil)

	ev := cap.last()
	if q := ev.Metadata["query"].(string); strings.Contains(q, googleKey) {
		t.Errorf("query metadata leaked a secret: %q", q)
	}
	if e := ev.Metadata["error"].(string); strings.Contains(e, tokenVal) {
		t.Errorf("error metadata leaked a secret: %q", e)
	}
}

func TestAuditSetsRequestIDFromContext(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{}
	deps := setupTestDeps()
	deps.Auditor = cap

	ctx := context.WithValue(context.Background(), auth.ContextKeyRequestID, "req-correlate-123")
	auditToolCall(ctx, deps, "scrape_page", time.Millisecond, nil, "")

	if got := cap.last().RequestID; got != "req-correlate-123" {
		t.Errorf("RequestID = %q, want correlated value from context", got)
	}
}

func TestAuditToolCallNoQueryHasNoQueryMeta(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: false}
	deps := setupTestDeps()
	deps.Auditor = cap

	auditToolCall(context.Background(), deps, "image_search", time.Millisecond, nil, "")

	ev := cap.last()
	if _, ok := ev.Metadata["query"]; ok {
		t.Error("no query metadata expected for query-less call")
	}
	if _, ok := ev.Metadata["query_length"]; ok {
		t.Error("no query_length metadata expected for query-less call")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestToolsWorkWithRouter(t *testing.T) {
	// Verify that tools work correctly when the Provider is a Router
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"organic_results": []map[string]any{
				{"position": 1, "title": "Router Result", "link": "https://router.example.com", "snippet": "Via router", "displayed_link": "router.example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := config.SearchConfig{
		SearchAPIKey: "test-key",
	}
	searchDeps := search.Deps{
		HTTPClient: ts.Client(),
		Breaker:    nil, // AvailableProviders creates its own breakers via the router
	}

	// Manually create a searchapi provider pointed at our test server
	provider := search.NewSearchAPIProvider(cfg.SearchAPIKey, search.Deps{
		HTTPClient: ts.Client(),
		Breaker:    newTestBreaker(),
	})
	provider.SetBaseURL(ts.URL)
	_ = searchDeps // used only for the pattern illustration

	providers := map[string]search.Provider{
		"searchapi": provider,
	}
	router := search.NewRouter(providers, search.RouterConfig{
		Routing: search.RoutingConfig{Default: []string{"searchapi"}},
	})

	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   router,
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}

	ctx := context.Background()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "test via router"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

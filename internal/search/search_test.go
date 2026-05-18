package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

// =============================================================================
// Provider Factory Tests
// =============================================================================

func newTestDeps(client *http.Client) Deps {
	return Deps{
		HTTPClient: client,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	}
}

func TestNewProvider_Google(t *testing.T) {
	cfg := config.SearchConfig{Provider: "google", GoogleAPIKey: "key", GoogleCX: "cx"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "google" {
		t.Errorf("expected provider name 'google', got %q", p.Name())
	}
}

func TestNewProvider_Brave(t *testing.T) {
	cfg := config.SearchConfig{Provider: "brave", BraveAPIKey: "key"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "brave" {
		t.Errorf("expected provider name 'brave', got %q", p.Name())
	}
}

func TestNewProvider_Serper(t *testing.T) {
	cfg := config.SearchConfig{Provider: "serper", SerperAPIKey: "key"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "serper" {
		t.Errorf("expected provider name 'serper', got %q", p.Name())
	}
}

func TestNewProvider_SearXNG(t *testing.T) {
	cfg := config.SearchConfig{Provider: "searxng", SearXNGURL: "http://localhost:8080"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "searxng" {
		t.Errorf("expected provider name 'searxng', got %q", p.Name())
	}
}

func TestNewProvider_DefaultIsGoogle(t *testing.T) {
	cfg := config.SearchConfig{Provider: "unknown", GoogleAPIKey: "key", GoogleCX: "cx"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "google" {
		t.Errorf("expected default provider 'google', got %q", p.Name())
	}
}

// =============================================================================
// Google Provider Tests
// =============================================================================

func TestGoogleProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameters
		q := r.URL.Query()
		if q.Get("key") != "test-key" {
			t.Errorf("expected key 'test-key', got %q", q.Get("key"))
		}
		if q.Get("cx") != "test-cx" {
			t.Errorf("expected cx 'test-cx', got %q", q.Get("cx"))
		}
		if !strings.Contains(q.Get("q"), "golang testing") {
			t.Errorf("expected query to contain 'golang testing', got %q", q.Get("q"))
		}
		if q.Get("num") != "5" {
			t.Errorf("expected num '5', got %q", q.Get("num"))
		}

		resp := googleResponse{
			Items: []googleItem{
				{Title: "Result One", Link: "https://example.com/1", Snippet: "First result", DisplayLink: "example.com"},
				{Title: "Result Two", Link: "https://example.com/2", Snippet: "Second result", DisplayLink: "example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// Patch the Google API URL by using a custom HTTP client that rewrites requests
	client := &http.Client{
		Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport},
	}

	deps := Deps{
		HTTPClient: client,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5}),
	}

	g := NewGoogleProvider("test-key", "test-cx", deps)
	results, err := g.Web(context.Background(), WebSearchParams{
		Query:      "golang testing",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Result One" {
		t.Errorf("expected first result title 'Result One', got %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("expected first result URL 'https://example.com/1', got %q", results[0].URL)
	}
}

func TestGoogleProvider_WebSearch_WithSiteAndTimeRange(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		query := q.Get("q")
		if !strings.Contains(query, "site:example.com") {
			t.Errorf("expected site: operator in query, got %q", query)
		}
		if q.Get("dateRestrict") != "w1" {
			t.Errorf("expected dateRestrict 'w1', got %q", q.Get("dateRestrict"))
		}
		if q.Get("safe") != "active" {
			t.Errorf("expected safe 'active', got %q", q.Get("safe"))
		}
		if q.Get("lr") != "lang_en" {
			t.Errorf("expected lr 'lang_en', got %q", q.Get("lr"))
		}

		resp := googleResponse{Items: []googleItem{{Title: "R1", Link: "http://example.com", Snippet: "s", DisplayLink: "example.com"}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("test-key", "test-cx", deps)

	_, err := g.Web(context.Background(), WebSearchParams{
		Query:      "test",
		NumResults: 5,
		Site:       "example.com",
		TimeRange:  "week",
		Safe:       "active",
		Language:   "en",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGoogleProvider_ImageSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("searchType") != "image" {
			t.Errorf("expected searchType 'image', got %q", q.Get("searchType"))
		}

		resp := googleResponse{
			Items: []googleItem{
				{
					Title:       "Image One",
					Link:        "https://example.com/img.png",
					DisplayLink: "example.com",
					Image:       &googleImage{ThumbnailLink: "https://thumb.example.com/img.png", ContextLink: "https://example.com", Width: 800, Height: 600},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("test-key", "test-cx", deps)

	results, err := g.Images(context.Background(), ImageSearchParams{
		Query:      "cats",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Width != 800 {
		t.Errorf("expected width 800, got %d", results[0].Width)
	}
	if results[0].ThumbnailLink != "https://thumb.example.com/img.png" {
		t.Errorf("unexpected thumbnail link: %q", results[0].ThumbnailLink)
	}
}

func TestGoogleProvider_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("key", "cx", deps)

	_, err := g.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestGoogleProvider_NewsSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("sort") != "date" {
			t.Errorf("expected sort 'date', got %q", q.Get("sort"))
		}
		query := q.Get("q")
		if !strings.Contains(query, "site:nytimes.com") {
			t.Errorf("expected source in query, got %q", query)
		}

		resp := googleResponse{
			Items: []googleItem{
				{Title: "News Item", Link: "https://news.example.com/1", Snippet: "Breaking news", DisplayLink: "news.example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("key", "cx", deps)

	results, err := g.News(context.Background(), NewsSearchParams{
		Query:      "technology",
		NumResults: 5,
		Source:     "nytimes.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Source != "news.example.com" {
		t.Errorf("expected source 'news.example.com', got %q", results[0].Source)
	}
}

// =============================================================================
// Brave Provider Tests
// =============================================================================

func TestBraveProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "brave-key" {
			t.Errorf("expected subscription token 'brave-key', got %q", r.Header.Get("X-Subscription-Token"))
		}

		resp := braveWebResponse{}
		resp.Web.Results = []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		}{
			{Title: "Brave Result", URL: "https://example.com/brave", Description: "Found via Brave"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", deps)

	results, err := b.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Brave Result" {
		t.Errorf("expected title 'Brave Result', got %q", results[0].Title)
	}
}

func TestBraveProvider_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", deps)

	_, err := b.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

// =============================================================================
// Serper Provider Tests
// =============================================================================

func TestSerperProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("X-API-KEY") != "serper-key" {
			t.Errorf("expected API key 'serper-key', got %q", r.Header.Get("X-API-KEY"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected content-type 'application/json', got %q", r.Header.Get("Content-Type"))
		}

		resp := serperWebResponse{
			Organic: []struct {
				Title   string `json:"title"`
				Link    string `json:"link"`
				Snippet string `json:"snippet"`
			}{
				{Title: "Serper Result", Link: "https://example.com/serper", Snippet: "From Serper"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSerperProvider("serper-key", deps)

	results, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Serper Result" {
		t.Errorf("expected title 'Serper Result', got %q", results[0].Title)
	}
}

func TestSerperProvider_ImageSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := serperImageResponse{
			Images: []struct {
				Title        string `json:"title"`
				ImageURL     string `json:"imageUrl"`
				ThumbnailURL string `json:"thumbnailUrl"`
				Source       string `json:"source"`
			}{
				{Title: "Image", ImageURL: "https://img.example.com/1.jpg", ThumbnailURL: "https://thumb.example.com/1.jpg", Source: "example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSerperProvider("key", deps)

	results, err := s.Images(context.Background(), ImageSearchParams{Query: "cats", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Link != "https://img.example.com/1.jpg" {
		t.Errorf("unexpected image link: %q", results[0].Link)
	}
}

// =============================================================================
// SearXNG Provider Tests
// =============================================================================

func TestSearXNGProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("format") != "json" {
			t.Errorf("expected format 'json', got %q", q.Get("format"))
		}
		if q.Get("categories") != "general" {
			t.Errorf("expected categories 'general', got %q", q.Get("categories"))
		}

		resp := searxngResponse{
			Results: []searxngResult{
				{Title: "SearXNG Result", URL: "https://example.com/sx", Content: "From SearXNG", Engine: "google"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, deps)

	results, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "SearXNG Result" {
		t.Errorf("expected title 'SearXNG Result', got %q", results[0].Title)
	}
}

func TestSearXNGProvider_NewsSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("categories") != "news" {
			t.Errorf("expected categories 'news', got %q", q.Get("categories"))
		}
		if q.Get("time_range") != "week" {
			t.Errorf("expected time_range 'week', got %q", q.Get("time_range"))
		}

		resp := searxngResponse{
			Results: []searxngResult{
				{Title: "News", URL: "https://news.example.com/1", Content: "Breaking", Engine: "bing", PublishedDate: "2024-01-15"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, deps)

	results, err := s.News(context.Background(), NewsSearchParams{Query: "tech", NumResults: 5, Freshness: "week"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].PublishedAt != "2024-01-15" {
		t.Errorf("expected published date '2024-01-15', got %q", results[0].PublishedAt)
	}
}

func TestSearXNGProvider_LimitsResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return more results than requested
		resp := searxngResponse{
			Results: []searxngResult{
				{Title: "R1", URL: "http://1.com", Content: "c1"},
				{Title: "R2", URL: "http://2.com", Content: "c2"},
				{Title: "R3", URL: "http://3.com", Content: "c3"},
				{Title: "R4", URL: "http://4.com", Content: "c4"},
				{Title: "R5", URL: "http://5.com", Content: "c5"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, deps)

	results, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results (capped), got %d", len(results))
	}
}

// =============================================================================
// Lens Registry Tests
// =============================================================================

func TestLensRegistry_LoadFromDir(t *testing.T) {
	dir := t.TempDir()

	// Create test lens files
	techLens := Lens{
		Name:        "tech",
		Description: "Technology sites",
		Domains:     []string{"arstechnica.com", "techcrunch.com", "theverge.com"},
	}
	techData, _ := json.Marshal(techLens)
	os.WriteFile(filepath.Join(dir, "tech.json"), techData, 0644)

	sciLens := Lens{
		Name:        "science",
		Description: "Science sites",
		Domains:     []string{"nature.com", "sciencemag.org"},
	}
	sciData, _ := json.Marshal(sciLens)
	os.WriteFile(filepath.Join(dir, "science.json"), sciData, 0644)

	// Create a non-JSON file that should be ignored
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0644)

	// Create a subdirectory that should be ignored
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	err := registry.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir error: %v", err)
	}

	names := registry.List()
	if len(names) != 2 {
		t.Errorf("expected 2 lenses loaded, got %d", len(names))
	}

	lens, ok := registry.Get("tech")
	if !ok {
		t.Fatal("expected 'tech' lens to exist")
	}
	if lens.Description != "Technology sites" {
		t.Errorf("expected description 'Technology sites', got %q", lens.Description)
	}
	if len(lens.Domains) != 3 {
		t.Errorf("expected 3 domains, got %d", len(lens.Domains))
	}
}

func TestLensRegistry_LoadFromDir_UsesFilenameIfNoName(t *testing.T) {
	dir := t.TempDir()

	// Lens without a name field
	data := []byte(`{"description": "No name", "domains": ["example.com"]}`)
	os.WriteFile(filepath.Join(dir, "custom.json"), data, 0644)

	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	err := registry.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir error: %v", err)
	}

	_, ok := registry.Get("custom")
	if !ok {
		t.Error("expected lens to use filename 'custom' when name field is empty")
	}
}

func TestLensRegistry_LoadFromDir_NonexistentDir(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	err := registry.LoadFromDir("/nonexistent/path/to/lenses")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestLensRegistry_Get_NotFound(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	_, ok := registry.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for nonexistent lens")
	}
}

func TestLensRegistry_BuildSiteQuery(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	lens := &Lens{
		Name:    "test",
		Domains: []string{"example.com", "test.org", "docs.io"},
	}

	result := registry.BuildSiteQuery("golang", lens)
	if !strings.Contains(result, "golang") {
		t.Error("expected query to contain original search term")
	}
	if !strings.Contains(result, "site:example.com") {
		t.Error("expected query to contain site:example.com")
	}
	if !strings.Contains(result, "site:test.org") {
		t.Error("expected query to contain site:test.org")
	}
	if !strings.Contains(result, " OR ") {
		t.Error("expected query to contain OR operators")
	}
}

func TestLensRegistry_BuildSiteQuery_EmptyDomains(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	lens := &Lens{Name: "empty", Domains: nil}

	result := registry.BuildSiteQuery("golang", lens)
	if result != "golang" {
		t.Errorf("expected unchanged query 'golang', got %q", result)
	}
}

func TestLensRegistry_BuildSiteQuery_MaxDomains(t *testing.T) {
	domains := make([]string, 15)
	for i := range domains {
		domains[i] = "domain" + strings.Repeat("x", i) + ".com"
	}

	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	lens := &Lens{Name: "big", Domains: domains}

	result := registry.BuildSiteQuery("test", lens)
	// Should only include up to 10 domains
	siteCount := strings.Count(result, "site:")
	if siteCount != 10 {
		t.Errorf("expected 10 site: operators (max), got %d", siteCount)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		params   WebSearchParams
		expected string
	}{
		{WebSearchParams{Query: "hello"}, "hello"},
		{WebSearchParams{Query: "hello", Site: "example.com"}, "hello site:example.com"},
	}
	for _, tt := range tests {
		got := buildQuery(tt.params)
		if got != tt.expected {
			t.Errorf("buildQuery(%+v) = %q, want %q", tt.params, got, tt.expected)
		}
	}
}

func TestMapTimeRange(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "d1"},
		{"day", "d1"},
		{"week", "w1"},
		{"month", "m1"},
		{"year", "y1"},
		{"invalid", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := mapTimeRange(tt.input)
		if got != tt.expected {
			t.Errorf("mapTimeRange(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		val, min, max, expected int
	}{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{15, 1, 10, 10},
		{1, 1, 10, 1},
		{10, 1, 10, 10},
	}
	for _, tt := range tests {
		got := clamp(tt.val, tt.min, tt.max)
		if got != tt.expected {
			t.Errorf("clamp(%d, %d, %d) = %d, want %d", tt.val, tt.min, tt.max, got, tt.expected)
		}
	}
}

func TestMapBraveFreshness(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "pd"},
		{"day", "pd"},
		{"week", "pw"},
		{"month", "pm"},
		{"year", "py"},
		{"invalid", ""},
	}
	for _, tt := range tests {
		got := mapBraveFreshness(tt.input)
		if got != tt.expected {
			t.Errorf("mapBraveFreshness(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMapSearXNGTimeRange(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "day"},
		{"day", "day"},
		{"week", "week"},
		{"month", "month"},
		{"year", "year"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := mapSearXNGTimeRange(tt.input)
		if got != tt.expected {
			t.Errorf("mapSearXNGTimeRange(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// =============================================================================
// Test Helpers
// =============================================================================

// rewriteTransport redirects all requests to a test server
type rewriteTransport struct {
	baseURL string
	inner   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Preserve the original query string but redirect to test server
	newURL := t.baseURL + req.URL.Path + "?" + req.URL.RawQuery
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return t.inner.RoundTrip(newReq)
}

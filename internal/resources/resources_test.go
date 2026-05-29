package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func createTestServer(m *metrics.Collector, s *session.Manager) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	rl := ratelimit.New(config.RateLimitConfig{PerTenant: 120, Global: 1000, DailyQuota: 5000})
	RegisterAll(srv, m, s, rl, []ProviderInfo{{Name: "google", Type: "web"}})
	return srv
}

func connectTestClient(ctx context.Context, t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	return cs
}

func TestRegisterAllDoesNotPanic(t *testing.T) {
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	createTestServer(m, s)
}

// =============================================================================
// Resource Handler Tests
// =============================================================================

func TestToolStatsResource(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	// Record some tool calls to populate metrics
	m.RecordToolCall("web_search", 100*time.Millisecond, nil, "", false)
	m.RecordToolCall("web_search", 200*time.Millisecond, nil, "", true)
	m.RecordToolCall("scrape_page", 50*time.Millisecond, nil, "", false)

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "stats://tools"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected at least one resource content item")
	}

	if result.Contents[0].URI != "stats://tools" {
		t.Fatalf("expected URI 'stats://tools', got %q", result.Contents[0].URI)
	}

	var statsResponse map[string]any
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &statsResponse); err != nil {
		t.Fatalf("failed to parse stats JSON: %v", err)
	}

	tools, ok := statsResponse["tools"].(map[string]any)
	if !ok {
		t.Fatal("expected 'tools' key in stats response")
	}

	webSearch, ok := tools["web_search"].(map[string]any)
	if !ok {
		t.Fatal("expected 'web_search' in tools stats")
	}

	if webSearch["totalCalls"].(float64) != 2 {
		t.Fatalf("expected 2 total calls for web_search, got %v", webSearch["totalCalls"])
	}
	if webSearch["cacheHits"].(float64) != 1 {
		t.Fatalf("expected 1 cache hit for web_search, got %v", webSearch["cacheHits"])
	}

	scrapePage, ok := tools["scrape_page"].(map[string]any)
	if !ok {
		t.Fatal("expected 'scrape_page' in tools stats")
	}
	if scrapePage["totalCalls"].(float64) != 1 {
		t.Fatalf("expected 1 total call for scrape_page, got %v", scrapePage["totalCalls"])
	}
}

func TestSessionsResource(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10, SessionTTL: time.Hour})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	// Create some sessions
	_, _ = s.Create("tenant-1")
	_, _ = s.Create("tenant-2")

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "stats://sessions"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected at least one resource content item")
	}

	var sessResponse map[string]any
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &sessResponse); err != nil {
		t.Fatalf("failed to parse sessions JSON: %v", err)
	}

	activeSessions, ok := sessResponse["activeSessions"].(float64)
	if !ok {
		t.Fatal("expected 'activeSessions' in response")
	}
	if activeSessions != 2 {
		t.Fatalf("expected 2 active sessions, got %v", activeSessions)
	}
}

func TestSessionsResourceZero(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "stats://sessions"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}

	var sessResponse map[string]any
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &sessResponse); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if sessResponse["activeSessions"].(float64) != 0 {
		t.Fatalf("expected 0 active sessions, got %v", sessResponse["activeSessions"])
	}
}

// =============================================================================
// Prompt Handler Tests
// =============================================================================

func TestComprehensiveResearchPrompt(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "comprehensive-research",
		Arguments: map[string]string{
			"topic": "quantum computing",
			"depth": "deep",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	tc, ok := result.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}

	if !strings.Contains(tc.Text, "quantum computing") {
		t.Error("expected prompt to mention the topic")
	}
	if !strings.Contains(tc.Text, "academic_search") {
		t.Error("expected deep research to mention academic_search tool")
	}
}

func TestComprehensiveResearchPromptQuick(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "comprehensive-research",
		Arguments: map[string]string{
			"topic": "AI ethics",
			"depth": "quick",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	tc := result.Messages[0].Content.(*mcp.TextContent)
	if strings.Contains(tc.Text, "CROSS-REFERENCE") {
		t.Error("quick depth should not include cross-reference step")
	}
}

func TestFactCheckPrompt(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "fact-check",
		Arguments: map[string]string{
			"claim":   "The earth is flat",
			"context": "social media debate",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}

	tc := result.Messages[0].Content.(*mcp.TextContent)
	if !strings.Contains(tc.Text, "The earth is flat") {
		t.Error("expected prompt to contain the claim")
	}
	if !strings.Contains(tc.Text, "social media debate") {
		t.Error("expected prompt to contain the context")
	}
}

func TestCompetitiveAnalysisPrompt(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "competitive-analysis",
		Arguments: map[string]string{
			"company": "Acme Corp",
			"market":  "cloud computing",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	tc := result.Messages[0].Content.(*mcp.TextContent)
	if !strings.Contains(tc.Text, "Acme Corp") {
		t.Error("expected prompt to mention the company")
	}
	if !strings.Contains(tc.Text, "cloud computing") {
		t.Error("expected prompt to mention the market")
	}
}

func TestLiteratureReviewPrompt(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "literature-review",
		Arguments: map[string]string{
			"topic":     "CRISPR gene editing",
			"year_from": "2020",
			"year_to":   "2025",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	tc := result.Messages[0].Content.(*mcp.TextContent)
	if !strings.Contains(tc.Text, "CRISPR gene editing") {
		t.Error("expected prompt to mention the topic")
	}
	if !strings.Contains(tc.Text, "2020") || !strings.Contains(tc.Text, "2025") {
		t.Error("expected prompt to mention year range")
	}
}

func TestToolStatsResourceWithErrors(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	// Record calls with errors
	m.RecordToolCall("web_search", 100*time.Millisecond, nil, "", false)
	m.RecordToolCall("web_search", 200*time.Millisecond, fmt.Errorf("timeout"), "timeout", false)
	m.RecordToolCall("web_search", 50*time.Millisecond, fmt.Errorf("rate limit"), "rate_limited", false)

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "stats://tools"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}

	var statsResponse map[string]any
	json.Unmarshal([]byte(result.Contents[0].Text), &statsResponse)

	tools := statsResponse["tools"].(map[string]any)
	webSearch := tools["web_search"].(map[string]any)

	if webSearch["totalCalls"].(float64) != 3 {
		t.Fatalf("expected 3 total calls, got %v", webSearch["totalCalls"])
	}
	if webSearch["successCalls"].(float64) != 1 {
		t.Fatalf("expected 1 success call, got %v", webSearch["successCalls"])
	}
	if webSearch["errorCalls"].(float64) != 2 {
		t.Fatalf("expected 2 error calls, got %v", webSearch["errorCalls"])
	}
}

func TestRateLimitResource(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "stats://rate-limits"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected at least one resource content item")
	}

	var response map[string]any
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &response); err != nil {
		t.Fatalf("failed to parse rate-limits JSON: %v", err)
	}

	cfg, ok := response["config"].(map[string]any)
	if !ok {
		t.Fatal("expected 'config' key in rate-limits response")
	}

	if cfg["perMinutePerTenant"].(float64) != 120 {
		t.Fatalf("expected perMinutePerTenant=120, got %v", cfg["perMinutePerTenant"])
	}
	if cfg["dailyPerTenant"].(float64) != 5000 {
		t.Fatalf("expected dailyPerTenant=5000, got %v", cfg["dailyPerTenant"])
	}

	guidance, ok := response["guidance"].(string)
	if !ok || guidance == "" {
		t.Fatal("expected non-empty 'guidance' string in response")
	}
}

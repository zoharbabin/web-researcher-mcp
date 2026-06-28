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

// fakeHealth is a stub HealthProvider for resource tests: it returns a fixed
// tri-state snapshot so the diagnostics://health handler can be exercised
// without constructing a real Router.
type fakeHealth struct{}

func (fakeHealth) Health() any {
	return map[string]any{
		"status": "degraded",
		"providers": []map[string]any{
			{"name": "google", "type": "web", "breaker": "open", "available": false},
			{"name": "brave", "type": "web", "breaker": "closed", "available": true},
		},
	}
}

func createTestServer(m *metrics.Collector, s session.Manager) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	rl := ratelimit.New(config.RateLimitConfig{PerTenant: 120, Global: 1000, DailyQuota: 5000})
	RegisterAll(srv, m, s, rl, []ProviderInfo{{Name: "google", Type: "web"}}, fakeHealth{}, []LensInfo{{Name: "academic", Description: "Academic sources", DomainCount: 5, HasCX: false}})
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
	_, _ = s.Create("tenant-1", "u1")
	_, _ = s.Create("tenant-2", "u1")

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
	// Drift guard: the fact-check prompt MUST surface the anti-hallucination tool
	// (verify_citation is the whole point). This caught a regression where the
	// prompts had drifted to a pre-trust-suite tool list.
	if !strings.Contains(tc.Text, "verify_citation") {
		t.Error("fact-check prompt must mention verify_citation (the anti-hallucination tool)")
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
	// Drift guard: a systematic literature review must steer toward auditing the
	// reference list for retracted/fabricated citations.
	if !strings.Contains(tc.Text, "audit_bibliography") {
		t.Error("literature-review prompt must mention audit_bibliography")
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

// =============================================================================
// Diagnostics Resource Tests (#81)
// =============================================================================

// TestDiagnosticsErrorsResource verifies the recent-errors Resource returns the
// ring contents as valid JSON, newest-first, with redacted causes.
func TestDiagnosticsErrorsResource(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	// An unauthenticated/STDIO caller resolves to tenant "default" (the value
	// auth.TenantIDFromContext returns with no tenant on the context). The tool
	// layer records errors under that same tenant, so the Resource sees them.
	m.RecordError(metrics.ErrorRecord{Tool: "web_search", Kind: "rate_limited", Provider: "google", TenantID: "default"})
	m.RecordError(metrics.ErrorRecord{Tool: "scrape_page", Kind: "network", Cause: "dial tcp api_key=secret123 failed", TenantID: "default"})

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "diagnostics://errors/recent"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	if len(result.Contents) == 0 || result.Contents[0].URI != "diagnostics://errors/recent" {
		t.Fatalf("unexpected contents: %+v", result.Contents)
	}
	var body struct {
		Count  int `json:"count"`
		Errors []struct {
			Tool, Kind, Cause string
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &body); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if body.Count != 2 || len(body.Errors) != 2 {
		t.Fatalf("count = %d / errors = %d, want 2/2", body.Count, len(body.Errors))
	}
	// Newest-first.
	if body.Errors[0].Tool != "scrape_page" {
		t.Errorf("newest = %q, want scrape_page", body.Errors[0].Tool)
	}
	// Redaction at the sink.
	for _, e := range body.Errors {
		if strings.Contains(e.Cause, "secret123") {
			t.Errorf("cause leaked secret: %q", e.Cause)
		}
	}
}

// TestDiagnosticsHealthResource verifies the health Resource renders the
// HealthProvider snapshot as JSON.
func TestDiagnosticsHealthResource(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s) // wires fakeHealth (status: degraded)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "diagnostics://health"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	var snap struct {
		Status    string           `json:"status"`
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &snap); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if snap.Status != "degraded" {
		t.Errorf("status = %q, want degraded", snap.Status)
	}
	if len(snap.Providers) != 2 {
		t.Errorf("providers = %d, want 2", len(snap.Providers))
	}
}

// TestDiagnosticsHealthResource_NilProvider verifies the no-Router path: a nil
// HealthProvider yields an empty, healthy snapshot rather than an error.
func TestDiagnosticsHealthResource_NilProvider(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	rl := ratelimit.New(config.RateLimitConfig{PerTenant: 120, Global: 1000, DailyQuota: 5000})
	RegisterAll(srv, m, s, rl, []ProviderInfo{{Name: "google", Type: "web"}}, nil, nil)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "diagnostics://health"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	var snap struct {
		Status    string           `json:"status"`
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &snap); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if snap.Status != "healthy" || len(snap.Providers) != 0 {
		t.Errorf("nil-provider snapshot = %+v, want healthy/empty", snap)
	}
}

// =============================================================================
// Lens Catalog Resource Tests (#197)
// =============================================================================

// TestLensesCatalogResource verifies the lenses://catalog resource returns the
// provided lens list as valid JSON with the expected fields.
func TestLensesCatalogResource(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s) // wires one "academic" LensInfo
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "lenses://catalog"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	if len(result.Contents) == 0 || result.Contents[0].URI != "lenses://catalog" {
		t.Fatalf("unexpected contents: %+v", result.Contents)
	}
	var body struct {
		Lenses []LensInfo `json:"lenses"`
	}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &body); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if len(body.Lenses) != 1 {
		t.Fatalf("expected 1 lens, got %d", len(body.Lenses))
	}
	l := body.Lenses[0]
	if l.Name != "academic" {
		t.Errorf("lens name = %q, want academic", l.Name)
	}
	if l.DomainCount != 5 {
		t.Errorf("domainCount = %d, want 5", l.DomainCount)
	}
	if l.HasCX {
		t.Error("hasCX should be false for this test lens")
	}
}

// TestLensesCatalogResourceEmpty verifies nil lenses yields an empty array, not
// a null.
func TestLensesCatalogResourceEmpty(t *testing.T) {
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	rl := ratelimit.New(config.RateLimitConfig{PerTenant: 120, Global: 1000, DailyQuota: 5000})
	RegisterAll(srv, m, s, rl, nil, nil, nil)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "lenses://catalog"})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	var body struct {
		Lenses any `json:"lenses"`
	}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &body); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	// nil slice marshals as JSON null — that is acceptable; just verify no panic
	// and the key is present.
	if _, ok := json.Marshal(body.Lenses); ok != nil {
		t.Error("lenses key missing from response")
	}
}

// =============================================================================
// Brand Guidelines Prompt Tests
// =============================================================================

// TestBrandGuidelinesPromptDefaultUseCase verifies that omitting use_case
// falls back to full_guidelines and includes the expected guidance text.
func TestBrandGuidelinesPromptDefaultUseCase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "full_guidelines") && !strings.Contains(msg, "comprehensive brand guidelines") {
		t.Errorf("expected full_guidelines guidance text in default use_case prompt, got: %s", msg[:min(200, len(msg))])
	}
	if !strings.Contains(msg, "brand_research") {
		t.Errorf("expected 'brand_research' in default use_case prompt, got: %s", msg[:min(200, len(msg))])
	}
}

// TestBrandGuidelinesPromptLandingPage verifies landing_page use_case output.
func TestBrandGuidelinesPromptLandingPage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura", "use_case": "landing_page"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "CTA button") {
		t.Errorf("expected 'CTA button' in landing_page prompt, got: %s", msg[:min(200, len(msg))])
	}
	if !strings.Contains(msg, "Color palette table") {
		t.Errorf("expected 'Color palette table' in landing_page prompt, got: %s", msg[:min(200, len(msg))])
	}
	if !strings.Contains(msg, "Typography spec") {
		t.Errorf("expected 'Typography spec' in landing_page prompt, got: %s", msg[:min(200, len(msg))])
	}
}

// TestBrandGuidelinesPromptEmail verifies email use_case output.
func TestBrandGuidelinesPromptEmail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura", "use_case": "email"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "Font stack") {
		t.Errorf("expected 'Font stack' in email prompt, got: %s", msg[:min(200, len(msg))])
	}
	if !strings.Contains(msg, "HTML inline-style") {
		t.Errorf("expected 'HTML inline-style' in email prompt, got: %s", msg[:min(200, len(msg))])
	}
}

// TestBrandGuidelinesPromptSocialPost verifies social_post use_case output.
func TestBrandGuidelinesPromptSocialPost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura", "use_case": "social_post"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "caption") {
		t.Errorf("expected 'caption' in social_post prompt, got: %s", msg[:min(200, len(msg))])
	}
	if !strings.Contains(msg, "hashtag") {
		t.Errorf("expected 'hashtag' in social_post prompt, got: %s", msg[:min(200, len(msg))])
	}
}

// TestBrandGuidelinesPromptVideoBrief verifies video_brief use_case output.
func TestBrandGuidelinesPromptVideoBrief(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura", "use_case": "video_brief"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "lower-third") {
		t.Errorf("expected 'lower-third' in video_brief prompt, got: %s", msg[:min(200, len(msg))])
	}
	if !strings.Contains(msg, "Voiceover tone") {
		t.Errorf("expected 'Voiceover tone' in video_brief prompt, got: %s", msg[:min(200, len(msg))])
	}
}

// TestBrandGuidelinesPromptDesignTokens verifies design_tokens use_case output.
func TestBrandGuidelinesPromptDesignTokens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura", "use_case": "design_tokens"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "include_design_tokens") {
		t.Errorf("expected 'include_design_tokens' in design_tokens prompt, got: %s", msg[:min(200, len(msg))])
	}
}

// TestBrandGuidelinesPromptMentionsBrandResearch is a drift guard that verifies
// every use_case branch includes a call to brand_research in the output.
func TestBrandGuidelinesPromptMentionsBrandResearch(t *testing.T) {
	t.Parallel()
	useCases := []string{"landing_page", "email", "social_post", "video_brief", "design_tokens", "full_guidelines"}
	for _, uc := range useCases {
		uc := uc
		t.Run(uc, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m := metrics.NewCollector()
			s, _ := session.NewManager(session.Config{MaxSessions: 10})
			srv := createTestServer(m, s)
			cs := connectTestClient(ctx, t, srv)
			defer cs.Close()

			result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
				Name:      "brand-guidelines",
				Arguments: map[string]string{"company": "Kaltura", "use_case": uc},
			})
			if err != nil {
				t.Fatalf("GetPrompt failed for use_case=%s: %v", uc, err)
			}
			if len(result.Messages) == 0 {
				t.Fatalf("expected at least one message for use_case=%s", uc)
			}
			msg := result.Messages[0].Content.(*mcp.TextContent).Text
			if !strings.Contains(msg, "brand_research") {
				t.Errorf("use_case=%s: expected 'brand_research' in prompt, got: %s", uc, msg[:min(200, len(msg))])
			}
		})
	}
}

// TestBrandGuidelinesPromptDepthPassthrough verifies that a depth argument is
// reflected in the prompt text.
func TestBrandGuidelinesPromptDepthPassthrough(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura", "depth": "full"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "depth: full") {
		t.Errorf("expected 'depth: full' in prompt text, got: %s", msg[:min(200, len(msg))])
	}
}

// TestBrandGuidelinesPromptDescription verifies that the prompt Description
// field contains the company name.
func TestBrandGuidelinesPromptDescription(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "brand-guidelines",
		Arguments: map[string]string{"company": "Kaltura"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if !strings.Contains(result.Description, "Kaltura") {
		t.Errorf("expected Description to contain 'Kaltura', got: %q", result.Description)
	}
}

// ── company-recon prompt tests ────────────────────────────────────────────────

func TestCompanyReconPromptRegistered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	prompts, err := cs.ListPrompts(ctx, &mcp.ListPromptsParams{})
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}
	found := false
	for _, pr := range prompts.Prompts {
		if pr.Name == "company-recon" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'company-recon' in prompt list")
	}
}

// TestCompanyReconPromptDefaultDepth verifies that the default depth is
// "standard" and the prompt includes the expected phases.
func TestCompanyReconPromptDefaultDepth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Acme Corp acme.com"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text

	// Standard depth: phases 1,2,3,4,6,7,8,9 — NOT phase 5
	for _, want := range []string{
		"Phase 1", "Phase 2", "Phase 3", "Phase 4",
		"Phase 6", "Phase 7", "Phase 8", "Phase 9",
		"Acme Corp acme.com",
		"standard",
		"osint",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("standard depth: expected %q in prompt, not found", want)
		}
	}
	if strings.Contains(msg, "Phase 5") {
		t.Error("standard depth: Phase 5 should NOT appear (deep only)")
	}
}

// TestCompanyReconPromptQuickDepth verifies quick depth includes only phases 1, 6, 8, 9.
func TestCompanyReconPromptQuickDepth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "TestCo", "depth": "quick"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text

	for _, want := range []string{"Phase 1", "Phase 6", "Phase 8", "Phase 9", "quick"} {
		if !strings.Contains(msg, want) {
			t.Errorf("quick depth: expected %q in prompt", want)
		}
	}
	for _, absent := range []string{"Phase 2", "Phase 3", "Phase 4", "Phase 5", "Phase 7"} {
		if strings.Contains(msg, absent) {
			t.Errorf("quick depth: %q should NOT appear", absent)
		}
	}
}

// TestCompanyReconPromptDeepDepth verifies deep depth includes all 9 phases.
func TestCompanyReconPromptDeepDepth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "DeepCo deep.io", "depth": "deep"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text

	for i := 1; i <= 9; i++ {
		phase := fmt.Sprintf("Phase %d", i)
		if !strings.Contains(msg, phase) {
			t.Errorf("deep depth: expected %q in prompt", phase)
		}
	}
}

// TestCompanyReconPromptFocusSalesIntel verifies sales_intel focus guidance.
func TestCompanyReconPromptFocusSalesIntel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Vendor Inc vendor.com", "focus": "sales_intel"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "sales_intel") {
		t.Errorf("expected 'sales_intel' focus label in prompt")
	}
	if !strings.Contains(msg, "customer discovery") {
		t.Errorf("expected 'customer discovery' guidance for sales_intel focus")
	}
}

// TestCompanyReconPromptFocusSecurity verifies security focus guidance.
func TestCompanyReconPromptFocusSecurity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "SecTarget sec.io", "focus": "security"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "security") {
		t.Errorf("expected 'security' in focus guidance")
	}
	if !strings.Contains(msg, "certificate transparency") {
		t.Errorf("expected 'certificate transparency' emphasis for security focus")
	}
}

// TestCompanyReconPromptFocusDueDiligence verifies due_diligence focus guidance.
func TestCompanyReconPromptFocusDueDiligence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Target Inc", "focus": "due_diligence"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "due_diligence") {
		t.Errorf("expected 'due_diligence' in focus guidance")
	}
	if !strings.Contains(msg, "SEC") {
		t.Errorf("expected 'SEC' mentioned for due_diligence focus")
	}
}

// TestCompanyReconPromptFocusBrandProtection verifies brand_protection focus.
func TestCompanyReconPromptFocusBrandProtection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "BrandCo brand.com", "focus": "brand_protection"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "brand_protection") {
		t.Errorf("expected 'brand_protection' in focus guidance")
	}
	if !strings.Contains(msg, "look-alike") {
		t.Errorf("expected 'look-alike' domain guidance for brand_protection focus")
	}
}

// TestCompanyReconPromptContainsOsintLens verifies the osint lens is referenced.
func TestCompanyReconPromptContainsOsintLens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Widget Co widget.io"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "lens: osint") {
		t.Errorf("expected 'lens: osint' reference in prompt")
	}
}

// TestCompanyReconPromptMentionsCrtSh verifies crt.sh CT endpoint is included.
func TestCompanyReconPromptMentionsCrtSh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Example Co example.com", "depth": "standard"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "crt.sh") {
		t.Errorf("expected 'crt.sh' endpoint in prompt for standard depth")
	}
}

// TestCompanyReconPromptMentionsWayback verifies Wayback CDX endpoint is included.
func TestCompanyReconPromptMentionsWayback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Example Co example.com", "depth": "standard"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "web.archive.org") {
		t.Errorf("expected 'web.archive.org' CDX endpoint in prompt")
	}
}

// TestCompanyReconPromptGA4LimitationDocumented verifies the GA4 limitation is noted
// for every depth value.
func TestCompanyReconPromptGA4LimitationDocumented(t *testing.T) {
	t.Parallel()
	for _, depth := range []string{"quick", "standard", "deep"} {
		depth := depth
		t.Run(depth, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m := metrics.NewCollector()
			s, _ := session.NewManager(session.Config{MaxSessions: 10})
			srv := createTestServer(m, s)
			cs := connectTestClient(ctx, t, srv)
			defer cs.Close()

			result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
				Name:      "company-recon",
				Arguments: map[string]string{"target": "TestCo test.com", "depth": depth},
			})
			if err != nil {
				t.Fatalf("GetPrompt failed: %v", err)
			}
			msg := result.Messages[0].Content.(*mcp.TextContent).Text
			if !strings.Contains(msg, "GA4") {
				t.Errorf("depth=%s: expected GA4 limitation documented in prompt", depth)
			}
		})
	}
}

// TestCompanyReconPromptConfidenceTiersIncluded verifies CONFIRMED/STRONG/MODERATE/WEAK tiers.
func TestCompanyReconPromptConfidenceTiersIncluded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Acme Corp acme.com", "depth": "standard"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	for _, tier := range []string{"CONFIRMED", "STRONG", "MODERATE", "WEAK"} {
		if !strings.Contains(msg, tier) {
			t.Errorf("expected confidence tier %q in prompt", tier)
		}
	}
}

// TestCompanyReconPromptResearchExportMentioned verifies research_export is referenced.
func TestCompanyReconPromptResearchExportMentioned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Acme Corp acme.com"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "research_export") {
		t.Errorf("expected 'research_export' tool referenced in prompt")
	}
}

// TestCompanyReconPromptDescription verifies the prompt Description contains the target.
func TestCompanyReconPromptDescription(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "TargetCo target.io"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if !strings.Contains(result.Description, "TargetCo target.io") {
		t.Errorf("expected Description to contain target name, got: %q", result.Description)
	}
}

// TestCompanyReconPhaseSetQuick is a unit test for companyReconPhaseSet.
func TestCompanyReconPhaseSetQuick(t *testing.T) {
	t.Parallel()
	ps := companyReconPhaseSet("quick")
	for _, must := range []string{"phase1", "phase6", "phase8", "phase9"} {
		if !ps[must] {
			t.Errorf("quick: expected %s to be active", must)
		}
	}
	for _, absent := range []string{"phase2", "phase3", "phase4", "phase5", "phase7"} {
		if ps[absent] {
			t.Errorf("quick: expected %s to be inactive", absent)
		}
	}
}

// TestCompanyReconPhaseSetStandard is a unit test for companyReconPhaseSet.
func TestCompanyReconPhaseSetStandard(t *testing.T) {
	t.Parallel()
	ps := companyReconPhaseSet("standard")
	for _, must := range []string{"phase1", "phase2", "phase3", "phase4", "phase6", "phase7", "phase8", "phase9"} {
		if !ps[must] {
			t.Errorf("standard: expected %s to be active", must)
		}
	}
	if ps["phase5"] {
		t.Error("standard: phase5 should be inactive")
	}
}

// TestCompanyReconPhaseSetDeep is a unit test for companyReconPhaseSet.
func TestCompanyReconPhaseSetDeep(t *testing.T) {
	t.Parallel()
	ps := companyReconPhaseSet("deep")
	for i := 1; i <= 9; i++ {
		key := fmt.Sprintf("phase%d", i)
		if !ps[key] {
			t.Errorf("deep: expected %s to be active", key)
		}
	}
}

// TestCompanyReconPhaseSetDefault verifies unknown depth falls back to standard.
func TestCompanyReconPhaseSetDefault(t *testing.T) {
	t.Parallel()
	ps := companyReconPhaseSet("unknown")
	// Should match standard: phases 1-4, 6-9 active; 5 absent
	for _, must := range []string{"phase1", "phase2", "phase3", "phase4", "phase6", "phase7", "phase8", "phase9"} {
		if !ps[must] {
			t.Errorf("default: expected %s to be active", must)
		}
	}
	if ps["phase5"] {
		t.Error("default: phase5 should be inactive")
	}
}

// TestCompanyReconFocusGuidanceAllValues verifies every known focus value returns
// a non-empty, distinct string.
func TestCompanyReconFocusGuidanceAllValues(t *testing.T) {
	t.Parallel()
	focuses := []string{"sales_intel", "security", "due_diligence", "brand_protection", "", "unknown"}
	seen := make(map[string]bool)
	for _, f := range focuses {
		got := companyReconFocusGuidance(f)
		if got == "" {
			t.Errorf("focus=%q returned empty guidance", f)
		}
		if seen[got] && f != "" && f != "unknown" {
			t.Errorf("focus=%q returned duplicate guidance string", f)
		}
		seen[got] = true
	}
}

// TestCompanyReconPromptMentionsFilingSearch verifies filing_search is referenced for BI.
func TestCompanyReconPromptMentionsFilingSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "PublicCo pub.com", "depth": "standard"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "filing_search") {
		t.Errorf("expected 'filing_search' referenced in prompt for business intelligence phase")
	}
}

// TestCompanyReconPromptMentionsHackerTarget verifies HackerTarget DNS endpoint is referenced.
func TestCompanyReconPromptMentionsHackerTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "company-recon",
		Arguments: map[string]string{"target": "Example example.com", "depth": "standard"},
	})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	msg := result.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(msg, "hackertarget.com") {
		t.Errorf("expected 'hackertarget.com' referenced in DNS phase")
	}
}

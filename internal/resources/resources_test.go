package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func TestRegisterAllDoesNotPanic(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0")
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)
}

// =============================================================================
// Resource Handler Tests
// =============================================================================

func TestToolStatsResource(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithResourceCapabilities(true, true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	// Record some tool calls to populate metrics
	m.RecordToolCall("web_search", 100*time.Millisecond, nil, "", false)
	m.RecordToolCall("web_search", 200*time.Millisecond, nil, "", true)
	m.RecordToolCall("scrape_page", 50*time.Millisecond, nil, "", false)

	// Call the resource handler via HandleMessage
	result := srv.HandleMessage(context.Background(), mustMarshalResourceRead("stats://tools"))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Contents []struct {
			URI      string `json:"uri"`
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal resource result: %v", err)
	}

	if len(raw.Contents) == 0 {
		t.Fatal("expected at least one resource content item")
	}

	if raw.Contents[0].URI != "stats://tools" {
		t.Fatalf("expected URI 'stats://tools', got %q", raw.Contents[0].URI)
	}

	// Parse the JSON text to verify tool stats
	var statsResponse map[string]any
	if err := json.Unmarshal([]byte(raw.Contents[0].Text), &statsResponse); err != nil {
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
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithResourceCapabilities(true, true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10, SessionTTL: time.Hour})
	RegisterAll(srv, m, s)

	// Create some sessions
	_, _ = s.Create("tenant-1")
	_, _ = s.Create("tenant-2")

	result := srv.HandleMessage(context.Background(), mustMarshalResourceRead("stats://sessions"))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Contents []struct {
			URI      string `json:"uri"`
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal resource result: %v", err)
	}

	if len(raw.Contents) == 0 {
		t.Fatal("expected at least one resource content item")
	}

	var sessResponse map[string]any
	if err := json.Unmarshal([]byte(raw.Contents[0].Text), &sessResponse); err != nil {
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
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithResourceCapabilities(true, true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	result := srv.HandleMessage(context.Background(), mustMarshalResourceRead("stats://sessions"))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	var sessResponse map[string]any
	if err := json.Unmarshal([]byte(raw.Contents[0].Text), &sessResponse); err != nil {
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
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithPromptCapabilities(true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	result := srv.HandleMessage(context.Background(), mustMarshalPromptGet("comprehensive-research", map[string]string{
		"topic": "quantum computing",
		"depth": "deep",
	}))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Description string `json:"description"`
		Messages    []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal prompt result: %v", err)
	}

	if len(raw.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	text := raw.Messages[0].Content.Text
	if text == "" {
		t.Fatal("expected non-empty prompt text")
	}

	// Check it contains the topic
	if !containsStr(text, "quantum computing") {
		t.Error("expected prompt to mention the topic")
	}
	// Deep depth should include step 6
	if !containsStr(text, "ACADEMIC") {
		t.Error("expected deep research to include academic step")
	}
}

func TestComprehensiveResearchPromptQuick(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithPromptCapabilities(true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	result := srv.HandleMessage(context.Background(), mustMarshalPromptGet("comprehensive-research", map[string]string{
		"topic": "AI ethics",
		"depth": "quick",
	}))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Messages []struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	text := raw.Messages[0].Content.Text
	// Quick should not include step 4+
	if containsStr(text, "CROSS-REFERENCE") {
		t.Error("quick depth should not include cross-reference step")
	}
}

func TestFactCheckPrompt(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithPromptCapabilities(true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	result := srv.HandleMessage(context.Background(), mustMarshalPromptGet("fact-check", map[string]string{
		"claim":   "The earth is flat",
		"context": "social media debate",
	}))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Description string `json:"description"`
		Messages    []struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(raw.Messages) == 0 {
		t.Fatal("expected messages")
	}

	text := raw.Messages[0].Content.Text
	if !containsStr(text, "The earth is flat") {
		t.Error("expected prompt to contain the claim")
	}
	if !containsStr(text, "social media debate") {
		t.Error("expected prompt to contain the context")
	}
}

func TestCompetitiveAnalysisPrompt(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithPromptCapabilities(true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	result := srv.HandleMessage(context.Background(), mustMarshalPromptGet("competitive-analysis", map[string]string{
		"company": "Acme Corp",
		"market":  "cloud computing",
	}))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Messages []struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	text := raw.Messages[0].Content.Text
	if !containsStr(text, "Acme Corp") {
		t.Error("expected prompt to mention the company")
	}
	if !containsStr(text, "cloud computing") {
		t.Error("expected prompt to mention the market")
	}
}

func TestLiteratureReviewPrompt(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithPromptCapabilities(true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	result := srv.HandleMessage(context.Background(), mustMarshalPromptGet("literature-review", map[string]string{
		"topic":     "CRISPR gene editing",
		"year_from": "2020",
		"year_to":   "2025",
	}))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Messages []struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	text := raw.Messages[0].Content.Text
	if !containsStr(text, "CRISPR gene editing") {
		t.Error("expected prompt to mention the topic")
	}
	if !containsStr(text, "2020") || !containsStr(text, "2025") {
		t.Error("expected prompt to mention year range")
	}
}

func TestToolStatsResourceWithErrors(t *testing.T) {
	srv := server.NewMCPServer("test", "1.0.0",
		server.WithResourceCapabilities(true, true),
	)
	m := metrics.NewCollector()
	s := session.NewManager(session.Config{MaxSessions: 10})
	RegisterAll(srv, m, s)

	// Record calls with errors
	m.RecordToolCall("web_search", 100*time.Millisecond, nil, "", false)
	m.RecordToolCall("web_search", 200*time.Millisecond, fmt.Errorf("timeout"), "timeout", false)
	m.RecordToolCall("web_search", 50*time.Millisecond, fmt.Errorf("rate limit"), "rate_limited", false)

	result := srv.HandleMessage(context.Background(), mustMarshalResourceRead("stats://tools"))
	resp := result.(mcp.JSONRPCResponse)
	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	json.Unmarshal(resultBytes, &raw)

	var statsResponse map[string]any
	json.Unmarshal([]byte(raw.Contents[0].Text), &statsResponse)

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

// =============================================================================
// Helpers
// =============================================================================

func mustMarshalResourceRead(uri string) json.RawMessage {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params": map[string]any{
			"uri": uri,
		},
	}
	b, _ := json.Marshal(msg)
	return b
}

func mustMarshalPromptGet(name string, args map[string]string) json.RawMessage {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/get",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	b, _ := json.Marshal(msg)
	return b
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

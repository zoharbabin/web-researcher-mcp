package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResearchPanelTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"question": "What is the sky's color?", "use_cache": false},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res.Content)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["question"] != "What is the sky's color?" {
		t.Errorf("expected question echoed, got %v", out["question"])
	}
	panel, _ := out["panel"].([]any)
	if len(panel) != 2 {
		t.Errorf("expected 2 panel entries (mock-a, mock-b), got %d", len(panel))
	}
	meta, _ := out["_meta"].(map[string]any)
	if meta["models_succeeded"].(float64) != 2 {
		t.Errorf("expected both mock providers to succeed, got %v", meta)
	}
	if out["trust"] != untrustedContentTrust {
		t.Errorf("expected trust marker %q, got %v", untrustedContentTrust, out["trust"])
	}
}

func TestResearchPanelEmptyQuestion(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"question": "  "},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for blank question, got success: %v", res.Content)
	}
}

func TestResearchPanelQuestionTooLarge(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	huge := make([]byte, researchPanelMaxQuestion+1)
	for i := range huge {
		huge[i] = 'a'
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"question": string(huge)},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for oversized question, got success")
	}
}

func TestResearchPanelNotRegisteredWhenNoProviders(t *testing.T) {
	deps := setupTestDeps()
	deps.ResearchPanelProviders = nil
	srv := createTestServer(deps)

	ctx := context.Background()
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		if tool.Name == "research_panel" {
			t.Fatalf("research_panel should not be registered with zero providers")
		}
	}
}

func TestResolveResearchPanel_ModelsOverride(t *testing.T) {
	deps := setupTestDeps()
	input := researchPanelInput{Models: []string{"mock-a/model-a"}}

	panel := resolveResearchPanel(input, deps)
	if len(panel) != 1 {
		t.Fatalf("expected exactly 1 matched provider, got %d", len(panel))
	}
	if panel[0].Name() != "mock-a" || panel[0].ModelID() != "model-a" {
		t.Errorf("expected mock-a/model-a, got %s/%s", panel[0].Name(), panel[0].ModelID())
	}
}

func TestResolveResearchPanel_UnknownModelsIgnored(t *testing.T) {
	deps := setupTestDeps()
	input := researchPanelInput{Models: []string{"nonexistent/model"}}

	panel := resolveResearchPanel(input, deps)
	if len(panel) != 0 {
		t.Errorf("expected zero matches for an unconfigured model spec, got %d", len(panel))
	}
}

func TestResolveResearchPanel_MaxModelsClamp(t *testing.T) {
	deps := setupTestDeps()
	input := researchPanelInput{MaxModels: 1}

	panel := resolveResearchPanel(input, deps)
	if len(panel) != 1 {
		t.Fatalf("expected panel clamped to 1, got %d", len(panel))
	}
}

// TestResearchPanelMultiInstanceIsolation proves rule 1 (issue #441): two
// Dependencies instances, each with distinct ResearchPanelProviders, are
// served by two independent in-process servers in the same test binary
// without leaking panel membership across instances.
func TestResearchPanelMultiInstanceIsolation(t *testing.T) {
	ctx := context.Background()

	depsA := setupTestDeps()
	depsA.ResearchPanelProviders = []ModelProvider{
		&mockModelProvider{name: "provider-a", modelID: "model-a", text: "Answer from A."},
	}
	depsB := setupTestDeps()
	depsB.ResearchPanelProviders = []ModelProvider{
		&mockModelProvider{name: "provider-b", modelID: "model-b", text: "Answer from B."},
	}

	srvA := createTestServer(depsA)
	srvB := createTestServer(depsB)
	sessionA := connectTestClient(ctx, t, srvA)
	defer sessionA.Close()
	sessionB := connectTestClient(ctx, t, srvB)
	defer sessionB.Close()

	resA, err := sessionA.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"question": "isolation check", "use_cache": false},
	})
	if err != nil || resA.IsError {
		t.Fatalf("server A call failed: err=%v isError=%v content=%v", err, resA.IsError, resA.Content)
	}
	var outA map[string]any
	if err := json.Unmarshal([]byte(resA.Content[0].(*mcp.TextContent).Text), &outA); err != nil {
		t.Fatalf("unmarshal A: %v", err)
	}
	panelA, _ := outA["panel"].([]any)
	if len(panelA) != 1 {
		t.Fatalf("server A should see only its own 1 provider, got %d", len(panelA))
	}
	if entry := panelA[0].(map[string]any); entry["provider"] != "provider-a" {
		t.Errorf("server A panel entry should be provider-a, got %v", entry["provider"])
	}

	resB, err := sessionB.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"question": "isolation check", "use_cache": false},
	})
	if err != nil || resB.IsError {
		t.Fatalf("server B call failed: err=%v isError=%v content=%v", err, resB.IsError, resB.Content)
	}
	var outB map[string]any
	if err := json.Unmarshal([]byte(resB.Content[0].(*mcp.TextContent).Text), &outB); err != nil {
		t.Fatalf("unmarshal B: %v", err)
	}
	panelB, _ := outB["panel"].([]any)
	if len(panelB) != 1 {
		t.Fatalf("server B should see only its own 1 provider, got %d", len(panelB))
	}
	if entry := panelB[0].(map[string]any); entry["provider"] != "provider-b" {
		t.Errorf("server B panel entry should be provider-b, got %v", entry["provider"])
	}
}

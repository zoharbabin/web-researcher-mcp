package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// recordingTrialProvider captures the TrialSearchParams it was called with, so
// tests can assert on how the tool wires its input schema through to the
// provider without a live ClinicalTrials.gov call.
type recordingTrialProvider struct {
	got search.TrialSearchParams
}

func (m *recordingTrialProvider) Name() string { return "clinicaltrials" }
func (m *recordingTrialProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "recording clinicaltrials"}
}
func (m *recordingTrialProvider) Trials(_ context.Context, params search.TrialSearchParams) ([]search.TrialResult, error) {
	m.got = params
	return nil, nil
}

func callClinical(t *testing.T, deps Dependencies, args map[string]any) (map[string]any, bool) {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "clinical_search", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		return nil, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out, false
}

func TestClinicalSearchHappyPath(t *testing.T) {
	out, isErr := callClinical(t, setupTestDeps(), map[string]any{"condition": "covid-19", "num_results": 5})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
	if out["provider"] != "clinicaltrials" {
		t.Errorf("provider = %v, want clinicaltrials", out["provider"])
	}
	trials, ok := out["trials"].([]any)
	if !ok || len(trials) != 1 {
		t.Fatalf("expected 1 trial, got %v", out["trials"])
	}
	tr, _ := trials[0].(map[string]any)
	if tr["nctId"] != "NCT00000000" {
		t.Errorf("nctId = %v", tr["nctId"])
	}
	if tr["hasResults"] != true {
		t.Errorf("hasResults = %v, want true", tr["hasResults"])
	}
	if tr["url"] != "https://clinicaltrials.gov/study/NCT00000000" {
		t.Errorf("url = %v", tr["url"])
	}
}

func TestClinicalSearchRequiresAFacet(t *testing.T) {
	_, isErr := callClinical(t, setupTestDeps(), map[string]any{"num_results": 5})
	if !isErr {
		t.Error("a call with no query/condition/intervention/sponsor should be a tool error")
	}
}

func TestClinicalSearchUnknownProvider(t *testing.T) {
	out, isErr := callClinical(t, setupTestDeps(), map[string]any{"condition": "x", "provider": "nope"})
	if !isErr {
		// Unknown provider returns a structured error result (IsError true).
		t.Errorf("unknown provider should error, got %v", out)
	}
}

// TestClinicalSearchPhaseParamWired is the tool-layer regression test for
// #437: the input schema's phase field must reach search.TrialSearchParams.
func TestClinicalSearchPhaseParamWired(t *testing.T) {
	deps := setupTestDeps()
	trial := &recordingTrialProvider{}
	deps.TrialProviders = map[string]search.TrialProvider{trial.Name(): trial}

	_, isErr := callClinical(t, deps, map[string]any{"condition": "covid-19", "phase": "PHASE3"})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	if trial.got.Phase != "PHASE3" {
		t.Errorf("Phase = %q, want PHASE3", trial.got.Phase)
	}
}

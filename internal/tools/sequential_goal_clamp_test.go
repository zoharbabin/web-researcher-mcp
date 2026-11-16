package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSequentialSearch_DerivedGoalNeverSplitsCharacter: with no researchGoal,
// step 1 uses the first 200 bytes of searchStep as the goal. "x" shifts the cut
// to the middle of a 3-byte character.
func TestSequentialSearch_DerivedGoalNeverSplitsCharacter(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "x" + strings.Repeat("日", 100),
			"stepNumber":     float64(1),
			"nextStepNeeded": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	goal, _ := output["researchGoal"].(string)
	if goal == "" {
		t.Fatal("expected a derived researchGoal")
	}
	if !utf8.ValidString(goal) || len(goal) > 200 {
		t.Errorf("goal invalid or too long (%d bytes): %q", len(goal), goal)
	}
}

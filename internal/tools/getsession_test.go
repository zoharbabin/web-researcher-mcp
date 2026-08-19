package tools

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestGetSessionStepOutOfRangeReportsValidRange (#620): a stepId beyond what
// the session actually recorded must produce a message that says so and
// states the valid range — not the generic "not found or expired" string
// used for a truly missing/expired session. Collapsing the two made it
// impossible for a caller to tell "your session is fine, you just asked for
// a step it doesn't have" apart from "your whole session is gone".
func TestGetSessionStepOutOfRangeReportsValidRange(t *testing.T) {
	deps := setupTestDeps()
	sid, _ := startSession(t, deps, "")
	if sid == "" {
		t.Fatal("no session created")
	}

	_, res := callTool(t, deps, "get_research_session", map[string]any{"sessionId": sid, "stepId": 99})
	if !res.IsError {
		t.Fatal("expected an error result for an out-of-range stepId")
	}
	msg := res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(msg, "Session not found or expired") {
		t.Errorf("out-of-range stepId must not reuse the generic missing/expired message, got %q", msg)
	}
	if !strings.Contains(msg, "99") {
		t.Errorf("expected the requested stepId (99) in the message, got %q", msg)
	}
	if !strings.Contains(msg, "1") {
		t.Errorf("expected the valid step range (this session only has step 1) in the message, got %q", msg)
	}
}

// TestGetSessionUnknownIDStillReportsGenericMessage (#620): a session ID that
// was never created (or has expired) must keep the original generic message
// — only the valid-session/out-of-range-step case gets the new specific one.
func TestGetSessionUnknownIDStillReportsGenericMessage(t *testing.T) {
	deps := setupTestDeps()

	_, res := callTool(t, deps, "get_research_session", map[string]any{"sessionId": "nonexistent-session-id", "stepId": 1})
	if !res.IsError {
		t.Fatal("expected an error result for a nonexistent sessionId")
	}
	msg := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(msg, "Session not found or expired") {
		t.Errorf("expected the generic missing/expired message for a nonexistent session, got %q", msg)
	}
}

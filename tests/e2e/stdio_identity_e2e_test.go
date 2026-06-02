//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestSecurity_STDIO_UserIdentity proves the STDIO_USER_ID feature end-to-end over the
// real binary: with the var set (+ MEMORY_ENABLED) the per-user memory feature is
// reachable in STDIO (consent auto-granted, so memory_save succeeds and
// memory_recall returns the note); without the var the SAME build denies it
// (anonymous → fail-closed consent), proving the default behavior is unchanged.
func TestSecurity_STDIO_UserIdentity(t *testing.T) {
	parse := func(raw json.RawMessage) map[string]any {
		t.Helper()
		var res struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &res); err != nil || len(res.Content) == 0 {
			t.Fatalf("bad tool result: %v raw=%s", err, raw)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatalf("tool output not JSON: %v raw=%s", err, res.Content[0].Text)
		}
		return out
	}

	t.Run("with STDIO_USER_ID + memory enabled → memory usable", func(t *testing.T) {
		h := newProviderHarness(t, []string{
			"MEMORY_ENABLED=true",
			"STDIO_USER_ID=local-tester",
		})
		defer h.shutdown()
		h.initialize(t)

		save := parse(h.callTool(t, "memory_save", map[string]interface{}{
			"note":  "the answer is 42",
			"topic": "e2e",
		}))
		if save["status"] != "ok" {
			t.Fatalf("memory_save should succeed with STDIO_USER_ID set, got status=%v reason=%v", save["status"], save["reason"])
		}

		recall := parse(h.callTool(t, "memory_recall", map[string]interface{}{"topic": "e2e"}))
		if recall["status"] != "ok" {
			t.Fatalf("memory_recall should succeed, got %v", recall["status"])
		}
		// The recalled payload carries the user-asserted trust marker.
		if recall["trust"] != "user-asserted-content" {
			t.Errorf("recall trust = %v, want user-asserted-content", recall["trust"])
		}
		mems, _ := recall["memories"].([]any)
		if len(mems) == 0 {
			t.Fatal("expected the saved memory to be recalled")
		}
	})

	t.Run("without STDIO_USER_ID → memory denied (anonymous, unchanged default)", func(t *testing.T) {
		h := newProviderHarness(t, []string{
			"MEMORY_ENABLED=true",
			// no STDIO_USER_ID
		})
		defer h.shutdown()
		h.initialize(t)

		save := parse(h.callTool(t, "memory_save", map[string]interface{}{"note": "x"}))
		if save["status"] != "unavailable" {
			t.Fatalf("memory_save must deny for anonymous STDIO, got status=%v", save["status"])
		}
	})

	t.Run("STDIO_USER_ID never auto-grants workspace", func(t *testing.T) {
		h := newProviderHarness(t, []string{
			"WORKSPACES_ENABLED=true",
			"STDIO_USER_ID=local-tester",
		})
		defer h.shutdown()
		h.initialize(t)

		// Even with an identity, workspace is membership-gated (host-managed) and
		// never auto-granted — read must not return contributions.
		read := parse(h.callTool(t, "workspace_read", map[string]interface{}{"workspace_id": "ws1"}))
		if read["status"] == "ok" {
			t.Fatalf("workspace_read must not succeed via STDIO auto-grant, got %v", read)
		}
	})
}

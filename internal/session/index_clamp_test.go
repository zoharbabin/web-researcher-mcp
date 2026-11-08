package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBuildIndexFromSession_NeverSplitsCharacter covers the one-liner (120
// bytes) and summary (80 bytes) cuts. "日" is 3 bytes: 120 is a multiple of 3
// but 80 is not, and the leading "x" shifts the 120 cut inside a character.
func TestBuildIndexFromSession_NeverSplitsCharacter(t *testing.T) {
	t.Parallel()

	sess := &Session{
		ID:           "s1",
		ResearchGoal: "goal",
		Steps: []ResearchStep{
			{StepNumber: 1, Description: "x" + strings.Repeat("日", 100)},
		},
	}
	idx := buildIndexFromSession(sess)

	if len(idx.StepIndex) != 1 {
		t.Fatalf("expected 1 step index entry, got %d", len(idx.StepIndex))
	}
	if got := idx.StepIndex[0].OneLiner; !utf8.ValidString(got) || len(got) > 120 {
		t.Errorf("one-liner invalid or too long (%d bytes): %q", len(got), got)
	}
	if !utf8.ValidString(idx.Summary) {
		t.Errorf("summary is not valid UTF-8: %q", idx.Summary)
	}
}

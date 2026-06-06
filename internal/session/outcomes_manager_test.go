package session

import (
	"testing"
	"time"
)

func TestRecordOutcomeAndAggregateInIndex(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1", "u1")
	for i := 0; i < 3; i++ {
		if err := m.RecordOutcome("tenant-1", "u1", idx.ID, OutcomeEvent{
			Provider:  "searxng",
			Success:   false,
			ErrorKind: "rate_limited",
			URL:       "https://example.com/" + string(rune('a'+i)),
			Timestamp: time.Now().Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
	}

	got, ok := m.GetIndex("tenant-1", "u1", idx.ID)
	if !ok {
		t.Fatal("GetIndex failed")
	}
	if len(got.ErrorPatterns) != 1 {
		t.Fatalf("expected 1 error pattern, got %d", len(got.ErrorPatterns))
	}
	if got.ErrorPatterns[0].Kind != "rate_limited" || got.ErrorPatterns[0].Count != 3 {
		t.Errorf("unexpected pattern: %+v", got.ErrorPatterns[0])
	}
	if got.ProviderStats["searxng"].Attempts != 3 {
		t.Errorf("provider stats wrong: %+v", got.ProviderStats)
	}
}

func TestRecordOutcomeMissingSessionIsNoop(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()
	// Must not error or panic for an unknown session.
	if err := m.RecordOutcome("tenant-1", "u1", "nope", OutcomeEvent{Success: true}); err != nil {
		t.Errorf("missing session should be a silent no-op, got %v", err)
	}
}

func TestRecordOutcomeBelowThresholdNoPattern(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()
	idx, _ := m.Create("tenant-1", "u1")
	for i := 0; i < 2; i++ {
		_ = m.RecordOutcome("tenant-1", "u1", idx.ID, OutcomeEvent{ErrorKind: "auth_required"})
	}
	got, _ := m.GetIndex("tenant-1", "u1", idx.ID)
	if len(got.ErrorPatterns) != 0 {
		t.Errorf("2 errors should not surface a pattern, got %+v", got.ErrorPatterns)
	}
}

func TestRecordOutcomePreservesSummary(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()
	idx, _ := m.Create("tenant-1", "u1")
	_, _ = m.AppendStep("tenant-1", "u1", idx.ID, ResearchStep{StepNumber: 1, Description: "x"}, nil, "my custom summary")
	_ = m.RecordOutcome("tenant-1", "u1", idx.ID, OutcomeEvent{Provider: "p", Success: true})
	got, _ := m.GetIndex("tenant-1", "u1", idx.ID)
	if got.Summary != "my custom summary" {
		t.Errorf("RecordOutcome should preserve externally-set summary, got %q", got.Summary)
	}
}

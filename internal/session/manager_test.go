package session

import (
	"os"
	"testing"
	"time"
)

func newTestManager(ttl time.Duration, maxSessions int) *Manager {
	dir, _ := os.MkdirTemp("", "session-test-*")
	m, _ := NewManager(Config{
		MaxSessions:        maxSessions,
		MaxStepsPerSession: 200,
		SessionTTL:         ttl,
		DataDir:            dir,
	})
	return m
}

func TestCreateAndGet(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, err := m.Create("tenant-1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if idx.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if idx.TenantID != "tenant-1" {
		t.Errorf("expected TenantID=tenant-1, got %s", idx.TenantID)
	}

	got, ok := m.GetIndex("tenant-1", idx.ID)
	if !ok {
		t.Fatal("GetIndex returned not-found for existing session")
	}
	if got.ID != idx.ID {
		t.Errorf("expected ID=%s, got %s", idx.ID, got.ID)
	}
}

func TestGetNonExistent(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	_, ok := m.GetIndex("tenant-1", "nonexistent-id")
	if ok {
		t.Error("expected not-found for nonexistent session")
	}
}

func TestTenantIsolation(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idxA, _ := m.Create("tenant-A")
	idxB, _ := m.Create("tenant-B")

	_, ok := m.GetIndex("tenant-A", idxB.ID)
	if ok {
		t.Error("tenant-A should not be able to access tenant-B's session")
	}

	_, ok = m.GetIndex("tenant-B", idxA.ID)
	if ok {
		t.Error("tenant-B should not be able to access tenant-A's session")
	}

	_, ok = m.GetIndex("tenant-A", idxA.ID)
	if !ok {
		t.Error("tenant-A should be able to access their own session")
	}
	_, ok = m.GetIndex("tenant-B", idxB.ID)
	if !ok {
		t.Error("tenant-B should be able to access their own session")
	}
}

func TestAppendStep(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")

	step := ResearchStep{
		StepNumber:  1,
		Description: "Initial search for quantum computing papers",
		Reasoning:   "Starting broad to identify key themes",
		Confidence:  "medium",
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	updated, err := m.AppendStep("tenant-1", idx.ID, step, nil, "")
	if err != nil {
		t.Fatalf("AppendStep failed: %v", err)
	}
	if updated.StepCount != 1 {
		t.Errorf("expected 1 step, got %d", updated.StepCount)
	}
	if len(updated.LastSteps) != 1 {
		t.Errorf("expected 1 lastStep, got %d", len(updated.LastSteps))
	}
}

func TestAppendStepWithGap(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")

	step := ResearchStep{
		StepNumber:  1,
		Description: "Found papers but missing implementation details",
		Timestamp:   time.Now().Format(time.RFC3339),
	}
	gap := &KnowledgeGap{
		Description: "No concrete benchmarks found",
		FoundInStep: 1,
	}

	updated, err := m.AppendStep("tenant-1", idx.ID, step, gap, "")
	if err != nil {
		t.Fatalf("AppendStep failed: %v", err)
	}
	if len(updated.ActiveGaps) != 1 {
		t.Errorf("expected 1 gap, got %d", len(updated.ActiveGaps))
	}
}

func TestDelete(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")
	m.Delete("tenant-1", idx.ID)

	_, ok := m.GetIndex("tenant-1", idx.ID)
	if ok {
		t.Error("session should not exist after delete")
	}
}

func TestDeleteAll(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	m.Create("tenant-1")
	m.Create("tenant-2")
	m.Create("tenant-3")

	if m.ActiveCount() != 3 {
		t.Fatalf("expected 3 sessions, got %d", m.ActiveCount())
	}

	m.DeleteAll()

	if m.ActiveCount() != 0 {
		t.Errorf("expected 0 sessions after DeleteAll, got %d", m.ActiveCount())
	}
}

func TestTTLExpiry(t *testing.T) {
	m := newTestManager(50*time.Millisecond, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")

	_, ok := m.GetIndex("tenant-1", idx.ID)
	if !ok {
		t.Fatal("session should be accessible immediately after creation")
	}

	time.Sleep(100 * time.Millisecond)

	_, ok = m.GetIndex("tenant-1", idx.ID)
	if ok {
		t.Error("session should not be accessible after TTL expiry")
	}
}

func TestMaxSessionsEviction(t *testing.T) {
	m := newTestManager(5*time.Minute, 2)
	defer m.Close()

	idx1, _ := m.Create("tenant-1")
	time.Sleep(10 * time.Millisecond)
	_, _ = m.Create("tenant-1")

	idx3, _ := m.Create("tenant-1")

	_, ok := m.GetIndex("tenant-1", idx1.ID)
	if ok {
		t.Error("oldest session should have been evicted")
	}

	_, ok = m.GetIndex("tenant-1", idx3.ID)
	if !ok {
		t.Error("newest session should still exist")
	}
}

func TestMaxSessionsPerTenant(t *testing.T) {
	m := newTestManager(5*time.Minute, 2)
	defer m.Close()

	m.Create("tenant-A")
	m.Create("tenant-A")

	idxB, _ := m.Create("tenant-B")
	_, ok := m.GetIndex("tenant-B", idxB.ID)
	if !ok {
		t.Error("tenant-B session should exist regardless of tenant-A being at max")
	}
}

func TestActiveCount(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	if m.ActiveCount() != 0 {
		t.Errorf("expected 0, got %d", m.ActiveCount())
	}

	m.Create("tenant-1")
	m.Create("tenant-2")

	if m.ActiveCount() != 2 {
		t.Errorf("expected 2, got %d", m.ActiveCount())
	}
}

func TestSessionIDsAreUnique(t *testing.T) {
	m := newTestManager(5*time.Minute, 100)
	defer m.Close()

	ids := make(map[string]bool)
	for i := 0; i < 20; i++ {
		idx, _ := m.Create("tenant-1")
		if ids[idx.ID] {
			t.Fatalf("duplicate session ID: %s", idx.ID)
		}
		ids[idx.ID] = true
	}
}

func TestGetFull(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")
	m.SetResearchGoal("tenant-1", idx.ID, "Find quantum computing benchmarks")

	step := ResearchStep{
		StepNumber:  1,
		Description: "Searched arXiv for quantum benchmarks",
		Confidence:  "high",
		Timestamp:   time.Now().Format(time.RFC3339),
	}
	m.AppendStep("tenant-1", idx.ID, step, nil, "")

	sess, err := m.GetFull("tenant-1", idx.ID)
	if err != nil {
		t.Fatalf("GetFull failed: %v", err)
	}
	if sess.ResearchGoal != "Find quantum computing benchmarks" {
		t.Errorf("expected research goal, got %q", sess.ResearchGoal)
	}
	if len(sess.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(sess.Steps))
	}
}

func TestGetStep(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")

	for i := 1; i <= 3; i++ {
		step := ResearchStep{
			StepNumber:  i,
			Description: "Step " + string(rune('0'+i)),
			Timestamp:   time.Now().Format(time.RFC3339),
		}
		m.AppendStep("tenant-1", idx.ID, step, nil, "")
	}

	step, err := m.GetStep("tenant-1", idx.ID, 2)
	if err != nil {
		t.Fatalf("GetStep failed: %v", err)
	}
	if step.StepNumber != 2 {
		t.Errorf("expected step 2, got %d", step.StepNumber)
	}

	_, err = m.GetStep("tenant-1", idx.ID, 99)
	if err == nil {
		t.Error("expected error for nonexistent step")
	}
}

func TestMaxStepsEnforcement(t *testing.T) {
	dir, _ := os.MkdirTemp("", "session-test-*")
	m, _ := NewManager(Config{
		MaxSessions:        10,
		MaxStepsPerSession: 3,
		SessionTTL:         5 * time.Minute,
		DataDir:            dir,
	})
	defer m.Close()

	idx, _ := m.Create("tenant-1")

	for i := 1; i <= 3; i++ {
		step := ResearchStep{StepNumber: i, Description: "step", Timestamp: time.Now().Format(time.RFC3339)}
		m.AppendStep("tenant-1", idx.ID, step, nil, "")
	}

	step := ResearchStep{StepNumber: 4, Description: "one too many", Timestamp: time.Now().Format(time.RFC3339)}
	result, err := m.AppendStep("tenant-1", idx.ID, step, nil, "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Warning != "session_limit_reached" {
		t.Errorf("expected warning, got %q", result.Warning)
	}
	if result.StepCount != 3 {
		t.Errorf("step count should remain 3, got %d", result.StepCount)
	}
}

func TestDiskPersistence(t *testing.T) {
	dir, _ := os.MkdirTemp("", "session-test-*")

	// Create a session and close manager
	m1, _ := NewManager(Config{
		MaxSessions: 10,
		SessionTTL:  5 * time.Minute,
		DataDir:     dir,
	})
	idx, _ := m1.Create("tenant-1")
	m1.SetResearchGoal("tenant-1", idx.ID, "test persistence")
	step := ResearchStep{StepNumber: 1, Description: "persisted step", Timestamp: time.Now().Format(time.RFC3339)}
	m1.AppendStep("tenant-1", idx.ID, step, nil, "")
	m1.Close()

	// Create new manager from same dir — should rebuild from disk
	m2, _ := NewManager(Config{
		MaxSessions: 10,
		SessionTTL:  5 * time.Minute,
		DataDir:     dir,
	})
	defer m2.Close()

	got, ok := m2.GetIndex("tenant-1", idx.ID)
	if !ok {
		t.Fatal("session should survive manager restart")
	}
	if got.StepCount != 1 {
		t.Errorf("expected 1 step after rebuild, got %d", got.StepCount)
	}
	if got.ResearchGoal != "test persistence" {
		t.Errorf("expected research goal after rebuild, got %q", got.ResearchGoal)
	}
}

func TestDiskPersistenceWithEncryption(t *testing.T) {
	dir, _ := os.MkdirTemp("", "session-test-*")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	m1, _ := NewManager(Config{
		MaxSessions:   10,
		SessionTTL:    5 * time.Minute,
		DataDir:       dir,
		EncryptionKey: key,
	})
	idx, _ := m1.Create("tenant-1")
	step := ResearchStep{StepNumber: 1, Description: "encrypted step", Timestamp: time.Now().Format(time.RFC3339)}
	m1.AppendStep("tenant-1", idx.ID, step, nil, "")
	m1.Close()

	m2, _ := NewManager(Config{
		MaxSessions:   10,
		SessionTTL:    5 * time.Minute,
		DataDir:       dir,
		EncryptionKey: key,
	})
	defer m2.Close()

	got, ok := m2.GetIndex("tenant-1", idx.ID)
	if !ok {
		t.Fatal("encrypted session should survive restart")
	}
	if got.StepCount != 1 {
		t.Errorf("expected 1 step, got %d", got.StepCount)
	}
}

func TestResearchGoal(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")
	m.SetResearchGoal("tenant-1", idx.ID, "Understand LLM context management")

	got, _ := m.GetIndex("tenant-1", idx.ID)
	if got.ResearchGoal != "Understand LLM context management" {
		t.Errorf("expected research goal, got %q", got.ResearchGoal)
	}
}

func TestSummaryGeneration(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")
	m.SetResearchGoal("tenant-1", idx.ID, "Find best practices")

	step := ResearchStep{StepNumber: 1, Description: "Searched for patterns", Timestamp: time.Now().Format(time.RFC3339)}
	updated, _ := m.AppendStep("tenant-1", idx.ID, step, nil, "")

	if updated.Summary == "" {
		t.Error("expected auto-generated summary")
	}
}

func TestCustomSummary(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	idx, _ := m.Create("tenant-1")

	step := ResearchStep{StepNumber: 1, Description: "First step", Timestamp: time.Now().Format(time.RFC3339)}
	updated, _ := m.AppendStep("tenant-1", idx.ID, step, nil, "Custom summary provided by LLM")

	if updated.Summary != "Custom summary provided by LLM" {
		t.Errorf("expected custom summary, got %q", updated.Summary)
	}
}

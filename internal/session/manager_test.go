package session

import (
	"testing"
	"time"
)

func newTestManager(ttl time.Duration, maxSessions int) *Manager {
	m := NewManager(Config{
		MaxSessions: maxSessions,
		SessionTTL:  ttl,
	})
	return m
}

func TestCreateAndGet(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	sess, err := m.Create("tenant-1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if sess.TenantID != "tenant-1" {
		t.Errorf("expected TenantID=tenant-1, got %s", sess.TenantID)
	}

	got, ok := m.Get("tenant-1", sess.ID)
	if !ok {
		t.Fatal("Get returned not-found for existing session")
	}
	if got.ID != sess.ID {
		t.Errorf("expected ID=%s, got %s", sess.ID, got.ID)
	}
}

func TestGetNonExistent(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	_, ok := m.Get("tenant-1", "nonexistent-id")
	if ok {
		t.Error("expected not-found for nonexistent session")
	}
}

func TestTenantIsolation(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	sessA, _ := m.Create("tenant-A")
	sessB, _ := m.Create("tenant-B")

	// Tenant A cannot see tenant B's session
	_, ok := m.Get("tenant-A", sessB.ID)
	if ok {
		t.Error("tenant-A should not be able to access tenant-B's session")
	}

	// Tenant B cannot see tenant A's session
	_, ok = m.Get("tenant-B", sessA.ID)
	if ok {
		t.Error("tenant-B should not be able to access tenant-A's session")
	}

	// Each tenant can see their own
	_, ok = m.Get("tenant-A", sessA.ID)
	if !ok {
		t.Error("tenant-A should be able to access their own session")
	}
	_, ok = m.Get("tenant-B", sessB.ID)
	if !ok {
		t.Error("tenant-B should be able to access their own session")
	}
}

func TestUpdate(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	sess, _ := m.Create("tenant-1")
	originalLastUsed := sess.LastUsed

	// Small delay to ensure time difference
	time.Sleep(10 * time.Millisecond)

	sess.Steps = append(sess.Steps, ResearchStep{
		StepNumber:  1,
		Description: "Initial search",
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	m.Update("tenant-1", sess)

	got, ok := m.Get("tenant-1", sess.ID)
	if !ok {
		t.Fatal("session not found after update")
	}
	if len(got.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(got.Steps))
	}
	if !got.LastUsed.After(originalLastUsed) {
		t.Error("expected LastUsed to be updated")
	}
}

func TestDelete(t *testing.T) {
	m := newTestManager(5*time.Minute, 10)
	defer m.Close()

	sess, _ := m.Create("tenant-1")
	m.Delete("tenant-1", sess.ID)

	_, ok := m.Get("tenant-1", sess.ID)
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
	// Use a very short TTL for testing
	m := newTestManager(50*time.Millisecond, 10)
	defer m.Close()

	sess, _ := m.Create("tenant-1")

	// Should be accessible immediately
	_, ok := m.Get("tenant-1", sess.ID)
	if !ok {
		t.Fatal("session should be accessible immediately after creation")
	}

	// Wait for TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Should no longer be accessible
	_, ok = m.Get("tenant-1", sess.ID)
	if ok {
		t.Error("session should not be accessible after TTL expiry")
	}
}

func TestMaxSessionsEviction(t *testing.T) {
	m := newTestManager(5*time.Minute, 2)
	defer m.Close()

	// Create 2 sessions (at max)
	sess1, _ := m.Create("tenant-1")
	time.Sleep(10 * time.Millisecond) // Ensure different LastUsed
	_, _ = m.Create("tenant-1")

	// Creating a 3rd should evict the oldest (sess1)
	sess3, _ := m.Create("tenant-1")

	// sess1 should have been evicted
	_, ok := m.Get("tenant-1", sess1.ID)
	if ok {
		t.Error("oldest session should have been evicted")
	}

	// sess3 should still exist
	_, ok = m.Get("tenant-1", sess3.ID)
	if !ok {
		t.Error("newest session should still exist")
	}
}

func TestMaxSessionsPerTenant(t *testing.T) {
	m := newTestManager(5*time.Minute, 2)
	defer m.Close()

	// Create max sessions for tenant-A
	m.Create("tenant-A")
	m.Create("tenant-A")

	// Tenant-B should be unaffected
	sessB, _ := m.Create("tenant-B")
	_, ok := m.Get("tenant-B", sessB.ID)
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
		sess, _ := m.Create("tenant-1")
		if ids[sess.ID] {
			t.Fatalf("duplicate session ID: %s", sess.ID)
		}
		ids[sess.ID] = true
	}
}

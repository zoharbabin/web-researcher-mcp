package redisbackend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// testKey is a valid 64-hex AES-256 key for encryption parity in tests.
const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newTestBackend(t *testing.T) *Backends {
	t.Helper()
	mr := miniredis.RunT(t)
	b, err := Connect(context.Background(), Config{
		URL:           "redis://" + mr.Addr(),
		EncryptionKey: testKey,
		SessionTTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestConnectRequiresEncryptionKey(t *testing.T) {
	mr := miniredis.RunT(t)
	_, err := Connect(context.Background(), Config{URL: "redis://" + mr.Addr()})
	if err != ErrEncryptionRequired {
		t.Fatalf("expected ErrEncryptionRequired, got %v", err)
	}
}

func TestConnectFailFast(t *testing.T) {
	_, err := Connect(context.Background(), Config{
		URL:           "redis://127.0.0.1:1", // nothing listening
		EncryptionKey: testKey,
		DialTimeout:   500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected fail-fast error for unreachable Redis")
	}
}

func TestPersistStoreEncryptedRoundTrip(t *testing.T) {
	b := newTestBackend(t)
	s := b.PersistStore()
	ctx := context.Background()

	s.Set(ctx, "k1", []byte("secret-value"), time.Hour)
	got, ok := s.Get(ctx, "k1")
	if !ok || string(got) != "secret-value" {
		t.Fatalf("round-trip failed: ok=%v val=%q", ok, got)
	}

	// Stored bytes must be ciphertext, not plaintext (encryption-at-rest parity).
	raw, _ := b.client.Get(ctx, s.redisKey("k1")).Bytes()
	if string(raw) == "secret-value" {
		t.Error("value stored in plaintext — encryption-at-rest violated")
	}

	s.Delete(ctx, "k1")
	if _, ok := s.Get(ctx, "k1"); ok {
		t.Error("expected miss after delete")
	}
}

func TestIncrDailyAtomicAndExpires(t *testing.T) {
	b := newTestBackend(t)
	s := b.PersistStore()
	ctx := context.Background()
	resetAt := time.Now().Add(time.Hour)

	for i := int64(1); i <= 3; i++ {
		v, ok := s.IncrDaily(ctx, "tenant-1", resetAt)
		if !ok || v != i {
			t.Fatalf("expected count %d, got %d ok=%v", i, v, ok)
		}
	}
	// A different tenant has an independent counter.
	if v, _ := s.IncrDaily(ctx, "tenant-2", resetAt); v != 1 {
		t.Errorf("expected independent counter for tenant-2, got %d", v)
	}
}

// TestIncrDailyNoDoubleSpend simulates many concurrent pods hitting the same
// tenant counter; the atomic INCR must yield exactly N distinct values with no
// duplicates (no double-spend), which is the core #42 correctness property.
func TestIncrDailyNoDoubleSpend(t *testing.T) {
	b := newTestBackend(t)
	s := b.PersistStore()
	resetAt := time.Now().Add(time.Hour)

	const n = 200
	var wg sync.WaitGroup
	seen := make([]int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, _ := s.IncrDaily(context.Background(), "tenant-hot", resetAt)
			seen[idx] = v
		}(i)
	}
	wg.Wait()

	counts := map[int64]int{}
	for _, v := range seen {
		counts[v]++
	}
	for v, c := range counts {
		if c != 1 {
			t.Errorf("value %d returned %d times — double-spend under concurrency", v, c)
		}
	}
	if len(counts) != n {
		t.Errorf("expected %d distinct counter values, got %d", n, len(counts))
	}
}

func TestSessionManagerSurvivesAcrossClients(t *testing.T) {
	b := newTestBackend(t)
	// Two managers sharing one Redis simulate two pods.
	pod1 := b.SessionManager()
	pod2 := b.SessionManager()

	idx, err := pod1.Create("tenant-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := pod1.AppendStep("tenant-1", idx.ID, session.ResearchStep{StepNumber: 1, Description: "from pod1"}, nil, ""); err != nil {
		t.Fatalf("append: %v", err)
	}

	// pod2 (a "different pod") sees the session created on pod1.
	full, err := pod2.GetFull("tenant-1", idx.ID)
	if err != nil {
		t.Fatalf("pod2 GetFull: %v", err)
	}
	if len(full.Steps) != 1 || full.Steps[0].Description != "from pod1" {
		t.Errorf("pod2 did not see pod1's step: %+v", full.Steps)
	}
}

func TestSessionManagerNotFoundTyped(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()
	_, err := m.AppendStep("t1", "missing", session.ResearchStep{StepNumber: 5}, nil, "")
	var nf *session.SessionNotFoundError
	if !asSessionNotFound(err, &nf) || nf.LastKnownStep != 4 {
		t.Fatalf("expected typed SessionNotFoundError with LastKnownStep=4, got %v", err)
	}
}

func TestSessionListAndDeleteByTenant(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()
	a, _ := m.Create("tenant-1")
	_, _ = m.Create("tenant-1")
	_, _ = m.Create("tenant-2")

	if got := m.ListByTenant("tenant-1"); len(got) != 2 {
		t.Errorf("expected 2 sessions for tenant-1, got %d", len(got))
	}
	if n := m.DeleteByTenant("tenant-1"); n != 2 {
		t.Errorf("expected 2 deleted, got %d", n)
	}
	if got := m.ListByTenant("tenant-1"); len(got) != 0 {
		t.Errorf("expected tenant-1 empty after delete, got %d", len(got))
	}
	if got := m.ListByTenant("tenant-2"); len(got) != 1 {
		t.Errorf("tenant-2 must be untouched, got %d", len(got))
	}
	_ = a
}

// asSessionNotFound is a tiny errors.As helper kept local to avoid importing
// errors in the test for one call.
func asSessionNotFound(err error, target **session.SessionNotFoundError) bool {
	if e, ok := err.(*session.SessionNotFoundError); ok {
		*target = e
		return true
	}
	return false
}

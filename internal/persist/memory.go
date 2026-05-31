package persist

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is the zero-config in-memory Store implementation. State does not
// survive a process restart. It is the default when no encrypted disk directory
// is configured, preserving the existing pure-memory behavior of the
// subsystems that adopt persist.Store.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]memEntry
}

type memEntry struct {
	value     []byte
	expiresAt time.Time // zero means no expiry
}

// NewMemoryStore returns an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]memEntry)}
}

func (m *MemoryStore) Get(_ context.Context, key string) ([]byte, bool) {
	m.mu.RLock()
	e, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		m.mu.Lock()
		// Re-check under write lock to avoid deleting a value re-Set concurrently.
		if cur, ok := m.entries[key]; ok && !cur.expiresAt.IsZero() && time.Now().After(cur.expiresAt) {
			delete(m.entries, key)
		}
		m.mu.Unlock()
		return nil, false
	}
	// Return a copy so callers cannot mutate stored bytes.
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, true
}

func (m *MemoryStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) {
	stored := make([]byte, len(value))
	copy(stored, value)
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	m.mu.Lock()
	m.entries[key] = memEntry{value: stored, expiresAt: expiresAt}
	m.mu.Unlock()
}

func (m *MemoryStore) Delete(_ context.Context, key string) {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
}

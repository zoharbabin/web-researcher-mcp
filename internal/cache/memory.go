package cache

import (
	"context"
	"sync"
	"time"
)

type MemoryConfig struct {
	MaxSizeMB int
}

type memoryEntry struct {
	value     []byte
	storedAt  time.Time
	expiresAt time.Time
	ttl       time.Duration
}

type Memory struct {
	mu       sync.RWMutex
	entries  map[string]memoryEntry
	maxBytes int64
	size     int64
}

func NewMemory(cfg MemoryConfig) *Memory {
	if cfg.MaxSizeMB <= 0 {
		cfg.MaxSizeMB = 64
	}
	m := &Memory{
		entries:  make(map[string]memoryEntry),
		maxBytes: int64(cfg.MaxSizeMB) * 1024 * 1024,
	}
	go m.cleanup()
	return m
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.value, true
}

func (m *Memory) GetWithMeta(_ context.Context, key string) ([]byte, *EntryMeta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[key]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, nil, false
	}
	meta := &EntryMeta{StoredAt: entry.storedAt, TTL: entry.ttl}
	return entry.value, meta, true
}

func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entrySize := int64(len(key) + len(value))

	if existing, ok := m.entries[key]; ok {
		m.size -= int64(len(key) + len(existing.value))
	}

	// Evict if over capacity
	for m.size+entrySize > m.maxBytes && len(m.entries) > 0 {
		m.evictOne()
	}

	now := time.Now()
	m.entries[key] = memoryEntry{
		value:     value,
		storedAt:  now,
		expiresAt: now.Add(ttl),
		ttl:       ttl,
	}
	m.size += entrySize
}

func (m *Memory) Delete(_ context.Context, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.entries[key]; ok {
		m.size -= int64(len(key) + len(entry.value))
		delete(m.entries, key)
	}
}

func (m *Memory) Flush() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]memoryEntry)
	m.size = 0
}

func (m *Memory) Close() error {
	m.Flush()
	return nil
}

func (m *Memory) evictOne() {
	var oldestKey string
	var oldestTime time.Time

	for k, v := range m.entries {
		if oldestKey == "" || v.expiresAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.expiresAt
		}
	}

	if oldestKey != "" {
		entry := m.entries[oldestKey]
		m.size -= int64(len(oldestKey) + len(entry.value))
		delete(m.entries, oldestKey)
	}
}

func (m *Memory) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for k, v := range m.entries {
			if now.After(v.expiresAt) {
				m.size -= int64(len(k) + len(v.value))
				delete(m.entries, k)
			}
		}
		m.mu.Unlock()
	}
}

func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// --- Noop Cache Tests ---

func TestNoop_GetAlwaysMiss(t *testing.T) {
	c := NewNoop()
	ctx := context.Background()

	c.Set(ctx, "key1", []byte("value1"), time.Hour)

	val, ok := c.Get(ctx, "key1")
	if ok {
		t.Fatal("expected Noop.Get to return false, got true")
	}
	if val != nil {
		t.Fatalf("expected nil value, got %v", val)
	}
}

func TestNoop_DeleteAndClose(t *testing.T) {
	c := NewNoop()
	ctx := context.Background()

	// These should not panic
	c.Delete(ctx, "nonexistent")
	if err := c.Close(); err != nil {
		t.Fatalf("Noop.Close() returned error: %v", err)
	}
}

// --- Memory Cache Tests ---

func TestMemory_SetAndGet(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value []byte
	}{
		{"simple string", "hello", []byte("world")},
		{"empty value", "empty", []byte{}},
		{"binary data", "binary", []byte{0x00, 0xFF, 0xAB, 0xCD}},
		{"long key", "a-very-long-key-that-has-many-characters-in-it", []byte("short")},
	}

	m := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.Set(ctx, tt.key, tt.value, time.Hour)

			got, ok := m.Get(ctx, tt.key)
			if !ok {
				t.Fatal("expected hit, got miss")
			}
			if string(got) != string(tt.value) {
				t.Fatalf("expected %q, got %q", tt.value, got)
			}
		})
	}
}

func TestMemory_GetMiss(t *testing.T) {
	m := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ctx := context.Background()

	val, ok := m.Get(ctx, "nonexistent")
	if ok {
		t.Fatal("expected miss for nonexistent key")
	}
	if val != nil {
		t.Fatalf("expected nil, got %v", val)
	}
}

func TestMemory_TTLExpiry(t *testing.T) {
	m := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ctx := context.Background()

	m.Set(ctx, "ephemeral", []byte("data"), 50*time.Millisecond)

	// Should be present immediately
	val, ok := m.Get(ctx, "ephemeral")
	if !ok {
		t.Fatal("expected hit immediately after set")
	}
	if string(val) != "data" {
		t.Fatalf("expected %q, got %q", "data", val)
	}

	// Wait for TTL to expire
	time.Sleep(100 * time.Millisecond)

	_, ok = m.Get(ctx, "ephemeral")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestMemory_MaxBytesEviction(t *testing.T) {
	// MaxSizeMB=1 means the internal limit is actually computed
	// but we want a very small cache for testing eviction.
	// The struct uses maxBytes = MaxSizeMB * 1024 * 1024.
	// We'll construct directly for tighter control.
	m := &Memory{
		entries:  make(map[string]memoryEntry),
		maxBytes: 100, // 100 bytes total
	}
	ctx := context.Background()

	// Each entry: key length + value length counts toward size
	// "key0" = 4 bytes, value = 40 bytes => 44 bytes per entry
	m.Set(ctx, "key0", make([]byte, 40), time.Hour)
	m.Set(ctx, "key1", make([]byte, 40), 2*time.Hour)

	// At this point size = 88 bytes. Adding another 44-byte entry exceeds 100.
	m.Set(ctx, "key2", make([]byte, 40), 3*time.Hour)

	// key0 has the earliest expiry, so it should be evicted first
	_, ok := m.Get(ctx, "key0")
	if ok {
		t.Fatal("expected key0 to be evicted (earliest expiry)")
	}

	// key1 and key2 should still exist
	_, ok = m.Get(ctx, "key1")
	if !ok {
		t.Fatal("expected key1 to still be present")
	}
	_, ok = m.Get(ctx, "key2")
	if !ok {
		t.Fatal("expected key2 to still be present")
	}
}

func TestMemory_Delete(t *testing.T) {
	m := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ctx := context.Background()

	m.Set(ctx, "toDelete", []byte("value"), time.Hour)
	m.Delete(ctx, "toDelete")

	_, ok := m.Get(ctx, "toDelete")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestMemory_DeleteNonexistent(t *testing.T) {
	m := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ctx := context.Background()

	// Should not panic
	m.Delete(ctx, "nope")
}

func TestMemory_Close(t *testing.T) {
	m := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ctx := context.Background()

	m.Set(ctx, "k1", []byte("v1"), time.Hour)
	m.Set(ctx, "k2", []byte("v2"), time.Hour)

	if err := m.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if m.Len() != 0 {
		t.Fatalf("expected 0 entries after close, got %d", m.Len())
	}
}

func TestMemory_OverwriteExistingKey(t *testing.T) {
	m := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ctx := context.Background()

	m.Set(ctx, "k", []byte("first"), time.Hour)
	m.Set(ctx, "k", []byte("second"), time.Hour)

	got, ok := m.Get(ctx, "k")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(got) != "second" {
		t.Fatalf("expected %q, got %q", "second", got)
	}
}

// --- Disk Cache Tests ---

func TestDiskCache_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(DiskConfig{Dir: dir})
	ctx := context.Background()

	dc.Set(ctx, "disk-key", []byte("disk-value"), time.Hour)

	got, ok := dc.Get(ctx, "disk-key")
	if !ok {
		t.Fatal("expected hit from disk cache")
	}
	if string(got) != "disk-value" {
		t.Fatalf("expected %q, got %q", "disk-value", got)
	}
}

func TestDiskCache_GetMiss(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(DiskConfig{Dir: dir})
	ctx := context.Background()

	_, ok := dc.Get(ctx, "missing")
	if ok {
		t.Fatal("expected miss for nonexistent key")
	}
}

func TestDiskCache_TTLExpiry(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(DiskConfig{Dir: dir})
	ctx := context.Background()

	dc.Set(ctx, "temp", []byte("data"), 50*time.Millisecond)

	// Present immediately
	_, ok := dc.Get(ctx, "temp")
	if !ok {
		t.Fatal("expected hit immediately after set")
	}

	time.Sleep(100 * time.Millisecond)

	_, ok = dc.Get(ctx, "temp")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestDiskCache_Delete(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(DiskConfig{Dir: dir})
	ctx := context.Background()

	dc.Set(ctx, "del-key", []byte("del-val"), time.Hour)
	dc.Delete(ctx, "del-key")

	_, ok := dc.Get(ctx, "del-key")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestDiskCache_EncryptionRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Generate a valid 32-byte AES key (64 hex chars)
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}
	hexKey := hex.EncodeToString(keyBytes)

	dc := NewDiskCache(DiskConfig{
		Dir:           dir,
		EncryptionKey: hexKey,
	})
	ctx := context.Background()

	original := []byte("sensitive-data-that-must-be-encrypted")
	dc.Set(ctx, "secret", original, time.Hour)

	// Close persists entries to disk (encrypted)
	if err := dc.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Create a new instance with the same key and directory to read from file
	dc2 := NewDiskCache(DiskConfig{
		Dir:           dir,
		EncryptionKey: hexKey,
	})

	got, ok := dc2.Get(ctx, "secret")
	if !ok {
		t.Fatal("expected hit from file after close and reopen")
	}
	if string(got) != string(original) {
		t.Fatalf("expected %q, got %q", original, got)
	}
}

func TestDiskCache_EncryptionWrongKey(t *testing.T) {
	dir := t.TempDir()

	keyBytes1 := make([]byte, 32)
	if _, err := rand.Read(keyBytes1); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}
	hexKey1 := hex.EncodeToString(keyBytes1)

	dc := NewDiskCache(DiskConfig{
		Dir:           dir,
		EncryptionKey: hexKey1,
	})
	ctx := context.Background()

	dc.Set(ctx, "secret", []byte("classified"), time.Hour)
	if err := dc.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Open with a different key
	keyBytes2 := make([]byte, 32)
	if _, err := rand.Read(keyBytes2); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}
	hexKey2 := hex.EncodeToString(keyBytes2)

	dc2 := NewDiskCache(DiskConfig{
		Dir:           dir,
		EncryptionKey: hexKey2,
	})

	_, ok := dc2.Get(ctx, "secret")
	if ok {
		t.Fatal("expected miss when decrypting with wrong key")
	}
}

func TestDiskCache_CloseAndReopen(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(DiskConfig{Dir: dir})
	ctx := context.Background()

	dc.Set(ctx, "persist", []byte("persisted-value"), time.Hour)
	if err := dc.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Reopen from same directory
	dc2 := NewDiskCache(DiskConfig{Dir: dir})

	got, ok := dc2.Get(ctx, "persist")
	if !ok {
		t.Fatal("expected hit from file after close and reopen")
	}
	if string(got) != "persisted-value" {
		t.Fatalf("expected %q, got %q", "persisted-value", got)
	}
}

func TestDiskCache_Flush(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(DiskConfig{Dir: dir})
	ctx := context.Background()

	dc.Set(ctx, "f1", []byte("val1"), time.Hour)
	dc.Set(ctx, "f2", []byte("val2"), time.Hour)

	dc.Flush()

	_, ok1 := dc.Get(ctx, "f1")
	_, ok2 := dc.Get(ctx, "f2")
	if ok1 || ok2 {
		t.Fatal("expected all entries removed after flush")
	}
}

// --- Hybrid Cache Tests ---

func TestHybrid_L1Hit(t *testing.T) {
	dir := t.TempDir()
	h := NewHybrid(HybridConfig{
		Memory: MemoryConfig{MaxSizeMB: 1},
		Disk:   DiskConfig{Dir: dir},
	})
	ctx := context.Background()

	h.Set(ctx, "key", []byte("value"), time.Hour)

	got, ok := h.Get(ctx, "key")
	if !ok {
		t.Fatal("expected hit from hybrid cache")
	}
	if string(got) != "value" {
		t.Fatalf("expected %q, got %q", "value", got)
	}
}

func TestHybrid_L2HitWithPromotion(t *testing.T) {
	dir := t.TempDir()
	h := NewHybrid(HybridConfig{
		Memory: MemoryConfig{MaxSizeMB: 1},
		Disk:   DiskConfig{Dir: dir},
	})
	ctx := context.Background()

	// Set into both layers
	h.Set(ctx, "promote-key", []byte("promote-value"), time.Hour)

	// Remove from L1 only (memory) to simulate L1 miss
	h.memory.Delete(ctx, "promote-key")

	// Verify L1 miss
	_, ok := h.memory.Get(ctx, "promote-key")
	if ok {
		t.Fatal("expected L1 miss after manual delete from memory")
	}

	// Get from hybrid should hit L2 and promote to L1
	got, ok := h.Get(ctx, "promote-key")
	if !ok {
		t.Fatal("expected hit from L2 (disk)")
	}
	if string(got) != "promote-value" {
		t.Fatalf("expected %q, got %q", "promote-value", got)
	}

	// Verify promotion: L1 should now have it
	got, ok = h.memory.Get(ctx, "promote-key")
	if !ok {
		t.Fatal("expected L1 to have promoted entry")
	}
	if string(got) != "promote-value" {
		t.Fatalf("expected promoted value %q, got %q", "promote-value", got)
	}
}

func TestHybrid_MissFromBoth(t *testing.T) {
	dir := t.TempDir()
	h := NewHybrid(HybridConfig{
		Memory: MemoryConfig{MaxSizeMB: 1},
		Disk:   DiskConfig{Dir: dir},
	})
	ctx := context.Background()

	val, ok := h.Get(ctx, "nonexistent")
	if ok {
		t.Fatal("expected miss from both L1 and L2")
	}
	if val != nil {
		t.Fatalf("expected nil, got %v", val)
	}
}

func TestHybrid_Delete(t *testing.T) {
	dir := t.TempDir()
	h := NewHybrid(HybridConfig{
		Memory: MemoryConfig{MaxSizeMB: 1},
		Disk:   DiskConfig{Dir: dir},
	})
	ctx := context.Background()

	h.Set(ctx, "del", []byte("val"), time.Hour)
	h.Delete(ctx, "del")

	_, ok := h.Get(ctx, "del")
	if ok {
		t.Fatal("expected miss after delete from hybrid")
	}

	// Verify gone from both layers
	_, ok = h.memory.Get(ctx, "del")
	if ok {
		t.Fatal("expected miss from memory after hybrid delete")
	}
	_, ok = h.disk.Get(ctx, "del")
	if ok {
		t.Fatal("expected miss from disk after hybrid delete")
	}
}

func TestHybrid_Close(t *testing.T) {
	dir := t.TempDir()
	h := NewHybrid(HybridConfig{
		Memory: MemoryConfig{MaxSizeMB: 1},
		Disk:   DiskConfig{Dir: dir},
	})
	ctx := context.Background()

	h.Set(ctx, "ck", []byte("cv"), time.Hour)

	if err := h.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// After close, memory should be empty
	if h.memory.Len() != 0 {
		t.Fatal("expected memory to be empty after close")
	}
}

func TestHybrid_Flush(t *testing.T) {
	dir := t.TempDir()
	h := NewHybrid(HybridConfig{
		Memory: MemoryConfig{MaxSizeMB: 1},
		Disk:   DiskConfig{Dir: dir},
	})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		h.Set(ctx, fmt.Sprintf("k%d", i), []byte(fmt.Sprintf("v%d", i)), time.Hour)
	}

	h.Flush()

	for i := 0; i < 5; i++ {
		_, ok := h.Get(ctx, fmt.Sprintf("k%d", i))
		if ok {
			t.Fatalf("expected miss for k%d after flush", i)
		}
	}
}

// --- Interface compliance tests ---

func TestCacheInterfaceCompliance(t *testing.T) {
	dir := t.TempDir()
	caches := map[string]Cache{
		"Noop":   NewNoop(),
		"Memory": NewMemory(MemoryConfig{MaxSizeMB: 1}),
		"Disk":   NewDiskCache(DiskConfig{Dir: dir}),
		"Hybrid": NewHybrid(HybridConfig{
			Memory: MemoryConfig{MaxSizeMB: 1},
			Disk:   DiskConfig{Dir: t.TempDir()},
		}),
	}

	for name, c := range caches {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			// Just verify the interface methods don't panic
			c.Set(ctx, "interface-key", []byte("interface-value"), time.Minute)
			c.Get(ctx, "interface-key")
			c.Delete(ctx, "interface-key")
			if err := c.Close(); err != nil {
				t.Fatalf("%s.Close() returned error: %v", name, err)
			}
		})
	}
}

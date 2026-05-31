package cache

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"
)

// --- TenantAware Cache Tests ---

type tenantCtxKey struct{}

func TestTenantAware_Isolation(t *testing.T) {
	t.Parallel()
	inner := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ta := NewTenantAware(inner, func(ctx context.Context) string {
		if v := ctx.Value(tenantCtxKey{}); v != nil {
			return v.(string)
		}
		return "default"
	})

	ctxA := context.WithValue(context.Background(), tenantCtxKey{}, "tenant-a")
	ctxB := context.WithValue(context.Background(), tenantCtxKey{}, "tenant-b")

	ta.Set(ctxA, "key1", []byte("value-a"), time.Minute)
	ta.Set(ctxB, "key1", []byte("value-b"), time.Minute)

	valA, ok := ta.Get(ctxA, "key1")
	if !ok || string(valA) != "value-a" {
		t.Errorf("tenant-a expected 'value-a', got %q (ok=%v)", valA, ok)
	}

	valB, ok := ta.Get(ctxB, "key1")
	if !ok || string(valB) != "value-b" {
		t.Errorf("tenant-b expected 'value-b', got %q (ok=%v)", valB, ok)
	}
}

func TestTenantAware_DefaultTenantNoPrefix(t *testing.T) {
	t.Parallel()
	inner := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ta := NewTenantAware(inner, func(ctx context.Context) string { return "default" })
	ctx := context.Background()

	ta.Set(ctx, "mykey", []byte("myval"), time.Minute)

	// Should be accessible directly from inner (no prefix for "default")
	val, ok := inner.Get(ctx, "mykey")
	if !ok || string(val) != "myval" {
		t.Errorf("default tenant should not prefix keys, got ok=%v val=%q", ok, val)
	}
}

func TestTenantAware_DeleteScoped(t *testing.T) {
	t.Parallel()
	inner := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ta := NewTenantAware(inner, func(ctx context.Context) string {
		if v := ctx.Value(tenantCtxKey{}); v != nil {
			return v.(string)
		}
		return "default"
	})

	ctxA := context.WithValue(context.Background(), tenantCtxKey{}, "tenant-a")
	ctxB := context.WithValue(context.Background(), tenantCtxKey{}, "tenant-b")

	ta.Set(ctxA, "key1", []byte("a"), time.Minute)
	ta.Set(ctxB, "key1", []byte("b"), time.Minute)

	ta.Delete(ctxA, "key1")

	_, ok := ta.Get(ctxA, "key1")
	if ok {
		t.Error("tenant-a key should be deleted")
	}
	val, ok := ta.Get(ctxB, "key1")
	if !ok || string(val) != "b" {
		t.Error("tenant-b key should still exist")
	}
}

func TestTenantAware_GetWithMeta(t *testing.T) {
	t.Parallel()
	inner := NewMemory(MemoryConfig{MaxSizeMB: 1})
	ta := NewTenantAware(inner, func(ctx context.Context) string {
		if v := ctx.Value(tenantCtxKey{}); v != nil {
			return v.(string)
		}
		return "default"
	})

	ctx := context.WithValue(context.Background(), tenantCtxKey{}, "org-1")
	ta.Set(ctx, "k", []byte("v"), 5*time.Minute)

	val, meta, ok := ta.GetWithMeta(ctx, "k")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(val) != "v" {
		t.Errorf("expected 'v', got %q", val)
	}
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if meta.TTL != 5*time.Minute {
		t.Errorf("expected TTL 5m, got %v", meta.TTL)
	}
}

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

// --- Crypto helper tests (crypto.go) ---

func TestNewGCM(t *testing.T) {
	t.Parallel()

	// Empty key => nil AEAD, nil error (encryption disabled).
	gcm, err := NewGCM("")
	if err != nil || gcm != nil {
		t.Fatalf("empty key: expected (nil,nil), got (%v,%v)", gcm, err)
	}

	// Valid 64-hex key => usable AEAD.
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	gcm, err = NewGCM(hex.EncodeToString(keyBytes))
	if err != nil || gcm == nil {
		t.Fatalf("valid key: expected usable AEAD, got (%v,%v)", gcm, err)
	}

	// Non-hex => error.
	if _, err := NewGCM("not-hex-zzzz"); err == nil {
		t.Fatal("expected error for non-hex key")
	}

	// Wrong length (32 hex = 16 bytes, AES-128 not allowed here) => error.
	if _, err := NewGCM(hex.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestGCMRoundTripWithAAD(t *testing.T) {
	t.Parallel()
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	gcm, err := NewGCM(hex.EncodeToString(keyBytes))
	if err != nil {
		t.Fatalf("NewGCM: %v", err)
	}

	plaintext := []byte("secret payload")
	aad := []byte("cache-key-123")

	blob := GCMEncrypt(gcm, plaintext, aad)
	got, err := GCMDecrypt(gcm, blob, aad)
	if err != nil {
		t.Fatalf("GCMDecrypt with matching AAD: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("round-trip mismatch: got %q", got)
	}

	// Wrong AAD must fail authentication.
	if _, err := GCMDecrypt(gcm, blob, []byte("cache-key-999")); err == nil {
		t.Fatal("expected GCMDecrypt to fail with mismatched AAD")
	}

	// Truncated ciphertext => ErrShortCiphertext.
	if _, err := GCMDecrypt(gcm, []byte{0x00}, aad); err != ErrShortCiphertext {
		t.Fatalf("expected ErrShortCiphertext, got %v", err)
	}
}

// --- Disk cache AAD + key-rotation tests ---

func diskKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// TestDiskCache_AADBindsKey confirms a blob written for one key's file cannot be
// decrypted after being copied to another key's file (M7 swap guard).
func TestDiskCache_AADBindsKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := diskKey(t)
	ctx := context.Background()

	dc := NewDiskCache(DiskConfig{Dir: dir, EncryptionKey: key})
	dc.Set(ctx, "alpha", []byte("alpha-value"), time.Hour)
	dc.Set(ctx, "beta", []byte("beta-value"), time.Hour)
	if err := dc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	alphaFile := dc.filePath("alpha")
	betaFile := dc.filePath("beta")
	alphaBytes, err := os.ReadFile(alphaFile)
	if err != nil {
		t.Fatalf("read alpha: %v", err)
	}
	if err := os.WriteFile(betaFile, alphaBytes, 0600); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	// Fresh instance: reading "beta" reads alpha's blob (AAD="alpha") => reject.
	dc2 := NewDiskCache(DiskConfig{Dir: dir, EncryptionKey: key})
	if _, ok := dc2.Get(ctx, "beta"); ok {
		t.Fatal("expected AAD mismatch to reject swapped blob")
	}
}

// TestDiskCache_PrevKeyFallbackAndLazyReEncrypt verifies zero-downtime rotation
// (M1): a blob written under the old key is readable via prev-key fallback and
// re-encrypted under the current key so a new-key-only instance can read it.
func TestDiskCache_PrevKeyFallbackAndLazyReEncrypt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldKey := diskKey(t)
	newKey := diskKey(t)
	ctx := context.Background()

	dcOld := NewDiskCache(DiskConfig{Dir: dir, EncryptionKey: oldKey})
	dcOld.Set(ctx, "rotate", []byte("payload"), time.Hour)
	if err := dcOld.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Rotated config: new current key, old as prev. Reading lazily re-encrypts.
	// Reuse the same version so invalidateOnVersionChange does not wipe the dir.
	dcRot := NewDiskCache(DiskConfig{Dir: dir, EncryptionKey: newKey, EncryptionKeyPrev: oldKey})
	got, ok := dcRot.Get(ctx, "rotate")
	if !ok || string(got) != "payload" {
		t.Fatalf("expected prev-key fallback hit, got %q ok=%v", got, ok)
	}

	// New-key-only instance must read the re-encrypted file.
	dcNew := NewDiskCache(DiskConfig{Dir: dir, EncryptionKey: newKey})
	got2, ok := dcNew.Get(ctx, "rotate")
	if !ok || string(got2) != "payload" {
		t.Fatalf("expected re-encrypted file readable with new key only, got %q ok=%v", got2, ok)
	}
}

// TestDiskCache_VersionBumpInvalidates ensures a version sentinel change clears
// the directory (the format bump absorbs the AAD wire-format break cleanly).
func TestDiskCache_VersionBumpInvalidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := context.Background()

	dc := NewDiskCache(DiskConfig{Dir: dir, Version: "build-1"})
	dc.Set(ctx, "k", []byte("v"), time.Hour)
	if err := dc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen with a different version => file cleared.
	dc2 := NewDiskCache(DiskConfig{Dir: dir, Version: "build-2"})
	if _, ok := dc2.Get(ctx, "k"); ok {
		t.Fatal("expected entry cleared after version change")
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

// TestPrependExpiryHeader covers the bounds-checked expiry-prefix helper that
// replaced the `make([]byte, 8+len(data))` sites (CWE-190 guard).
func TestPrependExpiryHeader(t *testing.T) {
	t.Parallel()

	t.Run("round-trips data with expiry header", func(t *testing.T) {
		t.Parallel()
		data := []byte("hello world")
		exp := time.Unix(1893456000, 0) // 2030-01-01
		buf, err := PrependExpiryHeader(data, exp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(buf) != ExpiryHeaderSize+len(data) {
			t.Fatalf("len = %d, want %d", len(buf), ExpiryHeaderSize+len(data))
		}
		if got := int64(binary.BigEndian.Uint64(buf[:ExpiryHeaderSize])); got != exp.Unix() {
			t.Errorf("header = %d, want %d", got, exp.Unix())
		}
		if string(buf[ExpiryHeaderSize:]) != string(data) {
			t.Errorf("payload not preserved")
		}
	})

	t.Run("zero expiry writes zero header", func(t *testing.T) {
		t.Parallel()
		buf, err := PrependExpiryHeader([]byte("x"), time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if binary.BigEndian.Uint64(buf[:ExpiryHeaderSize]) != 0 {
			t.Errorf("expected zero header for zero expiry")
		}
	})

	t.Run("rejects oversized value (CWE-190 guard)", func(t *testing.T) {
		t.Parallel()
		// Don't actually allocate >256MiB; fake the length check by exceeding it.
		// A slice of MaxStoredValueBytes+1 zero bytes is large but bounded for CI.
		big := make([]byte, MaxStoredValueBytes+1)
		if _, err := PrependExpiryHeader(big, time.Now()); err == nil {
			t.Fatal("expected error for value exceeding MaxStoredValueBytes")
		}
	})
}

package persist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func randHexKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// stores returns one memory and one disk store for parity testing.
func stores(t *testing.T) map[string]Store {
	t.Helper()
	ds, err := NewDiskStore(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	return map[string]Store{
		"memory":         NewMemoryStore(),
		"disk-plaintext": ds,
		"disk-encrypted": mustDiskStore(t, t.TempDir(), randHexKey(t), ""),
	}
}

func mustDiskStore(t *testing.T, dir, key, prev string) *DiskStore {
	t.Helper()
	ds, err := NewDiskStore(dir, key, prev)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	return ds
}

// TestStoreParity asserts memory and disk implementations behave identically
// for the core Get/Set/Delete/TTL contract — no drift between local and cloud.
func TestStoreParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for name, s := range stores(t) {
		s := s
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Miss on absent key.
			if _, ok := s.Get(ctx, "absent"); ok {
				t.Fatal("expected miss on absent key")
			}

			// Set + Get round-trip.
			s.Set(ctx, "k", []byte("v"), time.Hour)
			got, ok := s.Get(ctx, "k")
			if !ok || string(got) != "v" {
				t.Fatalf("expected hit 'v', got %q ok=%v", got, ok)
			}

			// Delete.
			s.Delete(ctx, "k")
			if _, ok := s.Get(ctx, "k"); ok {
				t.Fatal("expected miss after delete")
			}

			// Delete absent is a no-op (no panic).
			s.Delete(ctx, "never-existed")

			// Zero/negative TTL => stored without expiry.
			s.Set(ctx, "noexp", []byte("forever"), 0)
			if got, ok := s.Get(ctx, "noexp"); !ok || string(got) != "forever" {
				t.Fatalf("expected no-expiry hit, got %q ok=%v", got, ok)
			}
		})
	}
}

func TestStoreTTLExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for name, s := range stores(t) {
		s := s
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s.Set(ctx, "ephemeral", []byte("data"), 200*time.Millisecond)
			if _, ok := s.Get(ctx, "ephemeral"); !ok {
				t.Fatal("expected hit immediately after set")
			}
			time.Sleep(300 * time.Millisecond)
			if _, ok := s.Get(ctx, "ephemeral"); ok {
				t.Fatal("expected miss after TTL expiry")
			}
		})
	}
}

// TestStoreReturnsCopy ensures mutating the returned slice does not corrupt
// stored bytes (both implementations defend against aliasing).
func TestStoreReturnsCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, s := range stores(t) {
		s := s
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s.Set(ctx, "k", []byte("original"), time.Hour)
			got, _ := s.Get(ctx, "k")
			got[0] = 'X'
			again, _ := s.Get(ctx, "k")
			if string(again) != "original" {
				t.Fatalf("stored bytes mutated via returned slice: %q", again)
			}
		})
	}
}

// TestDiskStoreSurvivesRestart verifies persisted state is readable by a fresh
// instance pointed at the same directory and key (the H2/H7 durability need).
func TestDiskStoreSurvivesRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	key := randHexKey(t)

	s1 := mustDiskStore(t, dir, key, "")
	s1.Set(ctx, "revoked-token", []byte("1"), time.Hour)

	// Simulated restart: new instance, same dir+key, empty index.
	s2 := mustDiskStore(t, dir, key, "")
	got, ok := s2.Get(ctx, "revoked-token")
	if !ok || string(got) != "1" {
		t.Fatalf("expected persisted value after restart, got %q ok=%v", got, ok)
	}
}

func TestDiskStoreExpiredFileNotReturnedAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	key := randHexKey(t)

	s1 := mustDiskStore(t, dir, key, "")
	s1.Set(ctx, "short", []byte("x"), 100*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	s2 := mustDiskStore(t, dir, key, "")
	if _, ok := s2.Get(ctx, "short"); ok {
		t.Fatal("expected expired entry to be a miss after restart")
	}
}

// TestDiskStoreWrongKeyFails confirms a different key cannot decrypt persisted
// blobs (no silent plaintext leak).
func TestDiskStoreWrongKeyFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	s1 := mustDiskStore(t, dir, randHexKey(t), "")
	s1.Set(ctx, "secret", []byte("classified"), time.Hour)

	s2 := mustDiskStore(t, dir, randHexKey(t), "")
	if _, ok := s2.Get(ctx, "secret"); ok {
		t.Fatal("expected miss when decrypting with wrong key")
	}
}

// TestDiskStorePrevKeyFallbackAndLazyReEncrypt verifies zero-downtime rotation:
// data written under the old key is readable via the prev-key fallback and then
// re-encrypted under the new key (so a fresh instance with only the new key can
// read it).
func TestDiskStorePrevKeyFallbackAndLazyReEncrypt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	oldKey := randHexKey(t)
	newKey := randHexKey(t)

	// Write under the old key.
	sOld := mustDiskStore(t, dir, oldKey, "")
	sOld.Set(ctx, "rotate", []byte("payload"), time.Hour)

	// Open with new current key + old as prev: read triggers lazy re-encrypt.
	sRot := mustDiskStore(t, dir, newKey, oldKey)
	got, ok := sRot.Get(ctx, "rotate")
	if !ok || string(got) != "payload" {
		t.Fatalf("expected prev-key fallback hit, got %q ok=%v", got, ok)
	}

	// A fresh instance with ONLY the new key must now read the re-encrypted file.
	sNew := mustDiskStore(t, dir, newKey, "")
	got2, ok := sNew.Get(ctx, "rotate")
	if !ok || string(got2) != "payload" {
		t.Fatalf("expected re-encrypted file readable with new key only, got %q ok=%v", got2, ok)
	}
}

// TestDiskStoreAADBindsKey confirms a ciphertext blob written for one key cannot
// be decrypted when copied to another key's file (AAD swap guard).
func TestDiskStoreAADBindsKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	key := randHexKey(t)

	s := mustDiskStore(t, dir, key, "")
	s.Set(ctx, "alpha", []byte("alpha-value"), time.Hour)
	s.Set(ctx, "beta", []byte("beta-value"), time.Hour)

	// Swap the two files on disk.
	alphaFile := s.filePath("alpha")
	betaFile := s.filePath("beta")
	alphaBytes, err := os.ReadFile(alphaFile)
	if err != nil {
		t.Fatalf("read alpha: %v", err)
	}
	if err := os.WriteFile(betaFile, alphaBytes, 0600); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	// Fresh instance (empty index) reading "beta" must fail: the blob's AAD is
	// "alpha", which won't authenticate against key "beta".
	s2 := mustDiskStore(t, dir, key, "")
	if _, ok := s2.Get(ctx, "beta"); ok {
		t.Fatal("expected AAD mismatch to reject swapped blob")
	}
}

func TestDiskStoreFilePerms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	s := mustDiskStore(t, dir, randHexKey(t), "")
	s.Set(ctx, "k", []byte("v"), time.Hour)

	fi, err := os.Stat(s.filePath("k"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600 file perms, got %o", perm)
	}
}

func TestDiskStoreCleanOrphans(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := mustDiskStore(t, dir, "", "")

	orphan := filepath.Join(dir, ".persist-orphan.tmp")
	if err := os.WriteFile(orphan, []byte("junk"), 0600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	s.CleanOrphans()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("expected orphan removed, err=%v", err)
	}
}

func TestDiskStoreConcurrentAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := mustDiskStore(t, t.TempDir(), randHexKey(t), "")

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			key := "k"
			for j := 0; j < 50; j++ {
				s.Set(ctx, key, []byte("v"), time.Minute)
				s.Get(ctx, key)
				if j%10 == 0 {
					s.Delete(ctx, key)
				}
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

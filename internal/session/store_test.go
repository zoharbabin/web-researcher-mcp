package session

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testKey  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testKey2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func newSession(tenant, id string) *Session {
	return &Session{
		ID:           id,
		TenantID:     tenant,
		ResearchGoal: "round-trip goal",
		CreatedAt:    time.Now(),
		LastUsed:     time.Now(),
		Steps: []ResearchStep{
			{StepNumber: 1, Description: "first step", Timestamp: time.Now().Format(time.RFC3339)},
		},
	}
}

func TestStoreRoundTripEncrypted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := NewStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.gcm == nil {
		t.Fatal("expected encryption enabled with valid key")
	}

	key := "tenant-1:sess-abc"
	if err := s.Save(key, newSession("tenant-1", "sess-abc"), time.Hour); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load(key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != "sess-abc" || got.TenantID != "tenant-1" {
		t.Errorf("unexpected session: %+v", got)
	}
	if len(got.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(got.Steps))
	}
}

// TestStoreSaveSelfHealsMissingDir is the regression guard for the live-test
// finding: if the store directory is evicted/removed after construction (e.g.
// macOS cleaning ~/Library/Caches mid-run), Save must re-create it instead of
// failing with a cryptic ENOENT that hard-blocks the whole session workflow.
func TestStoreSaveSelfHealsMissingDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sessions")
	s, err := NewStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Simulate the OS evicting the cache directory out from under a long-lived
	// server after startup.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	key := "tenant-1:sess-heal"
	if err := s.Save(key, newSession("tenant-1", "sess-heal"), time.Hour); err != nil {
		t.Fatalf("Save should self-heal a missing dir, got: %v", err)
	}
	got, err := s.Load(key)
	if err != nil {
		t.Fatalf("Load after self-heal: %v", err)
	}
	if got.ID != "sess-heal" {
		t.Errorf("unexpected session after self-heal: %+v", got)
	}
}

func TestStoreRoundTripPlaintext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.gcm != nil {
		t.Fatal("expected encryption disabled with empty key")
	}

	key := "tenant-1:sess-plain"
	if err := s.Save(key, newSession("tenant-1", "sess-plain"), time.Hour); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load(key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != "sess-plain" {
		t.Errorf("unexpected id %q", got.ID)
	}
}

// TestStoreAADBlobSwapFails verifies the M7 AAD binding: a ciphertext written
// for one key cannot be moved onto another key's file and decrypted, because
// GCM authenticates the filename-hash AAD which differs per key.
func TestStoreAADBlobSwapFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := NewStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	keyA := "tenant-1:sess-A"
	keyB := "tenant-1:sess-B"
	if err := s.Save(keyA, newSession("tenant-1", "sess-A"), time.Hour); err != nil {
		t.Fatalf("Save A: %v", err)
	}

	// Copy A's on-disk blob onto B's filename. The expiry prefix is valid; only
	// the AAD differs, so Open must fail (corrupt).
	blob, err := os.ReadFile(s.filePath(keyA))
	if err != nil {
		t.Fatalf("read A file: %v", err)
	}
	if err := os.WriteFile(s.filePath(keyB), blob, 0600); err != nil {
		t.Fatalf("write B file: %v", err)
	}

	if _, err := s.Load(keyB); err != ErrCorrupt {
		t.Errorf("expected ErrCorrupt for AAD-mismatched blob swap, got %v", err)
	}
}

// TestStoreCrossKeyFails verifies a blob sealed under one key cannot be opened
// under an unrelated key (no prev-key configured).
func TestStoreCrossKeyFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s1, _ := NewStore(dir, testKey)
	key := "tenant-1:sess-x"
	if err := s1.Save(key, newSession("tenant-1", "sess-x"), time.Hour); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Fresh store over the same dir using a different (unrelated) key, no prev.
	s2, _ := NewStore(dir, testKey2)
	if _, err := s2.Load(key); err != ErrCorrupt {
		t.Errorf("expected ErrCorrupt loading under wrong key, got %v", err)
	}
}

// TestStorePrevKeyLazyReencrypt verifies M1: a blob written under the previous
// key is decryptable via the prev-key fallback and is lazily re-encrypted under
// the current key on read (so the prev key is no longer required afterwards).
func TestStorePrevKeyLazyReencrypt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write under the old key only.
	old, _ := NewStore(dir, testKey)
	key := "tenant-1:sess-rot"
	if err := old.Save(key, newSession("tenant-1", "sess-rot"), time.Hour); err != nil {
		t.Fatalf("Save under old key: %v", err)
	}

	// Rotate: current = testKey2, prev = testKey.
	rotated, _ := NewStoreWithPrev(dir, testKey2, testKey)
	got, err := rotated.Load(key)
	if err != nil {
		t.Fatalf("Load after rotation: %v", err)
	}
	if got.ID != "sess-rot" {
		t.Errorf("unexpected id %q", got.ID)
	}

	// After lazy re-encrypt, a store with ONLY the current key (no prev) must
	// read it cleanly.
	currentOnly, _ := NewStore(dir, testKey2)
	got2, err := currentOnly.Load(key)
	if err != nil {
		t.Fatalf("Load with current key only after re-encrypt: %v", err)
	}
	if got2.ID != "sess-rot" {
		t.Errorf("unexpected id after re-encrypt %q", got2.ID)
	}
}

// TestStorePrevKeyNoMatchFails verifies that when neither current nor previous
// key opens the blob, Load fails (no silent acceptance).
func TestStorePrevKeyNoMatchFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old, _ := NewStore(dir, testKey)
	key := "tenant-1:sess-nomatch"
	old.Save(key, newSession("tenant-1", "sess-nomatch"), time.Hour)

	// current and prev are both unrelated to the writing key.
	other := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	s, _ := NewStoreWithPrev(dir, testKey2, other)
	if _, err := s.Load(key); err != ErrCorrupt {
		t.Errorf("expected ErrCorrupt when no key matches, got %v", err)
	}
}

// TestLoadVsLoadFileAADConsistency is the cross-path AAD consistency guard from
// the test strategy: store.Load (which derives AAD from the key) and the
// rebuild path store.loadFile (which derives AAD from the filename hash) must
// decrypt the same blob identically. If the two AAD derivations ever drift,
// loadFile would fail to open a blob that Load opens.
func TestLoadVsLoadFileAADConsistency(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _ := NewStore(dir, testKey)

	key := "tenant-42:sess-consistency"
	if err := s.Save(key, newSession("tenant-42", "sess-consistency"), time.Hour); err != nil {
		t.Fatalf("Save: %v", err)
	}

	viaLoad, err := s.Load(key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	hash := fileHash(key)
	fp := filepath.Join(dir, hash+".session")
	viaFile, _, _, usedPrev, err := s.loadFile(fp, hash)
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if usedPrev {
		t.Error("did not expect prev-key usage with a single key")
	}
	if viaFile.ID != viaLoad.ID || viaFile.TenantID != viaLoad.TenantID {
		t.Errorf("loadFile/%+v disagrees with Load/%+v", viaFile, viaLoad)
	}
}

// TestLoadFilePrevKeyConsistency verifies loadFile honors the prev-key fallback
// with the hash-derived AAD, matching Load's behavior on the same rotated blob.
func TestLoadFilePrevKeyConsistency(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old, _ := NewStore(dir, testKey)
	key := "tenant-1:sess-rebuild-rot"
	old.Save(key, newSession("tenant-1", "sess-rebuild-rot"), time.Hour)

	rotated, _ := NewStoreWithPrev(dir, testKey2, testKey)
	hash := fileHash(key)
	fp := filepath.Join(dir, hash+".session")
	sess, _, _, usedPrev, err := rotated.loadFile(fp, hash)
	if err != nil {
		t.Fatalf("loadFile after rotation: %v", err)
	}
	if !usedPrev {
		t.Error("expected prev-key usage on rotated blob")
	}
	if sess.ID != "sess-rebuild-rot" {
		t.Errorf("unexpected id %q", sess.ID)
	}
}

// TestStoreFilePermissions verifies M3: session files are written 0600.
func TestStoreFilePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _ := NewStore(dir, testKey)
	key := "tenant-1:sess-perm"
	if err := s.Save(key, newSession("tenant-1", "sess-perm"), time.Hour); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(s.filePath(key))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}
}

// TestStoreTruncatedFileCorrupt verifies a file shorter than the 8-byte expiry
// prefix is reported corrupt, never panicking.
func TestStoreTruncatedFileCorrupt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _ := NewStore(dir, testKey)
	key := "tenant-1:sess-trunc"
	if err := os.WriteFile(s.filePath(key), []byte{1, 2, 3}, 0600); err != nil {
		t.Fatalf("write truncated: %v", err)
	}
	if _, err := s.Load(key); err != ErrCorrupt {
		t.Errorf("expected ErrCorrupt for truncated file, got %v", err)
	}
}

// TestStoreExpiryPrefixPreservedOnReencrypt verifies the lazy re-encrypt path
// preserves the original expiry window rather than resetting it.
func TestStoreExpiryPrefixPreservedOnReencrypt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old, _ := NewStore(dir, testKey)
	key := "tenant-1:sess-expiry"
	old.Save(key, newSession("tenant-1", "sess-expiry"), time.Hour)

	before, _ := os.ReadFile(old.filePath(key))
	origExpiry := int64(binary.BigEndian.Uint64(before[:8]))

	rotated, _ := NewStoreWithPrev(dir, testKey2, testKey)
	if _, err := rotated.Load(key); err != nil {
		t.Fatalf("Load: %v", err)
	}

	after, _ := os.ReadFile(rotated.filePath(key))
	newExpiry := int64(binary.BigEndian.Uint64(after[:8]))
	if origExpiry != newExpiry {
		t.Errorf("expiry changed on re-encrypt: orig=%d new=%d", origExpiry, newExpiry)
	}
}

// TestConcurrentSaveLoadDelete drives the encrypted session store under true
// goroutine contention: many goroutines Save/Load/Delete across a mix of shared
// and unique keys. Its purpose under `go test -race` is to prove the AES-GCM
// encrypt/decrypt path, the AAD derivation, and the atomic temp-file+rename
// writes are race-free when sessions are persisted concurrently (the realistic
// multi-tenant load). Bounded (sub-second), so it runs on every CI run rather
// than as a separate costly load test.
func TestConcurrentSaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, testKey) // encrypted store exercises the GCM+AAD path
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	const goroutines = 12
	const perG = 60
	done := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perG; i++ {
				// Mix shared keys (contended read/write/decrypt on the same
				// blob) with unique keys (independent files).
				keys := []string{
					"tenant-1:shared",
					fmt.Sprintf("tenant-%d:sess-%d", g, i),
				}
				for _, key := range keys {
					_ = s.Save(key, newSession("tenant", key), time.Hour)
					if loaded, err := s.Load(key); err == nil && loaded != nil {
						// Decrypt round-trip succeeded; AAD matched.
						_ = loaded.ID
					}
					if i%15 == 0 {
						_ = s.Delete(key)
					}
				}
			}
		}(g)
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// Final sanity: a fresh Save/Load round-trips correctly after the storm,
	// proving the store is not left in a corrupted state.
	if err := s.Save("tenant-1:final", newSession("tenant-1", "final"), time.Hour); err != nil {
		t.Fatalf("post-contention Save failed: %v", err)
	}
	got, err := s.Load("tenant-1:final")
	if err != nil || got == nil {
		t.Fatalf("post-contention Load failed: %v", err)
	}
}

package persist

import (
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

const diskFileSuffix = ".persist"

// DiskStore is an encrypted-disk-backed Store. It generalizes the session
// store's proven pattern (AES-256-GCM, atomic temp-file+rename writes, 0600
// perms, an 8-byte big-endian expiry prefix, SHA-256-hashed filenames) into the
// reusable persist.Store contract. Each blob's GCM additional authenticated
// data is the raw key, binding ciphertext to its file (cross-path swap guard).
//
// An in-memory index mirrors live entries for fast reads; on a cold start the
// store reconstructs nothing eagerly — Get falls back to reading the file when
// the index misses, so state survives restarts transparently.
type DiskStore struct {
	mu      sync.RWMutex
	dir     string
	gcm     cipher.AEAD // current key; nil when encryption disabled
	gcmPrev cipher.AEAD // previous key for rotation fallback; nil when unset
	index   map[string]diskIndexEntry
}

type diskIndexEntry struct {
	value     []byte
	expiresAt time.Time // zero means no expiry
}

// NewDiskStore creates (or opens) an encrypted disk store rooted at dir. An
// empty dir uses a fresh temp directory. encryptionKey / encryptionKeyPrev are
// 64-hex AES-256 keys; an empty current key disables encryption (plaintext on
// disk), and a previous key enables zero-downtime rotation with lazy
// re-encryption on read. A malformed key falls back to plaintext rather than
// failing construction, matching the cache/session tolerance (config validation
// rejects bad keys upstream).
func NewDiskStore(dir, encryptionKey, encryptionKeyPrev string) (*DiskStore, error) {
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "web-researcher-persist-*")
		if err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	s := &DiskStore{dir: dir, index: make(map[string]diskIndexEntry)}
	// Best-effort GCM: ignore errors and fall back to plaintext to mirror the
	// disk cache / session store behavior.
	s.gcm, _ = cache.NewGCM(encryptionKey)
	s.gcmPrev, _ = cache.NewGCM(encryptionKeyPrev)
	return s, nil
}

func (s *DiskStore) filePath(key string) string {
	h := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(h[:])+diskFileSuffix)
}

func (s *DiskStore) aad(key string) []byte { return []byte(key) }

func (s *DiskStore) Get(_ context.Context, key string) ([]byte, bool) {
	now := time.Now()

	s.mu.RLock()
	e, ok := s.index[key]
	s.mu.RUnlock()
	if ok {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			s.Delete(context.Background(), key)
			return nil, false
		}
		out := make([]byte, len(e.value))
		copy(out, e.value)
		return out, true
	}

	// Index miss: read from file (cold start after restart).
	return s.getFromFile(key, now)
}

func (s *DiskStore) getFromFile(key string, now time.Time) ([]byte, bool) {
	fp := s.filePath(key)
	data, err := os.ReadFile(fp)
	if err != nil || len(data) < 8 {
		return nil, false
	}

	expiresUnix := binary.BigEndian.Uint64(data[:8])
	var expiresAt time.Time
	if expiresUnix != 0 {
		expiresAt = time.Unix(int64(expiresUnix), 0)
		if now.After(expiresAt) {
			_ = os.Remove(fp)
			return nil, false
		}
	}

	payload := data[8:]
	if s.gcm == nil {
		s.hydrate(key, payload, expiresAt)
		return payload, true
	}

	aad := s.aad(key)
	if decrypted, derr := cache.GCMDecrypt(s.gcm, payload, aad); derr == nil {
		s.hydrate(key, decrypted, expiresAt)
		return decrypted, true
	}
	if s.gcmPrev != nil {
		if decrypted, derr := cache.GCMDecrypt(s.gcmPrev, payload, aad); derr == nil {
			// Lazy re-encrypt with the current key on first read after rotation.
			s.writeFile(key, decrypted, expiresAt)
			s.hydrate(key, decrypted, expiresAt)
			return decrypted, true
		}
	}
	return nil, false
}

func (s *DiskStore) hydrate(key string, value []byte, expiresAt time.Time) {
	stored := make([]byte, len(value))
	copy(stored, value)
	s.mu.Lock()
	s.index[key] = diskIndexEntry{value: stored, expiresAt: expiresAt}
	s.mu.Unlock()
}

func (s *DiskStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) {
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	s.hydrate(key, value, expiresAt)
	s.writeFile(key, value, expiresAt)
}

// writeFile atomically persists value to disk with an 8-byte expiry prefix.
func (s *DiskStore) writeFile(key string, value []byte, expiresAt time.Time) {
	data := value
	if s.gcm != nil {
		data = cache.GCMEncrypt(s.gcm, data, s.aad(key))
	}

	buf := make([]byte, 8+len(data))
	if !expiresAt.IsZero() {
		binary.BigEndian.PutUint64(buf[:8], uint64(expiresAt.Unix()))
	}
	copy(buf[8:], data)

	tmp, err := os.CreateTemp(s.dir, ".persist-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	_ = os.Rename(tmpName, s.filePath(key))
}

func (s *DiskStore) Delete(_ context.Context, key string) {
	s.mu.Lock()
	delete(s.index, key)
	s.mu.Unlock()
	_ = os.Remove(s.filePath(key))
}

// CleanOrphans removes leftover temp files from interrupted writes. Optional
// maintenance; safe to call at startup.
func (s *DiskStore) CleanOrphans() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

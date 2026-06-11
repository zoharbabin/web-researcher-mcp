package session

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

var ErrCorrupt = errors.New("session file corrupt or unreadable")

type Store struct {
	dir string
	// gcm is the current encryption key (nil when encryption disabled).
	gcm cipher.AEAD
	// gcmPrev is an optional previous key for zero-downtime rotation: a blob
	// that fails to open under the current key is retried under this one and
	// lazily re-encrypted with the current key on read (M1).
	gcmPrev cipher.AEAD
}

func NewStore(dir, encryptionKey string) (*Store, error) {
	return NewStoreWithPrev(dir, encryptionKey, "")
}

// NewStoreWithPrev builds a session store with an optional previous encryption
// key for zero-downtime rotation. Both keys are 64-hex AES-256 keys; an empty
// key disables that slot. A malformed key falls back to plaintext for the
// current key (matching prior best-effort behavior; config validation rejects
// non-64-hex keys upstream) and disables rotation fallback for the previous key.
func NewStoreWithPrev(dir, encryptionKey, encryptionKeyPrev string) (*Store, error) {
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "web-researcher-sessions-*")
		if err != nil {
			return nil, err
		}
	} else {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}

	s := &Store{dir: dir}

	// Reuse the shared cache GCM helper so the disk cache, persist store, and
	// session store all derive AES-256-GCM identically (no duplicated setup).
	if gcm, err := cache.NewGCM(encryptionKey); err == nil {
		s.gcm = gcm
	}
	if gcm, err := cache.NewGCM(encryptionKeyPrev); err == nil {
		s.gcmPrev = gcm
	}

	return s, nil
}

func (s *Store) Save(key string, sess *Session, ttl time.Duration) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}

	if s.gcm != nil {
		// Bind the ciphertext to the on-disk filename hash (M7) so a blob
		// written for one key cannot be moved to another key's file and
		// decrypted. Save/Load and manager.readSessionFile all derive this
		// same AAD from the filename hash.
		data = cache.GCMEncrypt(s.gcm, data, aadForKey(key))
	}

	expiry := time.Now().Add(ttl)
	buf, err := cache.PrependExpiryHeader(data, expiry)
	if err != nil {
		return err
	}

	fp := s.filePath(key)
	if err := s.ensureDir(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// Restrict permissions explicitly (M3). os.CreateTemp already creates 0600
	// files; this Chmod is self-documenting and guards against a permissive
	// umask on platforms that honor it differently.
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}

	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	return os.Rename(tmpName, fp)
}

func (s *Store) Load(key string) (*Session, error) {
	fp := s.filePath(key)
	// #nosec G304 -- path is an internal hash under the store's own directory, not user input
	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCorrupt
		}
		return nil, err
	}

	if len(data) < 8 {
		return nil, ErrCorrupt
	}

	payload := data[8:]
	aad := aadForKey(key)

	if s.gcm != nil {
		decrypted, usedPrev, err := s.openWithFallback(payload, aad)
		if err != nil {
			return nil, ErrCorrupt
		}
		payload = decrypted

		if usedPrev {
			// Lazy re-encrypt with the current key so the blob is upgraded on
			// first read after rotation (M1). Best-effort: a write failure does
			// not fail the read.
			// #nosec G115 -- Unix second count; no realistic overflow
			expiry := time.Unix(int64(binary.BigEndian.Uint64(data[:8])), 0)
			_ = s.rewrite(key, payload, expiry)
		}
	}

	var sess Session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return nil, ErrCorrupt
	}
	return &sess, nil
}

// openWithFallback attempts to decrypt under the current key, then the previous
// key (M1). It returns the plaintext, whether the previous key was used, and an
// error only when both keys fail. Callers must hold s.gcm != nil.
func (s *Store) openWithFallback(payload, aad []byte) (plaintext []byte, usedPrev bool, err error) {
	if decrypted, err := cache.GCMDecrypt(s.gcm, payload, aad); err == nil {
		return decrypted, false, nil
	}
	if s.gcmPrev != nil {
		if decrypted, err := cache.GCMDecrypt(s.gcmPrev, payload, aad); err == nil {
			return decrypted, true, nil
		}
	}
	return nil, false, ErrCorrupt
}

// rewrite re-persists already-decrypted plaintext for key under the current key
// at the given expiry, preserving the existing TTL window. Used by the lazy
// re-encrypt path on rotation.
func (s *Store) rewrite(key string, plaintext []byte, expiry time.Time) error {
	data := plaintext
	if s.gcm != nil {
		data = cache.GCMEncrypt(s.gcm, data, aadForKey(key))
	}

	buf, err := cache.PrependExpiryHeader(data, expiry)
	if err != nil {
		return err
	}

	fp := s.filePath(key)
	if err := s.ensureDir(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, fp)
}

// loadFile reads and decrypts a session file by its on-disk path and filename
// hash. It is the rebuild-path counterpart to Load: the caller has the hash
// (the filename) but not the original key, so AAD is derived from the hash via
// aadForHash, which is byte-for-byte identical to the aadForKey(key) AAD used by
// Save/Load for the matching key (M7). It returns the decoded session, the
// decoded plaintext (for lazy re-encrypt), the stored expiry, whether the
// previous key was used (M1), and an error on corruption.
func (s *Store) loadFile(fp, hash string) (sess *Session, plaintext []byte, expiry time.Time, usedPrev bool, err error) {
	// #nosec G304 -- path is an internal hash under the store's own directory, not user input
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, nil, time.Time{}, false, err
	}
	if len(data) < 8 {
		return nil, nil, time.Time{}, false, ErrCorrupt
	}

	// #nosec G115 -- Unix second count; no realistic overflow
	expiry = time.Unix(int64(binary.BigEndian.Uint64(data[:8])), 0)
	payload := data[8:]

	if s.gcm != nil {
		decrypted, prev, derr := s.openWithFallback(payload, aadForHash(hash))
		if derr != nil {
			return nil, nil, time.Time{}, false, ErrCorrupt
		}
		payload = decrypted
		usedPrev = prev
	}

	var decoded Session
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, nil, time.Time{}, false, ErrCorrupt
	}
	return &decoded, payload, expiry, usedPrev, nil
}

func (s *Store) Delete(key string) error {
	return os.Remove(s.filePath(key))
}

func (s *Store) ListValid(now time.Time) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}

	var keys []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".session") {
			continue
		}

		fp := filepath.Join(s.dir, e.Name())
		// #nosec G304 -- path is an internal hash under the store's own directory, not user input
		f, err := os.Open(fp)
		if err != nil {
			continue
		}

		var ts [8]byte
		_, err = io.ReadFull(f, ts[:])
		_ = f.Close()
		if err != nil {
			continue
		}

		// #nosec G115 -- Unix second count; no realistic overflow
		expiry := int64(binary.BigEndian.Uint64(ts[:]))
		if now.Unix() > expiry {
			_ = os.Remove(fp)
			continue
		}

		name := strings.TrimSuffix(e.Name(), ".session")
		keys = append(keys, name)
	}
	return keys, nil
}

func (s *Store) CleanOrphans() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
	return nil
}

func (s *Store) filePath(key string) string {
	return filepath.Join(s.dir, fileHash(key)+".session")
}

// ensureDir re-creates the store directory if it has gone missing since
// construction. The dir is created once in NewStoreWithPrev, but on a
// long-lived server the OS cache location can be evicted out from under us
// mid-run (e.g. macOS cleaning ~/Library/Caches), which would otherwise make
// every subsequent write fail with a cryptic ENOENT and hard-block the whole
// session/export workflow. MkdirAll is a cheap no-op when the dir exists.
func (s *Store) ensureDir() error {
	return os.MkdirAll(s.dir, 0700)
}

// fileHash returns the hex-encoded SHA-256 of the key (used for the on-disk
// filename and for reverse lookup during rebuild).
func fileHash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// aadForKey returns the GCM additional authenticated data bound to a session
// blob: the hex SHA-256 filename hash of the key (M7). All three encryption
// sites — Save, Load, and manager.readSessionFile — derive identical AAD this
// way. The filename hash (not the raw key) is used because rebuild reconstructs
// AAD from the on-disk filename, where the original key is unrecoverable.
func aadForKey(key string) []byte {
	return []byte(fileHash(key))
}

// aadForHash returns the AAD derived directly from an on-disk filename hash. It
// is the rebuild-path counterpart to aadForKey: manager.readSessionFile holds
// the hash (the filename) but not the original key, so it derives AAD from the
// hash, which is byte-for-byte identical to aadForKey(key) for the matching key.
func aadForHash(hash string) []byte {
	return []byte(hash)
}

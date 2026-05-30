package cache

import (
	"context"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// diskFormatVersion is bumped whenever the on-disk blob format changes
// (independent of the build version). It is mixed into the version sentinel so
// a format break invalidates the cache cleanly even when the build version is
// unchanged, avoiding decrypt-failure churn. Bumped to v2 for the AAD-bound
// AES-GCM format (M7).
const diskFormatVersion = "v2"

type DiskCache struct {
	mu      sync.RWMutex
	dir     string
	gcm     cipher.AEAD // current key; nil when encryption disabled
	gcmPrev cipher.AEAD // previous key for rotation fallback; nil when unset
	entries map[string]diskEntry
}

type diskEntry struct {
	value     []byte
	storedAt  time.Time
	expiresAt time.Time
	ttl       time.Duration
}

func NewDiskCache(cfg DiskConfig) *DiskCache {
	dir := cfg.Dir
	if dir == "" {
		dir = os.TempDir()
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		slog.Warn("failed to create cache directory", "dir", dir, "err", err)
	}

	dc := &DiskCache{
		dir:     dir,
		entries: make(map[string]diskEntry),
	}

	// Best-effort: a malformed key falls back to plaintext (matching prior
	// behavior). Config validation already rejects non-64-hex keys upstream.
	if gcm, err := NewGCM(cfg.EncryptionKey); err == nil {
		dc.gcm = gcm
	} else {
		slog.Warn("disk cache encryption key invalid; storing plaintext", "err", err)
	}
	if gcm, err := NewGCM(cfg.EncryptionKeyPrev); err == nil {
		dc.gcmPrev = gcm
	} else {
		slog.Warn("disk cache previous encryption key invalid; rotation fallback disabled", "err", err)
	}

	// Mix the on-disk format version with the build version so either a format
	// break or a build change invalidates the cache cleanly.
	dc.invalidateOnVersionChange(diskFormatVersion + ":" + cfg.Version)

	go dc.cleanup()
	return dc
}

func (d *DiskCache) invalidateOnVersionChange(version string) {
	versionFile := filepath.Join(d.dir, ".version")
	existing, err := os.ReadFile(versionFile)
	if err != nil || string(existing) != version {
		entries, _ := os.ReadDir(d.dir)
		for _, e := range entries {
			if e.Name() == ".version" {
				continue
			}
			_ = os.Remove(filepath.Join(d.dir, e.Name()))
		}
		_ = os.WriteFile(versionFile, []byte(version), 0600)
		slog.Info("cache invalidated on version change", "version", version)
	}
}

func (d *DiskCache) Get(_ context.Context, key string) ([]byte, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	entry, ok := d.entries[key]
	if !ok {
		return d.getFromFile(key)
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.value, true
}

func (d *DiskCache) GetWithMeta(_ context.Context, key string) ([]byte, *EntryMeta, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	entry, ok := d.entries[key]
	if !ok {
		val, ok := d.getFromFile(key)
		if !ok {
			return nil, nil, false
		}
		return val, nil, true
	}
	if time.Now().After(entry.expiresAt) {
		return nil, nil, false
	}
	meta := &EntryMeta{StoredAt: entry.storedAt, TTL: entry.ttl}
	return entry.value, meta, true
}

func (d *DiskCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	d.entries[key] = diskEntry{
		value:     value,
		storedAt:  now,
		expiresAt: now.Add(ttl),
		ttl:       ttl,
	}
}

func (d *DiskCache) Delete(_ context.Context, key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.entries, key)

	fp := d.filePath(key)
	_ = os.Remove(fp)
}

func (d *DiskCache) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for key, entry := range d.entries {
		if time.Now().Before(entry.expiresAt) {
			d.writeToFile(key, entry)
		}
	}
	d.entries = make(map[string]diskEntry)
	return nil
}

func (d *DiskCache) Flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries = make(map[string]diskEntry)

	entries, _ := os.ReadDir(d.dir)
	for _, e := range entries {
		_ = os.Remove(filepath.Join(d.dir, e.Name()))
	}
}

func (d *DiskCache) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		d.mu.Lock()
		now := time.Now()
		for k, v := range d.entries {
			if now.After(v.expiresAt) {
				delete(d.entries, k)
			}
		}
		d.mu.Unlock()
	}
}

func (d *DiskCache) filePath(key string) string {
	safe := hex.EncodeToString([]byte(key))
	if len(safe) > 64 {
		safe = safe[:64]
	}
	return filepath.Join(d.dir, safe+".cache")
}

// aad binds a blob to its cache key so a ciphertext written for one key cannot
// be moved to another key's file and decrypted (M7). GCM authenticates this
// additional data; a mismatch fails Open.
func (d *DiskCache) aad(key string) []byte {
	return []byte(key)
}

func (d *DiskCache) writeToFile(key string, entry diskEntry) {
	data := entry.value
	if d.gcm != nil {
		data = GCMEncrypt(d.gcm, data, d.aad(key))
	}

	// Prepend expiry timestamp
	buf := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(buf[:8], uint64(entry.expiresAt.Unix()))
	copy(buf[8:], data)

	fp := d.filePath(key)
	_ = os.WriteFile(fp, buf, 0600)
}

func (d *DiskCache) getFromFile(key string) ([]byte, bool) {
	fp := d.filePath(key)
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, false
	}

	if len(data) < 8 {
		return nil, false
	}

	expiresUnix := binary.BigEndian.Uint64(data[:8])
	expiresAt := time.Unix(int64(expiresUnix), 0)
	if time.Now().After(expiresAt) {
		_ = os.Remove(fp)
		return nil, false
	}

	payload := data[8:]
	if d.gcm == nil {
		return payload, true
	}

	aad := d.aad(key)
	if decrypted, err := GCMDecrypt(d.gcm, payload, aad); err == nil {
		return decrypted, true
	}

	// Current key failed: try the previous key for zero-downtime rotation (M1).
	if d.gcmPrev != nil {
		if decrypted, err := GCMDecrypt(d.gcmPrev, payload, aad); err == nil {
			// Lazy re-encrypt with the current key and rewrite the file so the
			// blob is upgraded on first read after rotation.
			d.writeToFile(key, diskEntry{value: decrypted, expiresAt: expiresAt})
			return decrypted, true
		}
	}

	return nil, false
}

package cache

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DiskCache struct {
	mu      sync.RWMutex
	dir     string
	gcm     cipher.AEAD
	entries map[string]diskEntry
}

type diskEntry struct {
	value     []byte
	expiresAt time.Time
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

	if cfg.EncryptionKey != "" {
		key, err := hex.DecodeString(cfg.EncryptionKey)
		if err == nil && len(key) == 32 {
			block, err := aes.NewCipher(key)
			if err == nil {
				gcm, err := cipher.NewGCM(block)
				if err == nil {
					dc.gcm = gcm
				}
			}
		}
	}

	if cfg.Version != "" {
		dc.invalidateOnVersionChange(cfg.Version)
	}

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

func (d *DiskCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.entries[key] = diskEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
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

func (d *DiskCache) writeToFile(key string, entry diskEntry) {
	data := entry.value
	if d.gcm != nil {
		data = d.encrypt(data)
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
	if time.Now().Unix() > int64(expiresUnix) {
		_ = os.Remove(fp)
		return nil, false
	}

	payload := data[8:]
	if d.gcm != nil {
		decrypted, err := d.decrypt(payload)
		if err != nil {
			return nil, false
		}
		payload = decrypted
	}

	return payload, true
}

func (d *DiskCache) encrypt(plaintext []byte) []byte {
	nonce := make([]byte, d.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return plaintext
	}
	return d.gcm.Seal(nonce, nonce, plaintext, nil)
}

func (d *DiskCache) decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := d.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, io.ErrUnexpectedEOF
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return d.gcm.Open(nil, nonce, ct, nil)
}

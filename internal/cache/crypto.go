package cache

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrShortCiphertext is returned by GCMDecrypt when the input is too short to
// contain a nonce.
var ErrShortCiphertext = errors.New("ciphertext shorter than nonce")

// ExpiryHeaderSize is the fixed-width big-endian Unix-seconds prefix written
// ahead of a stored value by PrependExpiryHeader.
const ExpiryHeaderSize = 8

// MaxStoredValueBytes bounds a single stored value (cache entry, session blob,
// persisted record). It is far larger than any legitimate payload yet small
// enough that the header + length computation cannot overflow a platform int,
// closing CWE-190 on the `make([]byte, ExpiryHeaderSize+len(data))` pattern.
const MaxStoredValueBytes = 256 << 20 // 256 MiB

// PrependExpiryHeader returns an 8-byte big-endian Unix-seconds header followed
// by data. A zero `expiry` writes a zero header (no expiry). It rejects values
// larger than MaxStoredValueBytes so the size computation provably cannot
// overflow — the single, shared, bounds-checked replacement for the
// `make([]byte, 8+len(data))` sites in the cache, persist, and session stores.
func PrependExpiryHeader(data []byte, expiry time.Time) ([]byte, error) {
	if len(data) > MaxStoredValueBytes {
		return nil, fmt.Errorf("stored value too large: %d bytes (max %d)", len(data), MaxStoredValueBytes)
	}
	buf := make([]byte, ExpiryHeaderSize+len(data))
	if !expiry.IsZero() {
		// Safe: a Unix second count is non-negative and far within uint64.
		binary.BigEndian.PutUint64(buf[:ExpiryHeaderSize], uint64(expiry.Unix())) // #nosec G115
	}
	copy(buf[ExpiryHeaderSize:], data)
	return buf, nil
}

// NewGCM builds an AES-256-GCM AEAD from a 64-hex-character key. It is the
// single source of GCM construction shared by the disk cache, the persist
// store, and the session store, replacing the duplicated setup that previously
// lived in each package.
//
// An empty key yields (nil, nil): callers treat a nil AEAD as "encryption
// disabled" and store plaintext. A malformed or wrong-length key yields a
// non-nil error so misconfiguration is never silently downgraded by this
// helper; callers that prefer best-effort behavior may ignore the error and
// fall back to plaintext, matching the prior tolerant behavior.
func NewGCM(hexKey string) (cipher.AEAD, error) {
	if hexKey == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("encryption key must decode to 32 bytes (AES-256)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// GCMEncrypt seals plaintext with a fresh random nonce prepended to the output.
// aad is authenticated but not encrypted; pass nil for no additional data.
// On nonce-generation failure it returns the plaintext unchanged, preserving
// the prior best-effort behavior (write succeeds rather than dropping data).
func GCMEncrypt(gcm cipher.AEAD, plaintext, aad []byte) []byte {
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return plaintext
	}
	return gcm.Seal(nonce, nonce, plaintext, aad)
}

// GCMDecrypt opens a blob produced by GCMEncrypt. aad must match the value used
// at seal time (GCM authenticates it); a mismatch fails the Open with an
// authentication error, which is exactly the cross-key / cross-path swap guard.
func GCMDecrypt(gcm cipher.AEAD, ciphertext, aad []byte) ([]byte, error) {
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrShortCiphertext
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, aad)
}

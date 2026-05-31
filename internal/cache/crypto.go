package cache

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
)

// ErrShortCiphertext is returned by GCMDecrypt when the input is too short to
// contain a nonce.
var ErrShortCiphertext = errors.New("ciphertext shorter than nonce")

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

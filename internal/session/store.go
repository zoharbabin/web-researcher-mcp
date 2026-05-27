package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
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
)

var ErrCorrupt = errors.New("session file corrupt or unreadable")

type Store struct {
	dir string
	gcm cipher.AEAD
}

func NewStore(dir, encryptionKey string) (*Store, error) {
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

	if encryptionKey != "" {
		key, err := hex.DecodeString(encryptionKey)
		if err == nil && len(key) == 32 {
			block, err := aes.NewCipher(key)
			if err == nil {
				gcm, err := cipher.NewGCM(block)
				if err == nil {
					s.gcm = gcm
				}
			}
		}
	}

	return s, nil
}

func (s *Store) Save(key string, sess *Session, ttl time.Duration) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}

	if s.gcm != nil {
		data = s.encrypt(data)
	}

	expiry := time.Now().Add(ttl)
	buf := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(buf[:8], uint64(expiry.Unix()))
	copy(buf[8:], data)

	fp := s.filePath(key)
	tmp, err := os.CreateTemp(s.dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	return os.Rename(tmpName, fp)
}

func (s *Store) Load(key string) (*Session, error) {
	fp := s.filePath(key)
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
	if s.gcm != nil {
		decrypted, err := s.decrypt(payload)
		if err != nil {
			return nil, ErrCorrupt
		}
		payload = decrypted
	}

	var sess Session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return nil, ErrCorrupt
	}
	return &sess, nil
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
		f, err := os.Open(fp)
		if err != nil {
			continue
		}

		var ts [8]byte
		_, err = io.ReadFull(f, ts[:])
		f.Close()
		if err != nil {
			continue
		}

		expiry := int64(binary.BigEndian.Uint64(ts[:]))
		if now.Unix() > expiry {
			os.Remove(fp)
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
			os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
	return nil
}

func (s *Store) filePath(key string) string {
	h := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(h[:])+".session")
}

// fileHash returns the hex-encoded SHA-256 of the key (used for reverse lookup during rebuild).
func fileHash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func (s *Store) encrypt(plaintext []byte) []byte {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return plaintext
	}
	return s.gcm.Seal(nonce, nonce, plaintext, nil)
}

func (s *Store) decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := s.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, io.ErrUnexpectedEOF
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return s.gcm.Open(nil, nonce, ct, nil)
}

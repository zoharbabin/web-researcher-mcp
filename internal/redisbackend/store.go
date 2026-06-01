package redisbackend

import (
	"context"
	"crypto/cipher"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

// Store is a Redis-backed persist.Store. Values are AES-256-GCM encrypted before
// SET (key bound as AAD) so Redis holds only ciphertext — at-rest parity with
// the disk store. TTL maps to Redis EXPIRE, so expiry is enforced server-side
// and shared across pods.
type Store struct {
	b       *Backends
	gcm     cipher.AEAD
	gcmPrev cipher.AEAD
}

// PersistStore returns a persist.Store backed by Redis. Encryption is mandatory
// (Connect already guaranteed a key), so this never stores plaintext.
func (b *Backends) PersistStore() *Store {
	gcm, _ := cache.NewGCM(b.cfg.EncryptionKey)
	gcmPrev, _ := cache.NewGCM(b.cfg.EncryptionKeyPrev)
	return &Store{b: b, gcm: gcm, gcmPrev: gcmPrev}
}

func (s *Store) redisKey(k string) string { return s.b.key("persist", k) }

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool) {
	data, err := s.b.client.Get(ctx, s.redisKey(key)).Bytes()
	if err != nil {
		return nil, false // redis.Nil (miss) or any error → miss, never panic
	}
	aad := []byte(key)
	if pt, derr := cache.GCMDecrypt(s.gcm, data, aad); derr == nil {
		return pt, true
	}
	if s.gcmPrev != nil {
		if pt, derr := cache.GCMDecrypt(s.gcmPrev, data, aad); derr == nil {
			return pt, true // decrypt-fallback during key rotation
		}
	}
	return nil, false
}

func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	ct := cache.GCMEncrypt(s.gcm, value, []byte(key))
	// ttl<=0 → no expiry (Redis SET without EXPIRE).
	var exp time.Duration
	if ttl > 0 {
		exp = ttl
	}
	_ = s.b.client.Set(ctx, s.redisKey(key), ct, exp).Err()
}

func (s *Store) Delete(ctx context.Context, key string) {
	_ = s.b.client.Del(ctx, s.redisKey(key)).Err()
}

// incrByLua atomically increments a counter and sets its expiry on first
// creation, returning the new value. Using a single EVAL avoids the
// INCR-then-EXPIRE race across pods (a crash between the two would leave a
// counter that never expires). KEYS[1]=counter, ARGV[1]=ttlSeconds.
var incrByLua = redis.NewScript(`
local v = redis.call('INCR', KEYS[1])
if v == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return v
`)

// IncrDaily atomically increments a per-tenant daily counter shared across pods
// and returns the new value. The counter is created with a TTL that expires it
// at the given reset time, so the quota window is consistent fleet-wide. This
// is the cross-pod replacement for the in-memory daily counter (#42).
func (s *Store) IncrDaily(ctx context.Context, tenantID string, resetAt time.Time) (int64, bool) {
	ttl := time.Until(resetAt)
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	k := s.b.key("quota:daily", tenantID)
	v, err := incrByLua.Run(ctx, s.b.client, []string{k}, int64(ttl.Seconds())).Int64()
	if err != nil {
		return 0, false // caller falls back to local behavior on Redis error
	}
	return v, true
}

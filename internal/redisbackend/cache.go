package redisbackend

import (
	"context"
	"crypto/cipher"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

// SharedCache is the Redis-backed cross-pod cache tier (L2), satisfying
// cache.SharedLayer. Values are AES-256-GCM encrypted before SET — the cache
// may hold scraped content, so it gets the same at-rest protection as disk.
// Cache keys are already content-addressed SHA-256 hashes, so no key change is
// needed; the key is bound as GCM AAD.
type SharedCache struct {
	b       *Backends
	gcm     cipher.AEAD
	gcmPrev cipher.AEAD
}

// SharedCache returns a cross-pod cache layer for injection into cache.Hybrid.
func (b *Backends) SharedCache() *SharedCache {
	return &SharedCache{b: b, gcm: b.gcm, gcmPrev: b.gcmPrev}
}

func (c *SharedCache) redisKey(k string) string { return c.b.key("cache", k) }

func (c *SharedCache) Get(ctx context.Context, key string) ([]byte, bool) {
	data, err := c.b.client.Get(ctx, c.redisKey(key)).Bytes()
	if err != nil {
		return nil, false
	}
	aad := []byte(key)
	if pt, derr := cache.GCMDecrypt(c.gcm, data, aad); derr == nil {
		return pt, true
	}
	if c.gcmPrev != nil {
		if pt, derr := cache.GCMDecrypt(c.gcmPrev, data, aad); derr == nil {
			return pt, true
		}
	}
	return nil, false
}

func (c *SharedCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	ct := cache.GCMEncrypt(c.gcm, value, []byte(key))
	var exp time.Duration
	if ttl > 0 {
		exp = ttl
	}
	_ = c.b.client.Set(ctx, c.redisKey(key), ct, exp).Err()
}

func (c *SharedCache) Delete(ctx context.Context, key string) {
	_ = c.b.client.Del(ctx, c.redisKey(key)).Err()
}

var _ cache.SharedLayer = (*SharedCache)(nil)

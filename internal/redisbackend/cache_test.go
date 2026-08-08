package redisbackend

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// TestSharedCacheSetGetDelete exercises the full Get/Set/Delete round trip of
// the Redis-backed cross-pod cache tier (#465): a miss before any Set, a hit
// with the exact value back after Set, ciphertext (not plaintext) on the wire
// for encryption-at-rest parity with disk, and a miss again after Delete.
func TestSharedCacheSetGetDelete(t *testing.T) {
	b := newTestBackend(t)
	c := b.SharedCache()
	ctx := context.Background()

	if _, ok := c.Get(ctx, "k1"); ok {
		t.Fatal("expected miss before any Set")
	}

	c.Set(ctx, "k1", []byte("cached-value"), time.Hour)
	got, ok := c.Get(ctx, "k1")
	if !ok || string(got) != "cached-value" {
		t.Fatalf("round-trip failed: ok=%v val=%q", ok, got)
	}

	// Stored bytes must be ciphertext, not plaintext — the shared cache tier
	// gets the same at-rest protection as disk (a scraped page may be sensitive).
	raw, err := b.client.Get(ctx, c.redisKey("k1")).Bytes()
	if err != nil {
		t.Fatalf("reading raw stored bytes: %v", err)
	}
	if string(raw) == "cached-value" {
		t.Error("value stored in plaintext — encryption-at-rest violated")
	}

	c.Delete(ctx, "k1")
	if _, ok := c.Get(ctx, "k1"); ok {
		t.Error("expected miss after delete")
	}
}

// TestSharedCacheRedisKeyNamespacing pins the exact key shape: prefix +
// "cache:" + the caller's content-addressed key, unchanged (cache keys are
// already SHA-256 hashes, so redisKey does no further hashing/prefixing).
func TestSharedCacheRedisKeyNamespacing(t *testing.T) {
	b := newTestBackend(t)
	c := b.SharedCache()
	if got, want := c.redisKey("abc123"), "wr:cache:abc123"; got != want {
		t.Errorf("redisKey(%q) = %q, want %q", "abc123", got, want)
	}
}

// TestSharedCacheTTLExpiry verifies a Set with a TTL actually expires server-side
// in Redis (not just relying on the caller to stop asking), using miniredis's
// FastForward to deterministically advance virtual time past the TTL.
func TestSharedCacheTTLExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	b, err := Connect(context.Background(), Config{
		URL:           "redis://" + mr.Addr(),
		EncryptionKey: testKey,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	c := b.SharedCache()
	ctx := context.Background()

	c.Set(ctx, "k1", []byte("v"), 100*time.Millisecond)
	if _, ok := c.Get(ctx, "k1"); !ok {
		t.Fatal("expected hit immediately after Set")
	}

	mr.FastForward(200 * time.Millisecond)

	if _, ok := c.Get(ctx, "k1"); ok {
		t.Error("expected miss after TTL expiry")
	}
}

// TestSharedCacheZeroTTLMeansNoExpiry verifies Set with ttl<=0 stores the key
// without a Redis EXPIRE, matching the persist.Store contract for "no TTL".
func TestSharedCacheZeroTTLMeansNoExpiry(t *testing.T) {
	b := newTestBackend(t)
	c := b.SharedCache()
	ctx := context.Background()

	c.Set(ctx, "k1", []byte("v"), 0)

	ttl, err := b.client.TTL(ctx, c.redisKey("k1")).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl >= 0 {
		t.Errorf("expected no expiry set (negative TTL sentinel), got %v", ttl)
	}
}

// TestSharedCacheGetMissDoesNotPanicOnGarbageBytes verifies Get on a key that
// exists in Redis but was never written through this SharedCache (so it isn't
// valid GCM ciphertext for this key's AAD) fails closed as a miss rather than
// panicking — defense-in-depth for the decrypt path exercised by #465.
func TestSharedCacheGetMissDoesNotPanicOnGarbageBytes(t *testing.T) {
	b := newTestBackend(t)
	c := b.SharedCache()
	ctx := context.Background()

	if err := b.client.Set(ctx, c.redisKey("garbage"), "not-valid-ciphertext", 0).Err(); err != nil {
		t.Fatalf("seeding garbage value: %v", err)
	}

	if _, ok := c.Get(ctx, "garbage"); ok {
		t.Error("expected miss for undecryptable garbage bytes")
	}
}

// Package persist provides a small key/value Store with TTL semantics, backed
// either by process memory (zero-config default) or by encrypted disk.
//
// One interface backs multiple subsystems that need durable-or-ephemeral state
// without bespoke storage code: auth token revocation (H2) and rate-limit daily
// quota counters (H7). Local (memory) and cloud (disk) implementations behave
// identically so there is no behavioral drift between deployment modes.
//
// REDIS_URL is currently a documented no-op pass-through (config.RedisURL): no
// RedisStore implementation ships yet. When one lands it will satisfy this same
// Store interface, so callers need no changes. Until then a Redis URL does not
// alter behavior — memory or disk is selected explicitly by the constructor the
// caller chooses.
//
// The disk implementation generalizes the session store's proven pattern:
// AES-256-GCM encryption (via the shared cache crypto helpers), atomic writes
// (temp file + rename), 0600 permissions, an 8-byte big-endian expiry prefix,
// and an in-memory index for fast lookups. Keys are hashed (SHA-256) for the
// on-disk filename and bound as GCM additional authenticated data so a blob
// cannot be swapped to a different key's file.
package persist

import (
	"context"
	"time"
)

// Store is a TTL-bounded byte key/value store. Implementations must be safe for
// concurrent use. Get reports a miss (false) for absent, expired, or
// undecryptable entries — never an error in the read path, keeping callers on
// the values-not-panics discipline.
type Store interface {
	// Get returns the stored value and true, or (nil,false) on miss/expiry.
	Get(ctx context.Context, key string) ([]byte, bool)
	// Set stores value under key for at least ttl. A non-positive ttl stores
	// the value without expiry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	// Delete removes key if present; deleting an absent key is a no-op.
	Delete(ctx context.Context, key string)
}

// Package redisbackend is the SINGLE, ISOLATED home for all Redis-backed
// implementations of the server's storage interfaces. It is the ONLY package
// in the codebase that imports github.com/redis/go-redis. This is a deliberate
// compliance + debuggability boundary:
//
//   - Two iron-clad paths. STDIO mode never touches Redis. HTTP mode uses Redis
//     ONLY when REDIS_URL is set. The decision is made in exactly one place
//     (main.go, via Connect below) — no other package may construct a Redis
//     client, so there is no way for the Redis path to "creep" into STDIO or
//     into the zero-config default.
//   - One gate. Connect is the sole entry point. It is called from main.go only
//     after confirming HTTP mode AND a non-empty REDIS_URL, and it FAILS FAST:
//     an operator who set REDIS_URL opted into cross-pod correctness, so a bad
//     URL or unreachable server is a startup error, never a silent fallback to
//     per-pod memory (which would reintroduce the N×-rate-limit bug).
//   - Interface parity. The returned types satisfy the existing cache.Cache,
//     persist.Store, and session.Manager interfaces, so callers are identical
//     to the in-memory path. Nothing downstream knows Redis is present.
//   - Encryption parity. Personal-data namespaces (sessions, persist) are
//     AES-256-GCM encrypted before SET using the same cache crypto helpers and
//     key as disk — Redis is never a plaintext trust downgrade. When no
//     encryption key is configured, Connect refuses to back personal-data
//     namespaces with Redis (see Backends.RequireEncryption).
package redisbackend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newID() string     { return uuid.New().String() }
func nowUTC() time.Time { return time.Now().UTC() }

// Config controls how Connect builds the Redis-backed stores.
type Config struct {
	// URL is the redis:// connection string (REDIS_URL). Required.
	URL string
	// EncryptionKey / EncryptionKeyPrev are the 64-hex AES-256-GCM keys shared
	// with the disk path. EncryptionKey is REQUIRED for personal-data namespaces
	// (sessions, persist); Connect errors if it is empty, to guarantee
	// encryption-at-rest parity with disk.
	EncryptionKey     string
	EncryptionKeyPrev string
	// SessionTTL bounds session keys server-side via Redis EXPIRE.
	SessionTTL time.Duration
	// MaxSessionsPerTenant mirrors the in-memory cap (0 = unlimited).
	MaxSessionsPerTenant int
	// KeyPrefix namespaces all keys (default "wr:"), so the instance can share a
	// Redis with other tenants/apps without collision.
	KeyPrefix string
	// DialTimeout bounds the fail-fast connectivity check.
	DialTimeout time.Duration
}

// ErrEncryptionRequired is returned when Redis is requested for personal-data
// namespaces without an encryption key, which would break at-rest parity.
var ErrEncryptionRequired = errors.New("redisbackend: CACHE_ENCRYPTION_KEY is required when REDIS_URL is set (personal-data namespaces must be encrypted at rest)")

// Backends holds the Redis-backed implementations of the storage interfaces.
// Each field satisfies the same interface as its in-memory counterpart, so
// main.go swaps them in with no caller changes.
type Backends struct {
	client *redis.Client
	cfg    Config
}

// Connect is the SOLE entry point. It validates config, opens the client, and
// performs a fail-fast PING. Any failure is returned as an error — the caller
// (main.go) treats it as fatal in HTTP mode rather than degrading to memory.
//
// Encryption is mandatory: personal-data namespaces (sessions, persist) are
// encrypted with EncryptionKey, so an empty key is rejected up front.
func Connect(ctx context.Context, cfg Config) (*Backends, error) {
	if cfg.URL == "" {
		return nil, errors.New("redisbackend: URL is required")
	}
	if cfg.EncryptionKey == "" {
		return nil, ErrEncryptionRequired
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "wr:"
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 5 * time.Second
	}

	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("redisbackend: invalid REDIS_URL: %w", err)
	}
	opts.DialTimeout = cfg.DialTimeout
	client := redis.NewClient(opts)

	// Fail-fast connectivity check: the operator opted into distributed state by
	// setting REDIS_URL, so an unreachable Redis is a startup error.
	pingCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redisbackend: cannot reach Redis at startup: %w", err)
	}

	return &Backends{client: client, cfg: cfg}, nil
}

// Close releases the Redis client.
func (b *Backends) Close() error {
	if b == nil || b.client == nil {
		return nil
	}
	return b.client.Close()
}

// key namespaces a logical key under the configured prefix + a subsystem tag.
func (b *Backends) key(subsystem, k string) string {
	return b.cfg.KeyPrefix + subsystem + ":" + k
}

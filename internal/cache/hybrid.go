package cache

import (
	"context"
	"time"
)

type HybridConfig struct {
	Memory         MemoryConfig
	Disk           DiskConfig
	RedisURL       string
	CacheIsolation string // read by main.go to decide whether to wrap with TenantAware
}

type DiskConfig struct {
	Dir               string
	EncryptionKey     string
	EncryptionKeyPrev string // optional previous key for zero-downtime rotation (decrypt-fallback + lazy re-encrypt)
	Version           string
}

// SharedLayer is an optional cross-pod cache tier (L2) sitting between local
// memory (L1) and disk (L3). It is injected by main.go ONLY in HTTP mode with
// REDIS_URL set; the implementation (Redis) lives entirely in the redisbackend
// package, so the cache package never imports go-redis. A nil shared layer
// means single-pod behavior, byte-for-byte unchanged.
type SharedLayer interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	Delete(ctx context.Context, key string)
}

type Hybrid struct {
	memory *Memory
	disk   *DiskCache
	shared SharedLayer // optional cross-pod L2; nil = single-pod
}

func NewHybrid(cfg HybridConfig) *Hybrid {
	memory := NewMemory(cfg.Memory)
	disk := NewDiskCache(cfg.Disk)

	return &Hybrid{
		memory: memory,
		disk:   disk,
	}
}

// WithSharedLayer attaches a cross-pod L2 cache tier. Called once at startup
// from main.go when Redis is configured; never after serving begins.
func (h *Hybrid) WithSharedLayer(s SharedLayer) *Hybrid {
	h.shared = s
	return h
}

func (h *Hybrid) Get(ctx context.Context, key string) ([]byte, bool) {
	// L1: memory
	if val, ok := h.memory.Get(ctx, key); ok {
		return val, true
	}

	// L2 (optional): shared cross-pod tier — promote to L1 on hit.
	if h.shared != nil {
		if val, ok := h.shared.Get(ctx, key); ok {
			h.memory.Set(ctx, key, val, 30*time.Minute)
			return val, true
		}
	}

	// L3: disk — promote to L1 with remaining TTL
	if val, meta, ok := h.disk.GetWithMeta(ctx, key); ok {
		promoteTTL := 30 * time.Minute
		if meta != nil {
			remaining := meta.TTL - time.Since(meta.StoredAt)
			if remaining > 0 {
				promoteTTL = remaining
			}
		}
		h.memory.Set(ctx, key, val, promoteTTL)
		return val, true
	}

	return nil, false
}

func (h *Hybrid) GetWithMeta(ctx context.Context, key string) ([]byte, *EntryMeta, bool) {
	// L1: memory (has accurate metadata)
	if val, meta, ok := h.memory.GetWithMeta(ctx, key); ok {
		return val, meta, true
	}

	// L2 (optional): shared cross-pod tier. It does not carry rich metadata, so
	// promote with the standard TTL and synthesize freshness from that.
	if h.shared != nil {
		if val, ok := h.shared.Get(ctx, key); ok {
			h.memory.Set(ctx, key, val, 30*time.Minute)
			return val, &EntryMeta{StoredAt: time.Now(), TTL: 30 * time.Minute}, true
		}
	}

	// L3: disk — promote to L1 with remaining TTL
	if val, meta, ok := h.disk.GetWithMeta(ctx, key); ok {
		promoteTTL := 30 * time.Minute
		if meta != nil {
			remaining := meta.TTL - time.Since(meta.StoredAt)
			if remaining > 0 {
				promoteTTL = remaining
			}
		}
		h.memory.Set(ctx, key, val, promoteTTL)
		return val, meta, true
	}

	return nil, nil, false
}

func (h *Hybrid) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	h.memory.Set(ctx, key, value, ttl)
	if h.shared != nil {
		h.shared.Set(ctx, key, value, ttl)
	}
	h.disk.Set(ctx, key, value, ttl)
}

func (h *Hybrid) Delete(ctx context.Context, key string) {
	h.memory.Delete(ctx, key)
	if h.shared != nil {
		h.shared.Delete(ctx, key)
	}
	h.disk.Delete(ctx, key)
}

func (h *Hybrid) Close() error {
	_ = h.memory.Close()
	return h.disk.Close()
}

func (h *Hybrid) Flush() {
	_ = h.memory.Close()
	h.disk.Flush()
}

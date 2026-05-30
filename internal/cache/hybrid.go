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
	Dir           string
	EncryptionKey string
	Version       string
}

type Hybrid struct {
	memory *Memory
	disk   *DiskCache
}

func NewHybrid(cfg HybridConfig) *Hybrid {
	memory := NewMemory(cfg.Memory)
	disk := NewDiskCache(cfg.Disk)

	return &Hybrid{
		memory: memory,
		disk:   disk,
	}
}

func (h *Hybrid) Get(ctx context.Context, key string) ([]byte, bool) {
	// L1: memory
	if val, ok := h.memory.Get(ctx, key); ok {
		return val, true
	}

	// L2: disk — promote to L1 with remaining TTL
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

	// L2: disk — promote to L1 with remaining TTL
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
	h.disk.Set(ctx, key, value, ttl)
}

func (h *Hybrid) Delete(ctx context.Context, key string) {
	h.memory.Delete(ctx, key)
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

package cache

import (
	"context"
	"time"
)

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	GetWithMeta(ctx context.Context, key string) ([]byte, *EntryMeta, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	Delete(ctx context.Context, key string)
	Flush()
	Close() error
}

// EntryMeta contains metadata about a cached entry.
type EntryMeta struct {
	StoredAt time.Time
	TTL      time.Duration
}

// AgeSeconds returns how old the cached entry is.
func (m *EntryMeta) AgeSeconds() int {
	return int(time.Since(m.StoredAt).Seconds())
}

// MaxAgeSeconds returns the configured TTL in seconds.
func (m *EntryMeta) MaxAgeSeconds() int {
	return int(m.TTL.Seconds())
}

// Freshness returns a qualitative label: "fresh", "aging", or "expiring".
func (m *EntryMeta) Freshness() string {
	ratio := float64(time.Since(m.StoredAt)) / float64(m.TTL)
	switch {
	case ratio < 0.5:
		return "fresh"
	case ratio < 0.8:
		return "aging"
	default:
		return "expiring"
	}
}

// TenantAware wraps a Cache and prefixes all keys with the tenant ID
// extracted from context, providing per-tenant cache isolation.
// When tenantFromCtx returns "", keys pass through unmodified (shared mode).
type TenantAware struct {
	inner         Cache
	tenantFromCtx func(ctx context.Context) string
}

func NewTenantAware(inner Cache, tenantFromCtx func(ctx context.Context) string) *TenantAware {
	return &TenantAware{inner: inner, tenantFromCtx: tenantFromCtx}
}

func (t *TenantAware) scopedKey(ctx context.Context, key string) string {
	tenant := t.tenantFromCtx(ctx)
	if tenant == "" || tenant == "default" {
		return key
	}
	return tenant + ":" + key
}

func (t *TenantAware) Get(ctx context.Context, key string) ([]byte, bool) {
	return t.inner.Get(ctx, t.scopedKey(ctx, key))
}

func (t *TenantAware) GetWithMeta(ctx context.Context, key string) ([]byte, *EntryMeta, bool) {
	return t.inner.GetWithMeta(ctx, t.scopedKey(ctx, key))
}

func (t *TenantAware) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	t.inner.Set(ctx, t.scopedKey(ctx, key), value, ttl)
}

func (t *TenantAware) Delete(ctx context.Context, key string) {
	t.inner.Delete(ctx, t.scopedKey(ctx, key))
}

func (t *TenantAware) Flush()       { t.inner.Flush() }
func (t *TenantAware) Close() error { return t.inner.Close() }

type Noop struct{}

func NewNoop() *Noop                                                                   { return &Noop{} }
func (n *Noop) Get(_ context.Context, _ string) ([]byte, bool)                         { return nil, false }
func (n *Noop) GetWithMeta(_ context.Context, _ string) ([]byte, *EntryMeta, bool)     { return nil, nil, false }
func (n *Noop) Set(_ context.Context, _ string, _ []byte, _ time.Duration)             {}
func (n *Noop) Delete(_ context.Context, _ string)                                     {}
func (n *Noop) Flush()                                                                 {}
func (n *Noop) Close() error                                                           { return nil }

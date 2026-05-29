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

type Noop struct{}

func NewNoop() *Noop                                                                   { return &Noop{} }
func (n *Noop) Get(_ context.Context, _ string) ([]byte, bool)                         { return nil, false }
func (n *Noop) GetWithMeta(_ context.Context, _ string) ([]byte, *EntryMeta, bool)     { return nil, nil, false }
func (n *Noop) Set(_ context.Context, _ string, _ []byte, _ time.Duration)             {}
func (n *Noop) Delete(_ context.Context, _ string)                                     {}
func (n *Noop) Flush()                                                                 {}
func (n *Noop) Close() error                                                           { return nil }

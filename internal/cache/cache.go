package cache

import (
	"context"
	"time"
)

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	Delete(ctx context.Context, key string)
	Flush()
	Close() error
}

type Noop struct{}

func NewNoop() *Noop                                                     { return &Noop{} }
func (n *Noop) Get(_ context.Context, _ string) ([]byte, bool)          { return nil, false }
func (n *Noop) Set(_ context.Context, _ string, _ []byte, _ time.Duration) {}
func (n *Noop) Delete(_ context.Context, _ string)                       {}
func (n *Noop) Flush()                                                   {}
func (n *Noop) Close() error                                             { return nil }

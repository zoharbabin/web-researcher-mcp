package tools

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
)

// coalescedFetch wraps a cache-miss upstream call with singleflight
// request-coalescing (#474): if N identical requests for the same cache key
// arrive concurrently during the same miss window, only one reaches fetch —
// the rest block and receive its result, cutting duplicate upstream spend and
// provider rate-limit pressure on popular-query bursts.
//
// The dedup key is tenant-scoped (mirroring cache.TenantAware.scopedKey's
// exact tenant/"default" handling) so two tenants issuing the identical query
// concurrently never coalesce into the same in-flight call — that would leak
// one tenant's result to another and defeat per-tenant cache isolation
// (CACHE_ISOLATION, #484). Callers pass the same plain cacheKey already used
// for deps.Cache.Get/Set; this function does the tenant-scoping internally.
//
// deps.Singleflight is nil-safe: a nil group (e.g. a minimal test harness)
// falls through to calling fetch directly, uncoalesced.
func coalescedFetch[T any](ctx context.Context, deps Dependencies, cacheKey string, fetch func() (T, error)) (T, error) {
	if deps.Singleflight == nil {
		return fetch()
	}
	key := coalesceKey(ctx, cacheKey)
	v, err, _ := deps.Singleflight.Do(key, func() (any, error) {
		return fetch()
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

// coalesceKey tenant-scopes a plain cache key for singleflight dedup,
// mirroring cache.TenantAware.scopedKey: the empty and "default" tenants
// share one dedup bucket (shared-mode cache), any other tenant gets its own.
func coalesceKey(ctx context.Context, cacheKey string) string {
	tenant := auth.TenantIDFromContext(ctx)
	if tenant == "" || tenant == "default" {
		return cacheKey
	}
	return tenant + ":" + cacheKey
}

package ratelimit

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
	"golang.org/x/time/rate"
)

// quotaStorePrefix namespaces daily-quota counters in the shared persist.Store
// so they cannot collide with other subsystems (e.g. auth revocation) backed by
// the same store.
const quotaStorePrefix = "ratelimit:daily:"

type Limiter struct {
	global    *rate.Limiter
	tenants   sync.Map // map[string]*tenantLimiter
	ips       sync.Map // map[string]*rate.Limiter (per-IP pre-auth limiter)
	config    config.RateLimitConfig
	cleanupMu sync.Mutex

	// store, when non-nil, persists daily-quota counters across restarts
	// (H7, RATE_LIMIT_PERSIST=true). nil keeps pure-memory zero-config behavior.
	store persist.Store
}

type tenantLimiter struct {
	limiter    *rate.Limiter
	dailyCount int64
	dailyReset time.Time
	hydrated   bool // whether dailyCount was loaded from the store
	mu         sync.Mutex
}

// New constructs a pure-memory Limiter. Zero-config: daily counters live only in
// process memory and reset on restart. Behavior is byte-for-byte unchanged from
// before the persist.Store addition.
func New(cfg config.RateLimitConfig) *Limiter {
	return &Limiter{
		global: rate.NewLimiter(rate.Limit(cfg.Global), cfg.Global),
		config: cfg,
	}
}

// NewWithStore constructs a Limiter that writes daily-quota counters through to
// store so they survive restarts. A nil store is equivalent to New(cfg) — the
// limiter stays pure-memory. The store is only consulted for the daily quota;
// the per-minute and per-IP token buckets are always in-memory by design.
func NewWithStore(cfg config.RateLimitConfig, store persist.Store) *Limiter {
	l := New(cfg)
	l.store = store
	return l
}

func (l *Limiter) Allow(tenantID string) bool {
	if !l.global.Allow() {
		return false
	}
	tl := l.getTenantLimiter(tenantID)
	return tl.limiter.Allow()
}

func (l *Limiter) AllowDaily(tenantID string) bool {
	tl := l.getTenantLimiter(tenantID)
	tl.mu.Lock()
	defer tl.mu.Unlock()

	now := time.Now()
	if now.After(tl.dailyReset) {
		tl.dailyCount = 0
		tl.dailyReset = now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	}

	if tl.dailyCount >= int64(l.config.DailyQuota) {
		return false
	}
	tl.dailyCount++
	l.persistCount(tenantID, tl)
	return true
}

func (l *Limiter) getTenantLimiter(tenantID string) *tenantLimiter {
	if v, ok := l.tenants.Load(tenantID); ok {
		return v.(*tenantLimiter)
	}

	tl := &tenantLimiter{
		limiter:    rate.NewLimiter(rate.Every(time.Minute/time.Duration(l.config.PerTenant)), l.config.PerTenant),
		dailyReset: time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour),
	}
	actual, loaded := l.tenants.LoadOrStore(tenantID, tl)
	got := actual.(*tenantLimiter)
	if !loaded {
		// First materialization of this tenant in-process: lazily hydrate the
		// daily counter from the store so a restart resumes where it left off.
		l.hydrateCount(tenantID, got)
	}
	return got
}

// hydrateCount loads a tenant's persisted daily counter into tl exactly once.
// No-op when no store is configured. Caller must not already hold tl.mu.
func (l *Limiter) hydrateCount(tenantID string, tl *tenantLimiter) {
	if l.store == nil {
		return
	}
	tl.mu.Lock()
	defer tl.mu.Unlock()
	if tl.hydrated {
		return
	}
	tl.hydrated = true
	if count, reset, ok := l.loadCount(tenantID); ok {
		// Only adopt the persisted value if it belongs to the current window.
		if time.Now().Before(reset) {
			tl.dailyCount = count
			tl.dailyReset = reset
		}
	}
}

// persistCount writes a tenant's current counter through to the store. No-op
// when no store is configured. Caller must hold tl.mu.
func (l *Limiter) persistCount(tenantID string, tl *tenantLimiter) {
	if l.store == nil {
		return
	}
	buf := make([]byte, 16)
	// #nosec G115 -- daily request counter is non-negative; no realistic overflow
	binary.BigEndian.PutUint64(buf[0:8], uint64(tl.dailyCount))
	// #nosec G115 -- Unix nanosecond count; no realistic overflow
	binary.BigEndian.PutUint64(buf[8:16], uint64(tl.dailyReset.UnixNano()))
	// TTL until the window resets (+1h slack) so stale windows self-evict.
	ttl := time.Until(tl.dailyReset) + time.Hour
	l.store.Set(context.Background(), quotaStorePrefix+tenantID, buf, ttl)
}

// loadCount reads a tenant's persisted counter. Returns ok=false on miss or
// malformed payload — never an error, keeping the read path values-not-panics.
func (l *Limiter) loadCount(tenantID string) (count int64, reset time.Time, ok bool) {
	if l.store == nil {
		return 0, time.Time{}, false
	}
	buf, found := l.store.Get(context.Background(), quotaStorePrefix+tenantID)
	if !found || len(buf) != 16 {
		return 0, time.Time{}, false
	}
	// #nosec G115 -- daily request counter round-trip; no realistic overflow
	count = int64(binary.BigEndian.Uint64(buf[0:8]))
	// #nosec G115 -- Unix nanosecond count round-trip; no realistic overflow
	reset = time.Unix(0, int64(binary.BigEndian.Uint64(buf[8:16])))
	return count, reset, true
}

func (l *Limiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := auth.TenantIDFromContext(r.Context())

		if !l.Allow(tid) {
			w.Header().Set("Retry-After", "60")
			msg := fmt.Sprintf(
				"Rate limited: %d req/min exceeded for tenant %q. Retry after 60s. Set RATE_LIMIT_PER_TENANT to increase.",
				l.config.PerTenant, tid)
			http.Error(w, msg, http.StatusTooManyRequests)
			return
		}

		if !l.AllowDaily(tid) {
			w.Header().Set("Retry-After", "3600")
			msg := fmt.Sprintf(
				"Daily quota exceeded: %d req/day for tenant %q. Resets at midnight UTC. Set DAILY_QUOTA_PER_TENANT to increase.",
				l.config.DailyQuota, tid)
			http.Error(w, msg, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AllowIP reports whether a request from ip may proceed under the pre-auth
// per-IP limit (RATE_LIMIT_PER_IP req/min). When PerIP <= 0 the limit is
// disabled and every request is allowed (passthrough). Each distinct IP gets
// its own token bucket.
func (l *Limiter) AllowIP(ip string) bool {
	if l.config.PerIP <= 0 {
		return true
	}
	return l.getIPLimiter(ip).Allow()
}

func (l *Limiter) getIPLimiter(ip string) *rate.Limiter {
	if v, ok := l.ips.Load(ip); ok {
		return v.(*rate.Limiter)
	}
	lim := rate.NewLimiter(rate.Every(time.Minute/time.Duration(l.config.PerIP)), l.config.PerIP)
	actual, _ := l.ips.LoadOrStore(ip, lim)
	return actual.(*rate.Limiter)
}

// WrapIP is pre-auth middleware that rejects request floods from a single
// client IP before they reach authentication or any tool. When
// RATE_LIMIT_PER_IP=0 (default) it is a transparent passthrough so zero-config
// and legitimate research are never blocked. The client IP is derived from the
// leftmost X-Forwarded-For entry only when TRUST_PROXY=true (behind a trusted
// load balancer); otherwise it uses the raw connection RemoteAddr, preventing
// spoofed-IP bypass.
func (l *Limiter) WrapIP(next http.Handler) http.Handler {
	if l.config.PerIP <= 0 {
		// Passthrough: avoid the sync.Map and per-request work entirely.
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := l.clientIP(r)
		if !l.AllowIP(ip) {
			w.Header().Set("Retry-After", "60")
			msg := fmt.Sprintf(
				"Rate limited: %d req/min exceeded for your address. Retry after 60s. Set RATE_LIMIT_PER_IP to increase.",
				l.config.PerIP)
			http.Error(w, msg, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the client address for per-IP limiting. With TRUST_PROXY it
// honors the leftmost X-Forwarded-For hop; otherwise it uses RemoteAddr. A
// malformed RemoteAddr (no port) is used verbatim rather than panicking.
func (l *Limiter) clientIP(r *http.Request) string {
	if l.config.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			if first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without a port (or otherwise malformed): use as-is.
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// TenantStats returns rate limit status for a given tenant.
type TenantStats struct {
	TenantID       string `json:"tenantId"`
	PerMinuteLimit int    `json:"perMinuteLimit"`
	DailyLimit     int    `json:"dailyLimit"`
	DailyUsed      int64  `json:"dailyUsed"`
	DailyRemaining int64  `json:"dailyRemaining"`
	DailyResetsAt  string `json:"dailyResetsAt"`
}

// Stats returns rate limit configuration and usage for a tenant. When a store is
// configured it reflects the persisted counter even if the tenant has not yet
// been materialized in this process (e.g. immediately after a restart).
func (l *Limiter) Stats(tenantID string) TenantStats {
	stats := TenantStats{
		TenantID:       tenantID,
		PerMinuteLimit: l.config.PerTenant,
		DailyLimit:     l.config.DailyQuota,
	}

	if v, ok := l.tenants.Load(tenantID); ok {
		tl := v.(*tenantLimiter)
		tl.mu.Lock()
		stats.DailyUsed = tl.dailyCount
		stats.DailyResetsAt = tl.dailyReset.UTC().Format(time.RFC3339)
		tl.mu.Unlock()
	} else if count, reset, ok := l.loadCount(tenantID); ok && time.Now().Before(reset) {
		stats.DailyUsed = count
		stats.DailyResetsAt = reset.UTC().Format(time.RFC3339)
	}

	stats.DailyRemaining = int64(l.config.DailyQuota) - stats.DailyUsed
	if stats.DailyRemaining < 0 {
		stats.DailyRemaining = 0
	}
	return stats
}

// Config returns the rate limit configuration.
func (l *Limiter) Config() config.RateLimitConfig {
	return l.config
}

func (l *Limiter) Cleanup() {
	l.cleanupMu.Lock()
	defer l.cleanupMu.Unlock()

	l.tenants.Range(func(key, value any) bool {
		tl := value.(*tenantLimiter)
		tl.mu.Lock()
		idle := time.Since(tl.dailyReset.Add(-24*time.Hour)) > time.Hour
		tl.mu.Unlock()
		if idle {
			l.tenants.Delete(key)
		}
		return true
	})

	// Prune per-IP limiters whose bucket has fully refilled (idle since the last
	// request), so the IP map does not grow unbounded under sustained traffic.
	l.ips.Range(func(key, value any) bool {
		lim := value.(*rate.Limiter)
		if lim.Tokens() >= float64(l.config.PerIP) {
			l.ips.Delete(key)
		}
		return true
	})
}

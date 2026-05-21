package ratelimit

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"golang.org/x/time/rate"
)

type Limiter struct {
	global    *rate.Limiter
	tenants   sync.Map // map[string]*tenantLimiter
	config    config.RateLimitConfig
	cleanupMu sync.Mutex
}

type tenantLimiter struct {
	limiter    *rate.Limiter
	dailyCount int64
	dailyReset time.Time
	mu         sync.Mutex
}

func New(cfg config.RateLimitConfig) *Limiter {
	return &Limiter{
		global: rate.NewLimiter(rate.Limit(cfg.Global), cfg.Global),
		config: cfg,
	}
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
	actual, _ := l.tenants.LoadOrStore(tenantID, tl)
	return actual.(*tenantLimiter)
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

// TenantStats returns rate limit status for a given tenant.
type TenantStats struct {
	TenantID        string `json:"tenantId"`
	PerMinuteLimit  int    `json:"perMinuteLimit"`
	DailyLimit      int    `json:"dailyLimit"`
	DailyUsed       int64  `json:"dailyUsed"`
	DailyRemaining  int64  `json:"dailyRemaining"`
	DailyResetsAt   string `json:"dailyResetsAt"`
}

// Stats returns rate limit configuration and usage for a tenant.
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
}

package ratelimit

import (
	"net/http"
	"sync"
	"time"

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
		tenantID := r.Context().Value(contextKeyTenantID)
		tid := "anonymous"
		if tenantID != nil {
			tid = tenantID.(string)
		}

		if !l.Allow(tid) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}

		if !l.AllowDaily(tid) {
			w.Header().Set("Retry-After", "3600")
			http.Error(w, "daily quota exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type contextKey string

const contextKeyTenantID contextKey = "tenantID"

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

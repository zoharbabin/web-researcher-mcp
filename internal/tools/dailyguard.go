package tools

import (
	"sync"
	"time"
)

// DailyCallGuard is a transport-agnostic, in-process per-(tenant,user) daily
// call ceiling — a denial-of-wallet backstop (OWASP Agentic ASI06) that applies
// in STDIO too, where the HTTP rate limiter and daily quota do not run. It is
// OFF unless a positive limit is configured (MAX_CALLS_PER_DAY).
//
// Counts are kept in memory and reset at the UTC day boundary. This is a local
// backstop, not a distributed quota: each process counts independently (the
// fleet-wide control is the Redis daily quota on the HTTP path). Operators who
// need a hard spend ceiling should also set upstream provider billing caps.
type DailyCallGuard struct {
	limit int
	mu    sync.Mutex
	day   string         // UTC date "2006-01-02" the counts belong to
	count map[string]int // (tenant\x00user) -> calls today
}

// NewDailyCallGuard returns a guard enforcing `limit` calls per (tenant,user)
// per UTC day. limit<=0 returns a disabled guard (Allow always true).
func NewDailyCallGuard(limit int) *DailyCallGuard {
	return &DailyCallGuard{limit: limit, count: make(map[string]int)}
}

// Enabled reports whether the guard enforces a ceiling.
func (g *DailyCallGuard) Enabled() bool { return g != nil && g.limit > 0 }

// Allow records one call for (tenantID,userID) on the given day and reports
// whether it is within the daily ceiling. now is passed in for testability;
// callers use time.Now().UTC(). When disabled it always allows.
func (g *DailyCallGuard) Allow(tenantID, userID string, now time.Time) bool {
	if !g.Enabled() {
		return true
	}
	day := now.UTC().Format("2006-01-02")
	key := tenantID + "\x00" + userID

	g.mu.Lock()
	defer g.mu.Unlock()
	if day != g.day {
		// New UTC day: reset all counters.
		g.day = day
		g.count = make(map[string]int)
	}
	if g.count[key] >= g.limit {
		return false
	}
	g.count[key]++
	return true
}

package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// HealthSnapshotter supplies a live provider/breaker snapshot for the operator
// dashboard. The Router satisfies it (via a thin adapter in main.go); the server
// package consumes it through this small interface to avoid importing search —
// the same decoupling pattern as resources.HealthProvider and
// metrics.AuditLossSource. Health() returns a JSON-marshalable value.
type HealthSnapshotter interface {
	Health() any
}

// dashboardData is the aggregate-only payload served at GET /dashboard/data and
// rendered by the dashboard page (#87). Every field is aggregate operational
// data that already exists elsewhere (Prometheus /metrics, stats://*,
// diagnostics://). There is NO per-user, per-query, or tenant-identifiable data:
// tool stats are server-wide, sessions are a count, rate limits are config +
// the default-tenant view, and recent errors are the global redacted ring.
type dashboardData struct {
	GeneratedAt    string                               `json:"generatedAt"`
	Version        string                               `json:"version"`
	Tools          map[string]metrics.ToolStatsSnapshot `json:"tools"`
	ActiveSessions int                                  `json:"activeSessions"`
	RateLimit      dashboardRateLimit                   `json:"rateLimit"`
	Health         any                                  `json:"health,omitempty"`
	RecentErrors   []metrics.ErrorRecord                `json:"recentErrors"`
}

// dashboardRateLimit is the aggregate rate-limit view: the configured ceilings
// plus the default-tenant counters (the only tenant in single-tenant/no-auth
// deployments; an aggregate proxy otherwise). No per-tenant enumeration.
type dashboardRateLimit struct {
	PerMinutePerTenant int                   `json:"perMinutePerTenant"`
	GlobalPerSecond    int                   `json:"globalPerSecond"`
	DailyPerTenant     int                   `json:"dailyPerTenant"`
	DefaultTenant      ratelimit.TenantStats `json:"defaultTenant"`
}

// dashboardNonce returns a fresh base64 CSP nonce per request. crypto/rand
// failure (practically impossible) yields an empty nonce; the page then fails
// closed under the strict CSP rather than weakening the policy.
func dashboardNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b[:])
}

// handleDashboard serves the self-contained operator dashboard HTML. The page
// itself carries NO data — it fetches /dashboard/data (admin-gated) client-side.
// A per-request CSP nonce authorizes exactly the one inline <script>/<style>
// block; combined with connect-src 'self' it keeps the strict
// default-src 'none' posture while allowing the page's own fetch() poll. No
// external/CDN assets, no build step (minimal-dependency rule).
func handleDashboard(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		nonce := dashboardNonce()
		// A dashboard-scoped CSP that overrides the global one for this response
		// only: allow the nonce'd inline script/style and same-origin fetch.
		csp := "default-src 'none'; " +
			"script-src 'nonce-" + nonce + "'; " +
			"style-src 'nonce-" + nonce + "'; " +
			"connect-src 'self'; " +
			"img-src 'self' data:; " +
			"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		page := strings.ReplaceAll(dashboardHTML, "{{NONCE}}", nonce)
		page = strings.ReplaceAll(page, "{{VERSION}}", htmlEscape(version))
		_, _ = w.Write([]byte(page))
	}
}

// handleDashboardData serves the aggregate JSON the dashboard renders. It is
// admin-gated by the same adminAuth wrapper as /admin/* (the caller wires it),
// so it shares the operator trust tier. Aggregate-only by construction.
func handleDashboardData(version string, m *metrics.Collector, sessions session.Manager, rl *ratelimit.Limiter, health HealthSnapshotter) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data := dashboardData{
			GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
			Version:      version,
			RecentErrors: []metrics.ErrorRecord{},
		}
		if m != nil {
			data.Tools = m.GetToolStats()
			// Global operator view of the redacted recent-errors ring (the
			// per-caller, tenant-scoped view is the diagnostics:// Resource).
			if errs := m.RecentErrors(""); errs != nil {
				data.RecentErrors = errs
			}
		}
		if sessions != nil {
			data.ActiveSessions = sessions.ActiveCount()
		}
		if rl != nil {
			cfg := rl.Config()
			data.RateLimit = dashboardRateLimit{
				PerMinutePerTenant: cfg.PerTenant,
				GlobalPerSecond:    cfg.Global,
				DailyPerTenant:     cfg.DailyQuota,
				DefaultTenant:      rl.Stats("default"),
			}
		}
		if health != nil {
			data.Health = health.Health()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(data)
	}
}

// htmlEscape minimally escapes a string for safe interpolation into the HTML
// template's text content / attribute (version string only). Avoids pulling
// html/template for a single trusted-but-escaped value.
func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type Config struct {
	Name    string
	Version string
	Logger  *slog.Logger
}

type HTTPConfig struct {
	Port           int
	Auth           *auth.Middleware
	RateLimiter    *ratelimit.Limiter
	AllowedOrigins []string
	Metrics        *metrics.Collector
	AdminKey       string
	Cache          cache.Cache
	Sessions       session.Manager
	// DataSubjects fans GDPR access/erasure requests out to every registered
	// per-user store (#85). Nil disables the /admin/data endpoints.
	DataSubjects *datasubject.Registry
	// Consent records/verifies/honors consent (#89); backs the consent admin
	// endpoints. Nil (or a Noop) means consent management is inert.
	Consent consent.Manager
	// Auditor records data.export/data.erasure/consent.* events for the admin
	// data-subject and consent endpoints.
	Auditor audit.Auditor

	// CORSStrict, when true (the default), makes an empty AllowedOrigins deny all
	// cross-origin requests (fail-closed). When false, an empty AllowedOrigins
	// keeps the legacy permissive reflect-any-Origin behavior. See docs/MIGRATION.md.
	CORSStrict bool

	// HTTP-server hardening knobs (C1/C2/H4). All ignored in STDIO mode since
	// ServeHTTP only runs when Port>0. Defaults are permissive; WriteTimeout=0
	// in particular keeps long scrape/research responses from being truncated.
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxRequestBody    int
	CSP               string
	ReferrerPolicy    string
	PermissionsPolicy string
}

type Server struct {
	mcpServer *mcp.Server
}

func New(cfg Config) *Server {
	mcpServer := mcp.NewServer(
		&mcp.Implementation{
			Name:    cfg.Name,
			Version: cfg.Version,
		},
		&mcp.ServerOptions{
			Logger: cfg.Logger,
		},
	)

	return &Server{mcpServer: mcpServer}
}

func (s *Server) MCP() *mcp.Server {
	return s.mcpServer
}

func (s *Server) RunSTDIO(ctx context.Context) error {
	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) ServeHTTP(ctx context.Context, cfg HTTPConfig) error {
	handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return s.mcpServer
	}, nil)

	mux := http.NewServeMux()

	// /mcp and /admin carry request bodies; cap them with MaxBytesReader (C2) so
	// an oversized POST is rejected with 413 before it can exhaust memory. A
	// non-positive cap disables the limit (passthrough).
	mux.Handle("/mcp/", maxBytes(cfg.MaxRequestBody, cfg.Auth.Wrap(cfg.RateLimiter.Wrap(handler))))

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ready")
	})

	mux.Handle("GET /metrics", cfg.Metrics.HTTPHandler())

	if cfg.AdminKey != "" {
		mux.Handle("DELETE /admin/cache", maxBytes(cfg.MaxRequestBody, adminAuth(cfg.AdminKey, handleAdminFlushCache(cfg.Cache))))
		mux.Handle("DELETE /admin/sessions", maxBytes(cfg.MaxRequestBody, adminAuth(cfg.AdminKey, handleAdminFlushSessions(cfg.Sessions))))
		mux.Handle("GET /admin/analytics", maxBytes(cfg.MaxRequestBody, adminAuth(cfg.AdminKey, handleAdminTenantAnalytics(cfg.Metrics))))
		if cfg.DataSubjects != nil {
			mux.Handle("GET /admin/data", maxBytes(cfg.MaxRequestBody, adminAuth(cfg.AdminKey, handleAdminDataExport(cfg.DataSubjects, cfg.Auditor))))
			mux.Handle("DELETE /admin/data", maxBytes(cfg.MaxRequestBody, adminAuth(cfg.AdminKey, handleAdminDataErasure(cfg.DataSubjects, cfg.Consent, cfg.Auditor))))
		}
		if cfg.Consent != nil {
			mux.Handle("POST /admin/consent", maxBytes(cfg.MaxRequestBody, adminAuth(cfg.AdminKey, handleAdminConsentRecord(cfg.Consent, cfg.Auditor))))
			mux.Handle("GET /admin/consent", maxBytes(cfg.MaxRequestBody, adminAuth(cfg.AdminKey, handleAdminConsentQuery(cfg.Consent))))
		}
	}

	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":"web-researcher-mcp","token_endpoint":"n/a"}`)
	})

	// Middleware chain (outermost first): per-IP rate limit guards the flood
	// before any other work, then request-ID ingress establishes correlation,
	// then security headers and CORS, then routing. WrapIP is OUTERMOST so an
	// unauthenticated flood is shed before it reaches auth or the mux.
	var root http.Handler = mux
	root = securityHeaders(securityHeadersConfig{
		csp:               cfg.CSP,
		referrerPolicy:    cfg.ReferrerPolicy,
		permissionsPolicy: cfg.PermissionsPolicy,
	}, corsMiddleware(cfg.AllowedOrigins, cfg.CORSStrict, root))
	root = requestIDMiddleware(root)
	root = cfg.RateLimiter.WrapIP(root)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           root,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}

	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()

	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// maxBytes wraps next so request bodies larger than limit bytes are rejected
// (the wrapped handler's Read returns an error the SDK surfaces as 413). A
// non-positive limit disables the cap entirely.
func maxBytes(limit int, next http.Handler) http.Handler {
	if limit <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, int64(limit))
		}
		next.ServeHTTP(w, r)
	})
}

// maxRequestIDLen bounds an adopted inbound request ID so a hostile client
// cannot bloat logs/headers with an unbounded correlation value.
const maxRequestIDLen = 200

// requestIDMiddleware establishes a request correlation ID for every request
// (H6). It adopts a sane inbound X-Request-Id, falling back to the trace-id
// segment of a W3C traceparent header, and otherwise generates a fresh UUIDv4.
// Adopted values are CRLF-stripped and length-clamped so they cannot inject
// response headers or bloat logs. The chosen ID is stored on the context via
// auth.ContextKeyRequestID (for audit correlation) and echoed back on the
// response as X-Request-Id.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get("X-Request-Id"))
		if id == "" {
			id = sanitizeRequestID(traceparentID(r.Header.Get("traceparent")))
		}
		if id == "" {
			id = newUUIDv4()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), auth.ContextKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sanitizeRequestID strips CR/LF (header-injection guard) and trims whitespace,
// then clamps the result to maxRequestIDLen runes.
func sanitizeRequestID(s string) string {
	s = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(s))
	if len(s) > maxRequestIDLen {
		s = s[:maxRequestIDLen]
	}
	return s
}

// traceparentID extracts the 32-hex trace-id field from a W3C traceparent
// header ("version-traceid-spanid-flags"), or "" if the header is malformed.
func traceparentID(tp string) string {
	if tp == "" {
		return ""
	}
	parts := strings.Split(tp, "-")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// newUUIDv4 generates a random RFC 4122 version-4 UUID using crypto/rand. On the
// (practically impossible) event rand.Read fails, it returns a zero-UUID rather
// than panicking, keeping the request path values-not-panics.
func newUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// securityHeadersConfig holds the configurable response security headers. An
// empty value for any field omits the corresponding header.
type securityHeadersConfig struct {
	csp               string
	referrerPolicy    string
	permissionsPolicy string
}

func securityHeaders(cfg securityHeadersConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		if cfg.csp != "" {
			h.Set("Content-Security-Policy", cfg.csp)
		}
		if cfg.referrerPolicy != "" {
			h.Set("Referrer-Policy", cfg.referrerPolicy)
		}
		if cfg.permissionsPolicy != "" {
			h.Set("Permissions-Policy", cfg.permissionsPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware reflects an allowed Origin. With a non-empty allowedOrigins it
// reflects only listed origins (or any when "*" is listed). With an empty
// allowedOrigins the behavior depends on strict: when strict is false (default)
// it preserves the permissive reflect-any-Origin behavior; when strict is true
// it denies all cross-origin requests (fail-closed). It never reflects the
// literal "*" together with credentials.
func corsMiddleware(allowedOrigins []string, strict bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := len(allowedOrigins) == 0 && !strict
			for _, o := range allowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Admin-Key")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func adminAuth(key string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Admin-Key")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}
}

func handleAdminFlushCache(c cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if c != nil {
			c.Flush()
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "cache flushed")
	}
}

func handleAdminFlushSessions(mgr session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if mgr != nil {
			mgr.DeleteAll()
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "sessions flushed")
	}
}

// handleAdminTenantAnalytics serves per-tenant AGGREGATE usage metrics (#91)
// for billing and capacity planning. Aggregate-only — counts, rates, and
// latency keyed by tenant_id, never per-query or per-user content. Optional
// ?tenant_id= filters to one tenant. Operator-gated by the admin key.
func handleAdminTenantAnalytics(m *metrics.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			http.Error(w, "metrics disabled", http.StatusServiceUnavailable)
			return
		}
		stats := m.GetTenantStats(r.URL.Query().Get("tenant_id"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"tenants": stats})
	}
}

// writeJSON writes v as JSON with no-store, used by the admin data endpoints.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleAdminDataExport implements GDPR Art. 15/20 (access + portability): a
// JSON export of everything held for a (tenant_id, user_id) subject, fanned
// across all registered namespaces (#85). tenant_id is required; user_id is
// optional (tenant-only stores like sessions ignore it). Cross-tenant access is
// impossible — the export targets exactly the requested tenant_id.
func handleAdminDataExport(reg *datasubject.Registry, auditor audit.Auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant_id")
		userID := r.URL.Query().Get("user_id")
		if tenantID == "" {
			http.Error(w, "tenant_id is required", http.StatusBadRequest)
			return
		}
		result := reg.Export(r.Context(), datasubject.Subject{TenantID: tenantID, UserID: userID})
		if auditor != nil {
			ev := audit.NewEvent("data.export", tenantID, userID)
			ev.Metadata = map[string]any{"namespaces": reg.Namespaces()}
			auditor.Log(ev)
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// handleAdminDataErasure implements GDPR Art. 17 (erasure): purges everything
// held for a (tenant_id, user_id) subject across all registered namespaces, and
// withdraws any consent for that subject so processing cannot silently resume.
// It records an erasure audit event of the action itself.
func handleAdminDataErasure(reg *datasubject.Registry, consentMgr consent.Manager, auditor audit.Auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant_id")
		userID := r.URL.Query().Get("user_id")
		if tenantID == "" {
			http.Error(w, "tenant_id is required", http.StatusBadRequest)
			return
		}
		subject := datasubject.Subject{TenantID: tenantID, UserID: userID}
		result := reg.Erase(r.Context(), subject)

		// Withdraw consent for every purpose so a later request cannot resume
		// processing against erased data (erasure implies consent revocation).
		if consentMgr != nil && userID != "" && userID != "anonymous" {
			now := time.Now().UTC().Format(time.RFC3339)
			for _, p := range consent.AllPurposes {
				_ = consentMgr.Withdraw(r.Context(), tenantID, userID, p, now)
			}
		}

		if auditor != nil {
			ev := audit.NewEvent("data.erasure", tenantID, userID)
			ev.Metadata = map[string]any{"deleted": result.Deleted}
			auditor.Log(ev)
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// consentRequest is the POST /admin/consent body: record a grant or withdrawal
// asserted by the host on the user's behalf.
type consentRequest struct {
	TenantID     string `json:"tenant_id"`
	UserID       string `json:"user_id"`
	Purpose      string `json:"purpose"`
	Granted      bool   `json:"granted"`
	TermsVersion string `json:"terms_version,omitempty"`
}

// handleAdminConsentRecord records a host-asserted consent decision (#89). The
// server verifies/records/honors; it does not collect consent UI-side.
func handleAdminConsentRecord(mgr consent.Manager, auditor audit.Auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req consentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.TenantID == "" || req.UserID == "" || req.Purpose == "" {
			http.Error(w, "tenant_id, user_id, and purpose are required", http.StatusBadRequest)
			return
		}
		purpose := consent.Purpose(req.Purpose)
		if !purpose.Valid() {
			http.Error(w, "unknown purpose", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		rec := consent.Record{
			TenantID: req.TenantID, UserID: req.UserID, Purpose: purpose,
			Granted: req.Granted, TermsVer: req.TermsVersion, DecidedAt: now,
			DecidedFrom: "admin_api",
		}
		if err := mgr.Record(r.Context(), rec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if auditor != nil {
			evType := consent.EventGrant
			if !req.Granted {
				evType = consent.EventWithdraw
			}
			ev := audit.NewEvent(evType, req.TenantID, req.UserID)
			ev.Metadata = map[string]any{"purpose": req.Purpose, "granted": req.Granted}
			auditor.Log(ev)
		}
		writeJSON(w, http.StatusOK, rec)
	}
}

// handleAdminConsentQuery returns the current consent decision for a subject +
// purpose, or 404 if none recorded.
func handleAdminConsentQuery(mgr consent.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tenantID, userID, purpose := q.Get("tenant_id"), q.Get("user_id"), consent.Purpose(q.Get("purpose"))
		if tenantID == "" || userID == "" || !purpose.Valid() {
			http.Error(w, "tenant_id, user_id, and a valid purpose are required", http.StatusBadRequest)
			return
		}
		rec, ok := mgr.Query(r.Context(), tenantID, userID, purpose)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"found": false})
			return
		}
		writeJSON(w, http.StatusOK, rec)
	}
}

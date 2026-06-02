package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

type contextKey string

const (
	ContextKeyTenantID  contextKey = "tenantID"
	ContextKeyUserID    contextKey = "userID"
	ContextKeySessionID contextKey = "sessionID"
	ContextKeyScopes    contextKey = "scopes"
	ContextKeyRequestID contextKey = "requestID"
	ContextKeySourceIP  contextKey = "sourceIP"
)

// Middleware provides JWT-based authentication for HTTP handlers.
type Middleware struct {
	config     config.OAuthConfig
	jwksCache  *jwksCache
	revoked    *revocationStore
	httpClient *http.Client
	stopCh     chan struct{}
	auditor    audit.Auditor // optional; nil-safe via auditFailure
}

// WithAuditor attaches an audit sink so authentication/authorization rejections
// emit an "auth.failure" event (token spray / admin-key guessing become
// detectable). Returns the receiver for fluent wiring. Passing nil (or never
// calling this) keeps the zero-config behavior — no auditing. Never logs token
// material; only the reason, SourceIP, and the request's correlation ID.
func (m *Middleware) WithAuditor(a audit.Auditor) *Middleware {
	m.auditor = a
	return m
}

// auditFailure emits a single auth.failure event. Nil-safe: a no-op when no
// auditor is wired. reason is a short, non-sensitive cause (never the token).
func (m *Middleware) auditFailure(r *http.Request, reason string) {
	if m.auditor == nil {
		return
	}
	ctx := r.Context()
	ev := audit.NewEvent("auth.failure", TenantIDFromContext(ctx), UserIDFromContext(ctx))
	ev.Success = false
	ev.ErrorCode = reason
	ev.SourceIP = SourceIPFromContext(ctx)
	if rid := RequestIDFromContext(ctx); rid != "" {
		ev.RequestID = rid
	}
	m.auditor.Log(ev)
}

// NewMiddleware creates a new auth middleware. If the config has a non-empty
// IssuerURL, it starts a background goroutine to refresh JWKS periodically.
func NewMiddleware(cfg config.OAuthConfig) *Middleware {
	return NewMiddlewareWithStore(cfg, nil)
}

// NewMiddlewareWithStore is like NewMiddleware but backs the token-revocation
// set with a persist.Store so revocations survive restarts (H2). Passing a nil
// store keeps the pure in-memory zero-config behavior. The in-memory set always
// remains authoritative for the running process; the store adds durability and
// is consulted as an additional source of truth on each check.
func NewMiddlewareWithStore(cfg config.OAuthConfig, store persist.Store) *Middleware {
	refreshInterval := cfg.JWKSRefreshInterval
	if refreshInterval == 0 {
		refreshInterval = 1 * time.Hour
	}

	m := &Middleware{
		config:    cfg,
		jwksCache: newJWKSCache(),
		revoked:   newRevocationStore(store),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		stopCh: make(chan struct{}),
	}

	if cfg.IssuerURL != "" {
		go m.refreshLoop(refreshInterval)
	}

	return m
}

// Stop terminates the background JWKS refresh goroutine.
func (m *Middleware) Stop() {
	close(m.stopCh)
}

// RevokeToken adds a JTI to the revocation set. The expiry parameter indicates
// when the token would naturally expire, so the revocation entry can be cleaned up.
func (m *Middleware) RevokeToken(jti string, expiry time.Time) {
	m.revoked.add(jti, expiry)
}

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	if m.config.IssuerURL == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ContextKeyTenantID, "default")
			ctx = context.WithValue(ctx, ContextKeyUserID, "anonymous")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			m.auditFailure(r, "missing_authorization_header")
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			m.auditFailure(r, "invalid_authorization_scheme")
			http.Error(w, "invalid authorization scheme", http.StatusUnauthorized)
			return
		}

		claims, err := m.validateToken(token)
		if err != nil {
			m.auditFailure(r, "invalid_token")
			http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, ContextKeyTenantID, claims.TenantID)
		ctx = context.WithValue(ctx, ContextKeyUserID, claims.UserID)
		if claims.SessionID != "" {
			ctx = context.WithValue(ctx, ContextKeySessionID, claims.SessionID)
		}
		// Always attach the parsed scopes (possibly empty) so the downstream
		// tools/call scope gate can distinguish "no scope claim" (allow) from
		// "scope claim present but insufficient" (reject).
		ctx = context.WithValue(ctx, ContextKeyScopes, claims.Scopes)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Claims holds the extracted identity from a validated JWT.
type Claims struct {
	TenantID  string
	UserID    string
	SessionID string
	// Scopes holds the union of the space-delimited "scope" claim and the
	// array "scp" claim. Nil/empty means the token carried no scope claim.
	Scopes []string
}

// validateToken performs full RS256 JWT validation.
func (m *Middleware) validateToken(rawToken string) (*Claims, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed JWT: expected 3 parts")
	}

	header, err := m.parseAndVerifyHeader(parts[0])
	if err != nil {
		return nil, err
	}

	if err := m.verifySignature(header.Kid, parts); err != nil {
		return nil, err
	}

	payload, err := m.parsePayload(parts[1])
	if err != nil {
		return nil, err
	}

	if err := m.validateClaims(payload); err != nil {
		return nil, err
	}

	return m.extractClaims(payload), nil
}

func (m *Middleware) parseAndVerifyHeader(encoded string) (*jwtHeader, error) {
	headerJSON, err := base64URLDecode(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm: %s", header.Alg)
	}

	return &header, nil
}

func (m *Middleware) verifySignature(kid string, parts []string) error {
	key, err := m.jwksCache.getKey(kid)
	if err != nil {
		if fetchErr := m.fetchJWKS(); fetchErr != nil {
			return fmt.Errorf("fetch JWKS: %w", fetchErr)
		}
		key, err = m.jwksCache.getKey(kid)
		if err != nil {
			return fmt.Errorf("key not found: %s", kid)
		}
	}

	signature, err := base64URLDecode(parts[2])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature); err != nil {
		return errors.New("invalid signature")
	}

	return nil
}

func (m *Middleware) parsePayload(encoded string) (*jwtPayload, error) {
	payloadJSON, err := base64URLDecode(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var payload jwtPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}

	return &payload, nil
}

func (m *Middleware) validateClaims(payload *jwtPayload) error {
	now := time.Now().Unix()

	if payload.Exp == 0 {
		return errors.New("missing exp claim")
	}
	if now > payload.Exp {
		return errors.New("token expired")
	}
	if payload.Iat != 0 && payload.Iat > now+60 {
		return errors.New("token issued in the future")
	}
	if payload.Nbf != 0 && now < payload.Nbf-60 {
		return errors.New("token not yet valid")
	}
	if payload.Iss != m.config.IssuerURL {
		return fmt.Errorf("issuer mismatch: got %q, want %q", payload.Iss, m.config.IssuerURL)
	}
	if !payload.hasAudience(m.config.Audience) {
		return fmt.Errorf("audience mismatch: %q not in %v", m.config.Audience, payload.Aud)
	}
	if payload.Jti != "" && m.revoked.isRevoked(payload.Jti) {
		return errors.New("token has been revoked")
	}

	return nil
}

func (m *Middleware) extractClaims(payload *jwtPayload) *Claims {
	claims := &Claims{
		TenantID:  payload.TenantID,
		UserID:    payload.Sub,
		SessionID: payload.SessionID,
		Scopes:    payload.scopes(),
	}

	if claims.TenantID == "" {
		claims.TenantID = "default"
	}
	if claims.UserID == "" {
		claims.UserID = "anonymous"
	}

	return claims
}

// refreshLoop periodically fetches fresh JWKS and cleans up expired revocations.
func (m *Middleware) refreshLoop(interval time.Duration) {
	// Initial fetch
	_ = m.fetchJWKS()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = m.fetchJWKS()
			m.revoked.cleanup()
		case <-m.stopCh:
			return
		}
	}
}

// fetchJWKS retrieves the JWKS document from the issuer's well-known endpoint.
func (m *Middleware) fetchJWKS() error {
	url := strings.TrimRight(m.config.IssuerURL, "/") + "/.well-known/jwks.json"

	resp, err := m.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return fmt.Errorf("read JWKS body: %w", err)
	}

	var jwks jwksDocument
	if err := json.Unmarshal(body, &jwks); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		if k.Alg != "" && k.Alg != "RS256" {
			continue
		}

		pubKey, err := parseRSAPublicKey(k)
		if err != nil {
			continue
		}
		keys[k.Kid] = pubKey
	}

	m.jwksCache.setKeys(keys)
	return nil
}

// --- JWKS types and cache ---

type jwksDocument struct {
	Keys []jwksKey `json:"keys"`
}

type jwksKey struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksCache struct {
	mu   sync.RWMutex
	keys map[string]*rsa.PublicKey
}

func newJWKSCache() *jwksCache {
	return &jwksCache{
		keys: make(map[string]*rsa.PublicKey),
	}
}

func (c *jwksCache) getKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %q not found in cache", kid)
	}
	return key, nil
}

func (c *jwksCache) setKeys(keys map[string]*rsa.PublicKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = keys
}

func parseRSAPublicKey(k jwksKey) (*rsa.PublicKey, error) {
	nBytes, err := base64URLDecode(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64URLDecode(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	if !e.IsInt64() {
		return nil, errors.New("exponent too large")
	}

	return &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}, nil
}

// --- JWT types ---

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

type jwtPayload struct {
	Iss       string   `json:"iss"`
	Sub       string   `json:"sub"`
	Aud       audience `json:"aud"`
	Exp       int64    `json:"exp"`
	Nbf       int64    `json:"nbf"`
	Iat       int64    `json:"iat"`
	Jti       string   `json:"jti"`
	TenantID  string   `json:"tenant_id"`
	SessionID string   `json:"session_id"`
	// Scope is the OAuth 2.0 space-delimited scope string (RFC 8693 / RFC 6749).
	Scope string `json:"scope"`
	// Scp is the array-form scope claim used by some IdPs (e.g. Azure AD, Okta).
	Scp scopeList `json:"scp"`
}

// scopes returns the de-duplicated union of the space-delimited "scope" claim
// and the array "scp" claim, preserving first-seen order. An empty result means
// the token carried no scope claim at all.
func (p *jwtPayload) scopes() []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range strings.Fields(p.Scope) {
		add(s)
	}
	for _, s := range p.Scp {
		add(s)
	}
	return out
}

// scopeList tolerates the "scp" claim arriving as either a JSON array of
// strings or a single space-delimited string, mirroring the audience handling.
type scopeList []string

func (s *scopeList) UnmarshalJSON(data []byte) error {
	var multi []string
	if err := json.Unmarshal(data, &multi); err == nil {
		*s = scopeList(multi)
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	*s = scopeList(strings.Fields(single))
	return nil
}

// audience handles the fact that "aud" can be a string or array of strings.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = audience{single}
		return nil
	}

	var multi []string
	if err := json.Unmarshal(data, &multi); err != nil {
		return err
	}
	*a = audience(multi)
	return nil
}

func (p *jwtPayload) hasAudience(expected string) bool {
	for _, a := range p.Aud {
		if a == expected {
			return true
		}
	}
	return false
}

// --- Revocation store ---

type revocationEntry struct {
	expiry time.Time
}

// revocationKeyPrefix namespaces revocation entries within a shared persist.Store
// so they cannot collide with other subsystems (e.g. rate-limit quotas).
const revocationKeyPrefix = "auth/revoked/"

// revocationStore tracks revoked JTIs. The in-memory map is always authoritative
// for the running process; an optional persist.Store (H2) adds durability across
// restarts and is consulted as an additional source of truth. A JTI is treated
// as revoked if it is present in EITHER the memory map or the backing store
// (fail-closed: a revocation is never lost because one layer missed it).
type revocationStore struct {
	mu      sync.RWMutex
	entries map[string]revocationEntry
	store   persist.Store
}

func newRevocationStore(store persist.Store) *revocationStore {
	return &revocationStore{
		entries: make(map[string]revocationEntry),
		store:   store,
	}
}

func (s *revocationStore) add(jti string, expiry time.Time) {
	s.mu.Lock()
	s.entries[jti] = revocationEntry{expiry: expiry}
	store := s.store
	s.mu.Unlock()

	if store != nil {
		// Persist with a TTL matching natural token expiry so the backing
		// store self-cleans. A non-positive TTL would store without expiry;
		// guard against already-expired inputs by skipping the write.
		ttl := time.Until(expiry)
		if ttl > 0 {
			store.Set(context.Background(), revocationKeyPrefix+jti, []byte{1}, ttl)
		}
	}
}

func (s *revocationStore) isRevoked(jti string) bool {
	s.mu.RLock()
	_, ok := s.entries[jti]
	store := s.store
	s.mu.RUnlock()

	if ok {
		return true
	}
	if store != nil {
		if _, found := store.Get(context.Background(), revocationKeyPrefix+jti); found {
			return true
		}
	}
	return false
}

func (s *revocationStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for jti, entry := range s.entries {
		if now.After(entry.expiry) {
			delete(s.entries, jti)
		}
	}
	// The backing store self-expires entries via their TTL, so no explicit
	// store cleanup is required here.
}

// --- Helpers ---

func base64URLDecode(s string) ([]byte, error) {
	// Add padding if necessary
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// --- Context helpers ---

func TenantIDFromContext(ctx context.Context) string {
	if v := ctx.Value(ContextKeyTenantID); v != nil {
		return v.(string)
	}
	return "default"
}

func UserIDFromContext(ctx context.Context) string {
	if v := ctx.Value(ContextKeyUserID); v != nil {
		return v.(string)
	}
	return "anonymous"
}

// ScopesFromContext returns the scopes attached to the request context by Wrap,
// or nil if none were set (no auth, or a token without a scope claim). It is
// empty-safe: a nil context value or a value of an unexpected type yields nil.
func ScopesFromContext(ctx context.Context) []string {
	if v := ctx.Value(ContextKeyScopes); v != nil {
		if scopes, ok := v.([]string); ok {
			return scopes
		}
	}
	return nil
}

// RequestIDFromContext returns the request correlation ID attached to the
// context (by the transport-layer ingress middleware), or "" if none is set.
// Empty-safe for nil values and unexpected types.
func RequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(ContextKeyRequestID); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// SourceIPFromContext returns the client IP attached by the HTTP ingress
// middleware (proxy-aware), or "" if none is set (e.g. STDIO transport, where
// there is no network peer). Empty-safe for nil values and unexpected types.
func SourceIPFromContext(ctx context.Context) string {
	if v := ctx.Value(ContextKeySourceIP); v != nil {
		if ip, ok := v.(string); ok {
			return ip
		}
	}
	return ""
}

// EnforceScopes decides whether a caller carrying the given token scopes may
// invoke the named tool. It centralizes the scope-gate policy so the SDK
// receiving-middleware (wired in main.go) and any future caller share one
// implementation. It returns nil to allow, or a non-nil error describing the
// missing scope to deny.
//
// Policy (permissive by default, fail-closed only on present-but-insufficient):
//   - ENFORCE_SCOPES=false              => always allow.
//   - ENFORCE_SCOPES=true, no scopes    => allow (backward-compatible: tokens
//     issued before scopes existed keep working).
//   - ENFORCE_SCOPES=true, scopes set   => require one of: the wildcard
//     "tool:*", the exact "tool:<toolName>", or the coarse-grained "research"
//     scope; AND every entry in REQUIRED_SCOPES (if configured) must be present.
//     Otherwise reject.
func (m *Middleware) EnforceScopes(scopes []string, toolName string) error {
	if !m.config.EnforceScopes {
		return nil
	}
	if len(scopes) == 0 {
		// Token carried no scope claim: permissive, backward-compatible.
		return nil
	}

	have := make(map[string]struct{}, len(scopes))
	for _, s := range scopes {
		have[s] = struct{}{}
	}

	// Every explicitly required scope must be present.
	for _, req := range m.config.RequiredScopes {
		if _, ok := have[req]; !ok {
			return fmt.Errorf("missing required scope %q", req)
		}
	}

	// Per-tool authorization: wildcard, exact tool scope, or coarse "research".
	if _, ok := have["tool:*"]; ok {
		return nil
	}
	if _, ok := have["tool:"+toolName]; ok {
		return nil
	}
	if _, ok := have["research"]; ok {
		return nil
	}

	return fmt.Errorf("insufficient scope for tool %q: require one of tool:*, tool:%s, or research", toolName, toolName)
}

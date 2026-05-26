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

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

type contextKey string

const (
	ContextKeyTenantID  contextKey = "tenantID"
	ContextKeyUserID    contextKey = "userID"
	ContextKeySessionID contextKey = "sessionID"
)

// Middleware provides JWT-based authentication for HTTP handlers.
type Middleware struct {
	config     config.OAuthConfig
	jwksCache  *jwksCache
	revoked    *revocationStore
	httpClient *http.Client
	stopCh     chan struct{}
}

// NewMiddleware creates a new auth middleware. If the config has a non-empty
// IssuerURL, it starts a background goroutine to refresh JWKS periodically.
func NewMiddleware(cfg config.OAuthConfig) *Middleware {
	refreshInterval := cfg.JWKSRefreshInterval
	if refreshInterval == 0 {
		refreshInterval = 1 * time.Hour
	}

	m := &Middleware{
		config:    cfg,
		jwksCache: newJWKSCache(),
		revoked:   newRevocationStore(),
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
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			http.Error(w, "invalid authorization scheme", http.StatusUnauthorized)
			return
		}

		claims, err := m.validateToken(token)
		if err != nil {
			http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, ContextKeyTenantID, claims.TenantID)
		ctx = context.WithValue(ctx, ContextKeyUserID, claims.UserID)
		if claims.SessionID != "" {
			ctx = context.WithValue(ctx, ContextKeySessionID, claims.SessionID)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Claims holds the extracted identity from a validated JWT.
type Claims struct {
	TenantID  string
	UserID    string
	SessionID string
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

type revocationStore struct {
	mu      sync.RWMutex
	entries map[string]revocationEntry
}

func newRevocationStore() *revocationStore {
	return &revocationStore{
		entries: make(map[string]revocationEntry),
	}
}

func (s *revocationStore) add(jti string, expiry time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[jti] = revocationEntry{expiry: expiry}
}

func (s *revocationStore) isRevoked(jti string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.entries[jti]
	return ok
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

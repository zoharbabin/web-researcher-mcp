package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

// testKey generates an RSA key pair for testing.
func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

// jwksHandler returns an http.Handler that serves the public key as JWKS.
func jwksHandler(t *testing.T, kid string, pub *rsa.PublicKey) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}

		nBytes := pub.N.Bytes()
		eBytes := big.NewInt(int64(pub.E)).Bytes()

		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"use": "sig",
					"kid": kid,
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(nBytes),
					"e":   base64.RawURLEncoding.EncodeToString(eBytes),
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	})
}

// signJWT creates a signed RS256 JWT from the given header and payload maps.
func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, payload map[string]interface{}) string {
	t.Helper()

	header := map[string]interface{}{
		"alg": "RS256",
		"typ": "JWT",
		"kid": kid,
	}

	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(payload)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := headerB64 + "." + payloadB64
	hash := sha256.Sum256([]byte(signingInput))

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return headerB64 + "." + payloadB64 + "." + sigB64
}

func setupMiddleware(t *testing.T, key *rsa.PrivateKey, kid string) (*Middleware, *httptest.Server) {
	t.Helper()

	jwksSrv := httptest.NewServer(jwksHandler(t, kid, &key.PublicKey))
	t.Cleanup(jwksSrv.Close)

	cfg := config.OAuthConfig{
		IssuerURL:           jwksSrv.URL,
		Audience:            "mcp-server",
		JWKSRefreshInterval: 1 * time.Hour,
	}

	m := NewMiddleware(cfg)
	t.Cleanup(m.Stop)

	// Manually fetch JWKS so tests don't race with the background goroutine
	if err := m.fetchJWKS(); err != nil {
		t.Fatalf("initial JWKS fetch: %v", err)
	}

	return m, jwksSrv
}

func validPayload(issuer string) map[string]interface{} {
	now := time.Now().Unix()
	return map[string]interface{}{
		"iss":        issuer,
		"sub":        "user-42",
		"aud":        "mcp-server",
		"exp":        now + 3600,
		"iat":        now - 10,
		"jti":        "token-id-1",
		"tenant_id":  "tenant-abc",
		"session_id": "sess-xyz",
	}
}

func TestMiddlewareNoAuth(t *testing.T) {
	m := NewMiddleware(config.OAuthConfig{})
	defer m.Stop()

	var capturedTenantID, capturedUserID string
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTenantID = TenantIDFromContext(r.Context())
		capturedUserID = UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if capturedTenantID != "default" {
		t.Fatalf("expected tenant 'default', got %q", capturedTenantID)
	}
	if capturedUserID != "anonymous" {
		t.Fatalf("expected user 'anonymous', got %q", capturedUserID)
	}
}

func TestMiddlewareRequiresAuthHeader(t *testing.T) {
	key := testKey(t)
	m, _ := setupMiddleware(t, key, "key-1")

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareInvalidScheme(t *testing.T) {
	key := testKey(t)
	m, _ := setupMiddleware(t, key, "key-1")

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareValidToken(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	var capturedTenantID, capturedUserID, capturedSessionID string
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTenantID = TenantIDFromContext(r.Context())
		capturedUserID = UserIDFromContext(r.Context())
		if v := r.Context().Value(ContextKeySessionID); v != nil {
			capturedSessionID = v.(string)
		}
		w.WriteHeader(http.StatusOK)
	}))

	token := signJWT(t, key, kid, validPayload(jwksSrv.URL))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if capturedTenantID != "tenant-abc" {
		t.Fatalf("expected tenant 'tenant-abc', got %q", capturedTenantID)
	}
	if capturedUserID != "user-42" {
		t.Fatalf("expected user 'user-42', got %q", capturedUserID)
	}
	if capturedSessionID != "sess-xyz" {
		t.Fatalf("expected session 'sess-xyz', got %q", capturedSessionID)
	}
}

func TestMiddlewareExpiredToken(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload(jwksSrv.URL)
	payload["exp"] = time.Now().Unix() - 100 // expired 100 seconds ago

	token := signJWT(t, key, kid, payload)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token expired") {
		t.Fatalf("expected 'token expired' error, got: %s", rec.Body.String())
	}
}

func TestMiddlewareWrongAudience(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload(jwksSrv.URL)
	payload["aud"] = "wrong-audience"

	token := signJWT(t, key, kid, payload)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "audience mismatch") {
		t.Fatalf("expected 'audience mismatch' error, got: %s", rec.Body.String())
	}
}

func TestMiddlewareWrongIssuer(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, _ := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload("https://wrong-issuer.example.com")

	token := signJWT(t, key, kid, payload)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "issuer mismatch") {
		t.Fatalf("expected 'issuer mismatch' error, got: %s", rec.Body.String())
	}
}

func TestMiddlewareRevokedToken(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload(jwksSrv.URL)
	payload["jti"] = "revoked-token-id"

	token := signJWT(t, key, kid, payload)

	// Revoke the token
	m.RevokeToken("revoked-token-id", time.Now().Add(1*time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "revoked") {
		t.Fatalf("expected 'revoked' error, got: %s", rec.Body.String())
	}
}

func TestMiddlewareInvalidSignature(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Sign with a different key
	wrongKey := testKey(t)
	token := signJWT(t, wrongKey, kid, validPayload(jwksSrv.URL))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid signature") {
		t.Fatalf("expected 'invalid signature' error, got: %s", rec.Body.String())
	}
}

func TestMiddlewareMalformedToken(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, _ := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt.token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareAudienceAsArray(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload(jwksSrv.URL)
	payload["aud"] = []string{"other-service", "mcp-server"}

	token := signJWT(t, key, kid, payload)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestRevocationStoreCleanup(t *testing.T) {
	store := newRevocationStore()

	store.add("expired-jti", time.Now().Add(-1*time.Hour))
	store.add("valid-jti", time.Now().Add(1*time.Hour))

	store.cleanup()

	if store.isRevoked("expired-jti") {
		t.Fatal("expected expired revocation to be cleaned up")
	}
	if !store.isRevoked("valid-jti") {
		t.Fatal("expected valid revocation to still exist")
	}
}

func TestMiddlewareKeyRotation(t *testing.T) {
	// Start with one key, then rotate to a new key
	key1 := testKey(t)
	key2 := testKey(t)
	kid1 := "key-1"
	kid2 := "key-2"

	var mu sync.Mutex
	currentKid := kid1
	currentKey := &key1.PublicKey

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}

		mu.Lock()
		pub := currentKey
		kid := currentKid
		mu.Unlock()

		nBytes := pub.N.Bytes()
		eBytes := big.NewInt(int64(pub.E)).Bytes()

		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"use": "sig",
					"kid": kid,
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(nBytes),
					"e":   base64.RawURLEncoding.EncodeToString(eBytes),
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksSrv.Close()

	cfg := config.OAuthConfig{
		IssuerURL:           jwksSrv.URL,
		Audience:            "mcp-server",
		JWKSRefreshInterval: 1 * time.Hour,
	}

	m := NewMiddleware(cfg)
	defer m.Stop()

	if err := m.fetchJWKS(); err != nil {
		t.Fatalf("initial JWKS fetch: %v", err)
	}

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Token signed with key1 should work
	token1 := signJWT(t, key1, kid1, validPayload(jwksSrv.URL))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token1)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for key1 token, got %d: %s", rec.Code, rec.Body.String())
	}

	// Rotate key
	mu.Lock()
	currentKid = kid2
	currentKey = &key2.PublicKey
	mu.Unlock()

	// Token signed with key2 should work after JWKS re-fetch (triggered by cache miss)
	token2 := signJWT(t, key2, kid2, validPayload(jwksSrv.URL))
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token2)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for key2 token after rotation, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantIDFromContext(t *testing.T) {
	ctx := context.Background()
	if TenantIDFromContext(ctx) != "default" {
		t.Fatal("expected 'default' for missing context value")
	}

	ctx = context.WithValue(ctx, ContextKeyTenantID, "my-tenant")
	if TenantIDFromContext(ctx) != "my-tenant" {
		t.Fatal("expected 'my-tenant'")
	}
}

func TestUserIDFromContext(t *testing.T) {
	ctx := context.Background()
	if UserIDFromContext(ctx) != "anonymous" {
		t.Fatal("expected 'anonymous' for missing context value")
	}

	ctx = context.WithValue(ctx, ContextKeyUserID, "user-123")
	if UserIDFromContext(ctx) != "user-123" {
		t.Fatal("expected 'user-123'")
	}
}

func TestMiddlewareMissingExpClaim(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload(jwksSrv.URL)
	delete(payload, "exp")

	token := signJWT(t, key, kid, payload)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing exp") {
		t.Fatalf("expected 'missing exp' error, got: %s", rec.Body.String())
	}
}

func TestMiddlewareUnsupportedAlgorithm(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Manually craft a token with HS256 header
	header := map[string]interface{}{
		"alg": "HS256",
		"typ": "JWT",
		"kid": kid,
	}
	payload := validPayload(jwksSrv.URL)

	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(payload)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	fakeSig := base64.RawURLEncoding.EncodeToString([]byte("fakesignature"))

	token := fmt.Sprintf("%s.%s.%s", headerB64, payloadB64, fakeSig)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unsupported algorithm") {
		t.Fatalf("expected 'unsupported algorithm' error, got: %s", rec.Body.String())
	}
}

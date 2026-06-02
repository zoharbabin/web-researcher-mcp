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

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
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
	store := newRevocationStore(nil)

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

func TestScopesFromContext(t *testing.T) {
	t.Parallel()

	// Empty-safe: no value set.
	if got := ScopesFromContext(context.Background()); got != nil {
		t.Fatalf("expected nil for missing scopes, got %v", got)
	}

	// Empty-safe: wrong type stored under the key.
	wrong := context.WithValue(context.Background(), ContextKeyScopes, "not-a-slice")
	if got := ScopesFromContext(wrong); got != nil {
		t.Fatalf("expected nil for wrong type, got %v", got)
	}

	// Round-trip.
	want := []string{"tool:web_search", "research"}
	ctx := context.WithValue(context.Background(), ContextKeyScopes, want)
	got := ScopesFromContext(ctx)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestRequestIDFromContext(t *testing.T) {
	t.Parallel()

	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty for missing request ID, got %q", got)
	}

	wrong := context.WithValue(context.Background(), ContextKeyRequestID, 123)
	if got := RequestIDFromContext(wrong); got != "" {
		t.Fatalf("expected empty for wrong type, got %q", got)
	}

	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-abc")
	if got := RequestIDFromContext(ctx); got != "req-abc" {
		t.Fatalf("expected 'req-abc', got %q", got)
	}
}

func TestJWTPayloadScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    []string
	}{
		{
			name:    "no scope claim",
			payload: `{}`,
			want:    nil,
		},
		{
			name:    "space-delimited scope string",
			payload: `{"scope":"tool:web_search research"}`,
			want:    []string{"tool:web_search", "research"},
		},
		{
			name:    "scp as array",
			payload: `{"scp":["tool:scrape_page","tool:*"]}`,
			want:    []string{"tool:scrape_page", "tool:*"},
		},
		{
			name:    "scp as space-delimited string",
			payload: `{"scp":"a b c"}`,
			want:    []string{"a", "b", "c"},
		},
		{
			name:    "union of scope and scp deduplicated, order preserved",
			payload: `{"scope":"research tool:web_search","scp":["tool:web_search","tool:patent_search"]}`,
			want:    []string{"research", "tool:web_search", "tool:patent_search"},
		},
		{
			name:    "extra whitespace ignored",
			payload: `{"scope":"  research   tool:x  "}`,
			want:    []string{"research", "tool:x"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p jwtPayload
			if err := json.Unmarshal([]byte(tt.payload), &p); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			got := p.scopes()
			if len(got) != len(tt.want) {
				t.Fatalf("scopes() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("scopes()[%d] = %q, want %q (full: %v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestEnforceScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		enforce   bool
		required  []string
		scopes    []string
		tool      string
		wantAllow bool
	}{
		{
			name:      "enforcement off allows everything",
			enforce:   false,
			scopes:    nil,
			tool:      "web_search",
			wantAllow: true,
		},
		{
			name:      "enforcement off allows even with unrelated scopes",
			enforce:   false,
			scopes:    []string{"tool:other"},
			tool:      "web_search",
			wantAllow: true,
		},
		{
			name:      "enforced but no scope claim allows (backward-compat)",
			enforce:   true,
			scopes:    nil,
			tool:      "web_search",
			wantAllow: true,
		},
		{
			name:      "enforced exact tool scope allows",
			enforce:   true,
			scopes:    []string{"tool:web_search"},
			tool:      "web_search",
			wantAllow: true,
		},
		{
			name:      "enforced exact tool scope rejects other tool",
			enforce:   true,
			scopes:    []string{"tool:web_search"},
			tool:      "scrape_page",
			wantAllow: false,
		},
		{
			name:      "enforced wildcard allows any tool",
			enforce:   true,
			scopes:    []string{"tool:*"},
			tool:      "scrape_page",
			wantAllow: true,
		},
		{
			name:      "enforced coarse research scope allows",
			enforce:   true,
			scopes:    []string{"research"},
			tool:      "patent_search",
			wantAllow: true,
		},
		{
			name:      "enforced insufficient scope rejects",
			enforce:   true,
			scopes:    []string{"tool:image_search"},
			tool:      "web_search",
			wantAllow: false,
		},
		{
			name:      "required scope present and tool scope present allows",
			enforce:   true,
			required:  []string{"research"},
			scopes:    []string{"research", "tool:web_search"},
			tool:      "web_search",
			wantAllow: true,
		},
		{
			name:      "required scope missing rejects even with tool scope",
			enforce:   true,
			required:  []string{"audit"},
			scopes:    []string{"tool:*"},
			tool:      "web_search",
			wantAllow: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Middleware{config: config.OAuthConfig{
				EnforceScopes:  tt.enforce,
				RequiredScopes: tt.required,
			}}
			err := m.EnforceScopes(tt.scopes, tt.tool)
			if tt.wantAllow && err != nil {
				t.Fatalf("expected allow, got error: %v", err)
			}
			if !tt.wantAllow && err == nil {
				t.Fatal("expected reject, got allow")
			}
		})
	}
}

func TestMiddlewareInjectsScopesIntoContext(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)

	var captured []string
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = ScopesFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload(jwksSrv.URL)
	payload["scope"] = "research tool:web_search"

	token := signJWT(t, key, kid, payload)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if len(captured) != 2 || captured[0] != "research" || captured[1] != "tool:web_search" {
		t.Fatalf("expected scopes [research tool:web_search], got %v", captured)
	}
}

func TestRevocationStoreNilStore(t *testing.T) {
	t.Parallel()

	// A nil backing store preserves pure in-memory behavior.
	s := newRevocationStore(nil)
	s.add("jti-1", time.Now().Add(time.Hour))
	if !s.isRevoked("jti-1") {
		t.Fatal("expected jti-1 revoked in memory")
	}
	if s.isRevoked("jti-2") {
		t.Fatal("expected jti-2 not revoked")
	}
}

func TestRevocationStorePersistsAcrossRestart(t *testing.T) {
	t.Parallel()

	// Shared store simulates durable backend surviving a process restart.
	shared := persist.NewMemoryStore()

	first := newRevocationStore(shared)
	first.add("durable-jti", time.Now().Add(time.Hour))
	if !first.isRevoked("durable-jti") {
		t.Fatal("expected revocation visible in first store")
	}

	// "Restart": a brand-new revocation store with empty memory but the same
	// backing store must still see the revocation (H2 fail-closed durability).
	second := newRevocationStore(shared)
	if _, ok := second.entries["durable-jti"]; ok {
		t.Fatal("memory map should be empty after restart")
	}
	if !second.isRevoked("durable-jti") {
		t.Fatal("expected revocation to survive restart via backing store")
	}
}

func TestRevocationStoreExpiredNotPersisted(t *testing.T) {
	t.Parallel()

	shared := persist.NewMemoryStore()
	s := newRevocationStore(shared)

	// An already-expired expiry yields a non-positive TTL; nothing is written
	// to the backing store, but the in-memory entry still exists until cleanup.
	s.add("expired-jti", time.Now().Add(-time.Hour))
	if _, ok := shared.Get(context.Background(), revocationKeyPrefix+"expired-jti"); ok {
		t.Fatal("expected expired revocation not to be persisted")
	}

	// A fresh store with only the backing store must not see it.
	fresh := newRevocationStore(shared)
	if fresh.isRevoked("expired-jti") {
		t.Fatal("expected expired-jti not revoked via backing store")
	}
}

func TestMiddlewareRevocationViaStore(t *testing.T) {
	key := testKey(t)
	kid := "key-1"

	jwksSrv := httptest.NewServer(jwksHandler(t, kid, &key.PublicKey))
	t.Cleanup(jwksSrv.Close)

	cfg := config.OAuthConfig{
		IssuerURL:           jwksSrv.URL,
		Audience:            "mcp-server",
		JWKSRefreshInterval: 1 * time.Hour,
	}

	shared := persist.NewMemoryStore()

	// First middleware instance revokes the token.
	m1 := NewMiddlewareWithStore(cfg, shared)
	t.Cleanup(m1.Stop)
	m1.RevokeToken("store-revoked-jti", time.Now().Add(time.Hour))

	// Second instance ("after restart") shares the backing store but has empty
	// in-memory state; it must still reject the revoked token.
	m2 := NewMiddlewareWithStore(cfg, shared)
	t.Cleanup(m2.Stop)
	if err := m2.fetchJWKS(); err != nil {
		t.Fatalf("initial JWKS fetch: %v", err)
	}

	handler := m2.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	payload := validPayload(jwksSrv.URL)
	payload["jti"] = "store-revoked-jti"
	token := signJWT(t, key, kid, payload)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for store-backed revocation, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "revoked") {
		t.Fatalf("expected 'revoked' error, got: %s", rec.Body.String())
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

// captureAuditor is a test Auditor that records every event it receives.
type captureAuditor struct {
	mu     sync.Mutex
	events []audit.AuditEvent
}

func (c *captureAuditor) Log(e audit.AuditEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}
func (c *captureAuditor) IncludeRequestBody() bool { return false }
func (c *captureAuditor) Close()                   {}
func (c *captureAuditor) all() []audit.AuditEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]audit.AuditEvent(nil), c.events...)
}

func TestWrapAuditsAuthFailures(t *testing.T) {
	key := testKey(t)
	kid := "key-1"
	m, jwksSrv := setupMiddleware(t, key, kid)
	aud := &captureAuditor{}
	m.WithAuditor(aud)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name       string
		setup      func(*http.Request)
		wantReason string
	}{
		{"missing header", func(r *http.Request) {}, "missing_authorization_header"},
		{"bad scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic xyz") }, "invalid_authorization_scheme"},
		{"invalid token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer not.a.jwt") }, "invalid_token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aud.events = nil
			req := httptest.NewRequest(http.MethodPost, "/mcp/", nil)
			tc.setup(req)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
			evs := aud.all()
			if len(evs) != 1 {
				t.Fatalf("expected exactly 1 auth.failure event, got %d", len(evs))
			}
			ev := evs[0]
			if ev.EventType != "auth.failure" {
				t.Errorf("event type = %q, want auth.failure", ev.EventType)
			}
			if ev.Success {
				t.Error("auth.failure event must have Success=false")
			}
			if ev.ErrorCode != tc.wantReason {
				t.Errorf("reason = %q, want %q", ev.ErrorCode, tc.wantReason)
			}
		})
	}

	// A SUCCESSFUL auth must emit NO auth.failure event.
	t.Run("success emits nothing", func(t *testing.T) {
		aud.events = nil
		token := signJWT(t, key, kid, validPayload(jwksSrv.URL))
		req := httptest.NewRequest(http.MethodPost, "/mcp/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if n := len(aud.all()); n != 0 {
			t.Errorf("successful auth emitted %d events, want 0", n)
		}
	})
}

func TestWithIdentity(t *testing.T) {
	base := context.WithValue(context.Background(), ContextKeyScopes, []string{"research"})
	ctx := WithIdentity(base, "tenant-7", "carol")

	if got := TenantIDFromContext(ctx); got != "tenant-7" {
		t.Errorf("tenant = %q, want tenant-7", got)
	}
	if got := UserIDFromContext(ctx); got != "carol" {
		t.Errorf("user = %q, want carol", got)
	}
	// Unrelated keys are preserved.
	if got := ScopesFromContext(ctx); len(got) != 1 || got[0] != "research" {
		t.Errorf("scopes not preserved: %v", got)
	}
	// A fresh empty context defaults remain when nothing set.
	if got := UserIDFromContext(context.Background()); got != "anonymous" {
		t.Errorf("empty ctx user = %q, want anonymous", got)
	}
}

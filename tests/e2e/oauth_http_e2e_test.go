//go:build e2e

package e2e

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests stand up an in-process JWKS server, start the binary in HTTP mode
// with OAuth enabled (OAUTH_ISSUER_URL pointed at the JWKS server), and assert
// the full auth boundary end-to-end: unauthenticated and malformed requests are
// rejected at the transport with 401, a valid RS256 token is accepted, and the
// scope gate denies an insufficient-scope token as an MCP IsError result (not a
// protocol error) so an LLM client can see and self-correct.
//
// JWT/JWKS helpers are ported from internal/auth/middleware_test.go (those are
// unexported and package-internal; the e2e package drives the binary as a
// black-box client and needs its own copy).

const oauthKID = "e2e-key-1"
const oauthAudience = "mcp-server"

// rsaTestKey generates a 2048-bit RSA key for signing test tokens.
func rsaTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

// jwksServer serves the public half of key as a single-key JWKS document at the
// well-known path the auth middleware fetches ({issuer}/.well-known/jwks.json).
func jwksServer(t *testing.T, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{{
				"kty": "RSA",
				"use": "sig",
				"kid": oauthKID,
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// signRS256 builds and signs an RS256 JWT from the given payload claims.
func signRS256(t *testing.T, key *rsa.PrivateKey, payload map[string]interface{}) string {
	t.Helper()
	header := map[string]interface{}{"alg": "RS256", "typ": "JWT", "kid": oauthKID}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// oauthClaims returns a valid claim set for issuer. scopes (if non-empty) is set
// as the space-delimited "scope" claim the middleware parses.
func oauthClaims(issuer, scope string) map[string]interface{} {
	now := time.Now().Unix()
	c := map[string]interface{}{
		"iss":       issuer,
		"sub":       "user-42",
		"aud":       oauthAudience,
		"exp":       now + 3600,
		"iat":       now - 10,
		"jti":       "e2e-token-1",
		"tenant_id": "tenant-abc",
	}
	if scope != "" {
		c["scope"] = scope
	}
	return c
}

// newOAuthHarness starts the binary in HTTP+OAuth mode against an in-process
// JWKS server and returns the harness plus the signing key and issuer URL.
func newOAuthHarness(t *testing.T, extraEnv ...string) (*httpHarness, *rsa.PrivateKey, string) {
	t.Helper()
	key := rsaTestKey(t)
	jwks := jwksServer(t, &key.PublicKey)
	issuer := jwks.URL

	env := append([]string{
		"OAUTH_ISSUER_URL=" + issuer,
		"OAUTH_AUDIENCE=" + oauthAudience,
	}, extraEnv...)
	h := newHTTPHarness(t, env...)
	return h, key, issuer
}

// bearer sets the Authorization header for all subsequent /mcp/ requests.
func (h *httpHarness) bearer(token string) {
	h.extraHdr["Authorization"] = "Bearer " + token
}

// initBare POSTs initialize directly (no rpc() 200-assertion) so auth-failure
// status codes can be inspected before the MCP handshake would succeed.
func (h *httpHarness) initBare() *http.Response {
	return h.post(jsonRPCRequest{
		JSONRPC: "2.0", ID: 1, Method: "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "oauth-e2e", "version": "1.0.0"},
		},
	}, nil)
}

// TestOAuth_NoToken_401: a request with no Authorization header is rejected at
// the transport before reaching the MCP layer.
func TestOAuth_NoToken_401(t *testing.T) {
	h, _, _ := newOAuthHarness(t)

	resp := h.initBare()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("no token: want 401, got %d: %s", resp.StatusCode, body)
	}
}

// TestOAuth_InvalidToken_401: a structurally-bogus bearer token is rejected.
func TestOAuth_InvalidToken_401(t *testing.T) {
	h, _, _ := newOAuthHarness(t)
	h.bearer("not.a.realjwt")

	resp := h.initBare()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("invalid token: want 401, got %d: %s", resp.StatusCode, body)
	}
}

// TestOAuth_ValidToken_200: a properly signed RS256 token with matching issuer
// and audience is accepted and the MCP handshake + tool call succeed.
func TestOAuth_ValidToken_200(t *testing.T) {
	h, key, issuer := newOAuthHarness(t)
	h.bearer(signRS256(t, key, oauthClaims(issuer, "")))

	h.initialize()
	resp := h.callTool(5, "web_search", map[string]interface{}{"query": "authed query"})
	if resp.Error != nil {
		t.Fatalf("authed tool call returned protocol error: %s", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected a result for an authenticated tool call")
	}
}

// TestOAuth_InsufficientScope_Denied: with ENFORCE_SCOPES on, a token whose
// scope claim does not authorize the tool is denied by the scope gate. The
// denial is an MCP CallToolResult with IsError=true (visible to the LLM), not a
// transport-level error — so the HTTP status is 200 and the body carries the
// access-denied result.
func TestOAuth_InsufficientScope_Denied(t *testing.T) {
	h, key, issuer := newOAuthHarness(t, "ENFORCE_SCOPES=true")
	// A scope that grants a different tool, so the claim is present-but-insufficient
	// (the fail-closed branch) rather than absent (the permissive branch).
	h.bearer(signRS256(t, key, oauthClaims(issuer, "tool:scrape_page")))

	h.initialize()
	resp := h.callTool(6, "web_search", map[string]interface{}{"query": "denied query"})
	if resp.Error != nil {
		t.Fatalf("scope denial should be an IsError result, not a protocol error: %s", resp.Error)
	}

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse tool result: %v\nraw: %s", err, resp.Result)
	}
	// Assert on the scope-gate's own denial marker ("access denied: " from the
	// receiving middleware in main.go), NOT merely IsError: an authorized call
	// can also return IsError on an upstream failure (e.g. a bad provider key),
	// so IsError alone does not prove the *scope gate* fired.
	if !result.IsError {
		t.Fatal("insufficient scope should yield IsError=true")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "access denied") {
		t.Fatalf("expected an 'access denied' scope-gate message, got: %+v", result.Content)
	}
}

// TestOAuth_SufficientScope_Allowed: the same enforced-scope server accepts a
// token carrying the exact per-tool scope, proving the gate is not blanket-deny.
func TestOAuth_SufficientScope_Allowed(t *testing.T) {
	h, key, issuer := newOAuthHarness(t, "ENFORCE_SCOPES=true")
	h.bearer(signRS256(t, key, oauthClaims(issuer, "tool:web_search")))

	h.initialize()
	resp := h.callTool(8, "web_search", map[string]interface{}{"query": "scoped query"})
	if resp.Error != nil {
		t.Fatalf("authorized scope returned protocol error: %s", resp.Error)
	}

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse tool result: %v", err)
	}
	// An authorized call must NOT be denied by the scope gate. We assert the
	// absence of the gate's "access denied" marker rather than IsError==false:
	// the underlying web_search may still fail upstream (e.g. an invalid Google
	// key in CI), which is orthogonal to authorization. The contract under test
	// is "the scope gate let it through", not "the search succeeded".
	for _, c := range result.Content {
		if strings.Contains(c.Text, "access denied") {
			t.Fatalf("a token with tool:web_search scope must NOT be denied, got: %s", c.Text)
		}
	}
}

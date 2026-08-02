//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestOperationalHardening467_E2E is the milestone-closing E2E proof for the
// v1.47.0 "Operational Hardening" tracker (#467): it drives the real compiled
// binary in HTTP+OAuth multi-tenant mode and exercises the two rules that cut
// across the most other work in the milestone — tenant cache/data isolation
// (#484, rule 1) and the admin-key dual-key rotation grace period (#488,
// rule 5) — over the live MCP transport, the same way an operator's rollout
// would. Unit/integration coverage for every individual rule already runs in
// harness gates 1-5; this gate proves those pieces compose correctly end to
// end in a single real process.
func TestOperationalHardening467_E2E(t *testing.T) {
	const adminKey = "e2e-467-admin-key-aaaaaaaa"
	const adminKeyPrev = "e2e-467-admin-key-bbbbbbbb"

	h, key, issuer := newOAuthHarness(t,
		"ADMIN_API_KEY="+adminKey,
		"ADMIN_API_KEY_PREV="+adminKeyPrev,
		"MEMORY_ENABLED=true",
	)

	tokenAlice := signRS256(t, key, oauthClaimsForTenant(issuer, "tenant-alice", "user-alice"))
	tokenBob := signRS256(t, key, oauthClaimsForTenant(issuer, "tenant-bob", "user-bob"))

	grantConsent := func(tenantID, userID string) {
		t.Helper()
		body := strings.NewReader(`{"tenant_id":"` + tenantID + `","user_id":"` + userID + `","purpose":"memory","granted":true}`)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.baseURL+"/admin/consent", body)
		if err != nil {
			t.Fatalf("build consent request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Admin-Key", adminKey)
		resp, err := h.client.Do(req)
		if err != nil {
			t.Fatalf("POST /admin/consent: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("grant consent for %s/%s: status = %d", tenantID, userID, resp.StatusCode)
		}
	}
	grantConsent("tenant-alice", "user-alice")
	grantConsent("tenant-bob", "user-bob")

	t.Run("CrossTenantMemoryIsolation", func(t *testing.T) {
		// Alice authenticates and saves a note under her own tenant/user.
		hAlice := newHTTPHarnessConn(h)
		hAlice.bearer(tokenAlice)
		hAlice.initialize()

		saveResp := hAlice.callTool(20, "memory_save", map[string]interface{}{
			"note":  "operational-hardening-467 isolation secret for alice",
			"topic": "e2e-467",
		})
		if saveResp.Error != nil {
			t.Fatalf("memory_save (alice) returned protocol error: %s", saveResp.Error)
		}
		assertToolNotError(t, saveResp, "memory_save (alice)")

		// Alice recalls and must see her own note.
		recallAlice := hAlice.callTool(21, "memory_recall", map[string]interface{}{"topic": "e2e-467"})
		if recallAlice.Error != nil {
			t.Fatalf("memory_recall (alice) returned protocol error: %s", recallAlice.Error)
		}
		if !strings.Contains(string(recallAlice.Result), "isolation secret for alice") {
			t.Fatalf("alice should see her own memory, got: %s", recallAlice.Result)
		}

		// Bob authenticates under a distinct tenant and must NOT see Alice's note.
		hBob := newHTTPHarnessConn(h)
		hBob.bearer(tokenBob)
		hBob.initialize()

		recallBob := hBob.callTool(22, "memory_recall", map[string]interface{}{"topic": "e2e-467"})
		if recallBob.Error != nil {
			t.Fatalf("memory_recall (bob) returned protocol error: %s", recallBob.Error)
		}
		if strings.Contains(string(recallBob.Result), "isolation secret for alice") {
			t.Fatalf("cross-tenant leak: bob saw alice's memory: %s", recallBob.Result)
		}
	})

	t.Run("AdminKeyDualKeyGracePeriod", func(t *testing.T) {
		// The current admin key works.
		req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.baseURL+"/dashboard/data", nil)
		req1.Header.Set("X-Admin-Key", adminKey)
		resp1, err := h.client.Do(req1)
		if err != nil {
			t.Fatalf("GET /dashboard/data (current key): %v", err)
		}
		defer resp1.Body.Close()
		if resp1.StatusCode != http.StatusOK {
			t.Fatalf("current admin key: status = %d, want 200", resp1.StatusCode)
		}

		// The previous admin key, configured via ADMIN_API_KEY_PREV, is also
		// accepted during the rotation grace period (#488).
		req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.baseURL+"/dashboard/data", nil)
		req2.Header.Set("X-Admin-Key", adminKeyPrev)
		resp2, err := h.client.Do(req2)
		if err != nil {
			t.Fatalf("GET /dashboard/data (prev key): %v", err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("previous admin key during grace period: status = %d, want 200", resp2.StatusCode)
		}

		// An unrelated key is still rejected.
		req3, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.baseURL+"/dashboard/data", nil)
		req3.Header.Set("X-Admin-Key", "not-a-configured-key-at-all")
		resp3, err := h.client.Do(req3)
		if err != nil {
			t.Fatalf("GET /dashboard/data (wrong key): %v", err)
		}
		defer resp3.Body.Close()
		if resp3.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong admin key: status = %d, want 401", resp3.StatusCode)
		}
	})
}

// oauthClaimsForTenant is like oauthClaims but overrides tenant_id and sub so
// two distinct authenticated identities can be minted against the same
// harness/issuer for a cross-tenant isolation check.
func oauthClaimsForTenant(issuer, tenantID, subject string) map[string]interface{} {
	c := oauthClaims(issuer, "")
	c["tenant_id"] = tenantID
	c["sub"] = subject
	return c
}

// newHTTPHarnessConn builds a second logical client (its own session state)
// against an already-running httpHarness's server, so two distinct
// authenticated identities can drive independent MCP sessions against the
// same process without starting a second binary.
func newHTTPHarnessConn(h *httpHarness) *httpHarness {
	return &httpHarness{
		t:        h.t,
		cmd:      h.cmd,
		baseURL:  h.baseURL,
		stderr:   h.stderr,
		client:   h.client,
		extraHdr: map[string]string{},
	}
}

// assertToolNotError fails the test if the tool result carries IsError=true.
func assertToolNotError(t *testing.T, resp jsonRPCResponse, label string) {
	t.Helper()
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("%s: parse result: %v\nraw: %s", label, err, resp.Result)
	}
	if result.IsError {
		t.Fatalf("%s: unexpected IsError=true: %+v", label, result.Content)
	}
}

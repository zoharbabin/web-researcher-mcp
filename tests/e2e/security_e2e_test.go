//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Security E2E — drives the real binary over STDIO and verifies that the
// hardening fixes take hold at the live tool-call boundary (not just in unit
// tests). These run in default STDIO mode: no PORT, no auth, no rate limiting —
// exactly how the dominant deployment runs. They assert that security controls
// which MUST apply regardless of transport (SSRF, scheme validation, domain
// classification, raw-mode guards, schema strictness) are active end to end.
//
// All cases are network-free: they target URLs that are rejected BEFORE any
// outbound request (blocked schemes, private IPs, cloud-metadata hosts), so the
// suite is deterministic and needs no API keys.
// =============================================================================

// scrapeResult is the subset of the scrape_page payload the security tests read.
type scrapeResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// callScrape invokes scrape_page and returns (isError, firstTextBlock).
func callScrape(t *testing.T, h *providerHarness, args map[string]interface{}) (bool, string) {
	t.Helper()
	raw := h.callTool(t, "scrape_page", args)
	var r scrapeResult
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("parse scrape result: %v\nraw: %s", err, raw)
	}
	text := ""
	if len(r.Content) > 0 {
		text = r.Content[0].Text
	}
	return r.IsError, text
}

// TestSecurity_STDIO_ScrapeBoundary verifies the input-validation and SSRF
// chokepoint (H1 scheme validation, H5 metadata blocklist, private-IP blocking)
// reject malicious targets at the live tool boundary without making a request.
func TestSecurity_STDIO_ScrapeBoundary(t *testing.T) {
	h := newProviderHarness(t, nil)
	h.initialize(t)
	defer h.shutdown()

	cases := []struct {
		name     string
		url      string
		wantText []string // any one of these substrings proves the right rejection
	}{
		{
			name:     "rejects file scheme (H1)",
			url:      "file:///etc/passwd",
			wantText: []string{"http", "scheme", "url"},
		},
		{
			name:     "rejects gopher scheme (H1)",
			url:      "gopher://127.0.0.1:25/",
			wantText: []string{"http", "scheme", "url"},
		},
		{
			name:     "blocks loopback private IP (SSRF)",
			url:      "http://127.0.0.1:80/",
			wantText: []string{"ssrf", "blocked", "private"},
		},
		{
			name:     "blocks AWS/GCP/Azure metadata IP (SSRF)",
			url:      "http://169.254.169.254/latest/meta-data/",
			wantText: []string{"ssrf", "blocked", "private"},
		},
		{
			name:     "blocks GCP metadata hostname (H5)",
			url:      "http://metadata.google.internal/computeMetadata/v1/",
			wantText: []string{"ssrf", "blocked"},
		},
		{
			name:     "blocks RFC1918 private range (SSRF)",
			url:      "http://10.0.0.1/",
			wantText: []string{"ssrf", "blocked", "private"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isErr, text := callScrape(t, h, map[string]interface{}{"url": tc.url})
			if !isErr {
				t.Fatalf("expected error result for %q, got success: %s", tc.url, text)
			}
			lower := strings.ToLower(text)
			matched := false
			for _, w := range tc.wantText {
				if strings.Contains(lower, w) {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("error message for %q did not indicate the expected rejection.\nwant one of %v\ngot: %s",
					tc.url, tc.wantText, text)
			}
		})
	}
}

// TestSecurity_STDIO_RawModeGuards verifies raw mode (the new capability) still
// enforces the SAME SSRF + scheme guards as default mode — only sanitization is
// skipped, never the security boundary. (CONTENT-RAW-MODE decision.)
func TestSecurity_STDIO_RawModeGuards(t *testing.T) {
	h := newProviderHarness(t, nil)
	h.initialize(t)
	defer h.shutdown()

	cases := []struct {
		name string
		url  string
	}{
		{"raw blocks metadata IP", "http://169.254.169.254/latest/meta-data/"},
		{"raw blocks loopback", "http://127.0.0.1:9999/"},
		{"raw rejects file scheme", "file:///etc/hosts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isErr, text := callScrape(t, h, map[string]interface{}{
				"url":  tc.url,
				"mode": "raw",
			})
			if !isErr {
				t.Fatalf("raw mode must still reject %q, got success: %s", tc.url, text)
			}
			lower := strings.ToLower(text)
			if !strings.Contains(lower, "ssrf") && !strings.Contains(lower, "blocked") &&
				!strings.Contains(lower, "scheme") && !strings.Contains(lower, "http") {
				t.Fatalf("raw rejection for %q lacked a security reason: %s", tc.url, text)
			}
		})
	}
}

// TestSecurity_STDIO_SchemaStrictness verifies tool inputs are validated at the
// boundary: unknown properties are rejected (additionalProperties:false) and a
// missing required field is caught. This confirms "validate at system
// boundaries" holds over the live MCP transport.
func TestSecurity_STDIO_SchemaStrictness(t *testing.T) {
	h := newProviderHarness(t, nil)
	h.initialize(t)
	defer h.shutdown()

	t.Run("rejects unknown property", func(t *testing.T) {
		// The MCP SDK validates arguments against the input schema and returns a
		// JSON-RPC error (not a tool result) for unknown properties.
		id := h.nextID
		h.nextID++
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "web_search",
				"arguments": map[string]interface{}{"query": "test", "bogusParam": "x"},
			},
		})
		resp := h.readResponse()
		// Either a JSON-RPC error or an isError tool result is acceptable; what
		// matters is the unknown property is rejected, not silently accepted.
		if resp.Error == nil && resp.Result == nil {
			t.Fatal("expected an error for unknown property, got neither error nor result")
		}
		blob := string(resp.Error) + string(resp.Result)
		if !strings.Contains(strings.ToLower(blob), "additional") &&
			!strings.Contains(strings.ToLower(blob), "bogusparam") &&
			!strings.Contains(strings.ToLower(blob), "unexpected") {
			t.Fatalf("expected validation rejection of unknown property, got: %s", blob)
		}
	})

	t.Run("rejects missing required url", func(t *testing.T) {
		id := h.nextID
		h.nextID++
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "scrape_page",
				"arguments": map[string]interface{}{},
			},
		})
		resp := h.readResponse()
		blob := strings.ToLower(string(resp.Error) + string(resp.Result))
		if !strings.Contains(blob, "url") && !strings.Contains(blob, "required") {
			t.Fatalf("expected missing-required-url rejection, got: %s", blob)
		}
	})
}

// TestSecurity_STDIO_NoAuthByDefault confirms the zero-config contract: in STDIO
// mode (no PORT, no OAuth, no ENFORCE_SCOPES) every tool is reachable without a
// token. Scope enforcement is an HTTP-only concern and must NOT leak into STDIO.
func TestSecurity_STDIO_NoAuthByDefault(t *testing.T) {
	// Explicitly set the scope-enforcement knob ON to prove it is inert without
	// the HTTP transport / OAuth middleware wiring — STDIO stays permissive.
	h := newProviderHarness(t, []string{"ENFORCE_SCOPES=true"})
	h.initialize(t)
	defer h.shutdown()

	// A pure-validation tool call that needs no network: scrape_page with a
	// blocked URL still reaches the handler (returns a tool error), proving the
	// call was authorized to execute rather than rejected for missing scope.
	isErr, text := callScrape(t, h, map[string]interface{}{"url": "http://127.0.0.1/"})
	if !isErr {
		t.Fatalf("expected SSRF rejection, got success: %s", text)
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "scope") || strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "forbidden") {
		t.Fatalf("STDIO mode must not enforce scopes; got auth-like rejection: %s", text)
	}
	if !strings.Contains(lower, "ssrf") && !strings.Contains(lower, "blocked") {
		t.Fatalf("expected the call to reach the SSRF guard, got: %s", text)
	}
}

// TestSecurity_STDIO_RawModeReturnsUnsanitized verifies the raw-mode CAPABILITY
// works end to end: a real fetch returns verbatim source (script/style markup
// intact) that the default sanitized mode would strip, and honors max_length.
// Network-dependent: skipped if the fetch fails (offline CI).
func TestSecurity_STDIO_RawModeReturnsUnsanitized(t *testing.T) {
	h := newProviderHarness(t, nil)
	h.initialize(t)
	defer h.shutdown()

	// example.com is a stable, tiny, standards-maintained page.
	isErr, text := callScrape(t, h, map[string]interface{}{
		"url":        "https://example.com/",
		"mode":       "raw",
		"max_length": 4096,
	})
	if isErr {
		t.Skipf("raw fetch failed (likely offline); skipping capability check: %s", text)
	}
	var payload struct {
		Content       string `json:"content"`
		Raw           bool   `json:"raw"`
		ContentLength int    `json:"contentLength"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse raw payload: %v\nraw: %s", err, text)
	}
	if !payload.Raw {
		t.Errorf("raw mode result should set \"raw\": true; got false")
	}
	// Verbatim HTML markup must be present (sanitized mode would strip tags).
	if !strings.Contains(strings.ToLower(payload.Content), "<!doctype html") &&
		!strings.Contains(strings.ToLower(payload.Content), "<html") &&
		!strings.Contains(payload.Content, "<") {
		t.Errorf("raw content should contain verbatim markup, got: %.200s", payload.Content)
	}
	if payload.ContentLength > 4096 {
		t.Errorf("raw content length %d exceeded max_length 4096", payload.ContentLength)
	}
}

// callToolResult invokes any tool and returns (isError, firstTextBlock).
func callToolResult(t *testing.T, h *providerHarness, name string, args map[string]interface{}) (bool, string) {
	t.Helper()
	raw := h.callTool(t, name, args)
	var r scrapeResult // same {isError, content[]} shape for every tool result
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("parse %s result: %v\nraw: %s", name, err, raw)
	}
	text := ""
	if len(r.Content) > 0 {
		text = r.Content[0].Text
	}
	return r.IsError, text
}

// TestSecurity_STDIO_SessionCryptoRoundTrip exercises the new session-store
// crypto (M1 prev-key rotation + M7 AAD binding) end to end over STDIO using a
// SHARED on-disk session directory across THREE separate binary invocations:
//
//  1. key A  : create an encrypted session (sequential_search step 1).
//  2. key A  : recover it (get_research_session) — proves AAD-bound decrypt works.
//  3. key B current + key A prev : recover the SAME on-disk session — proves
//     zero-downtime key rotation (decrypt-fallback to the previous key).
//
// This is the highest-risk STDIO-path change (new wire format + version bump);
// unit tests cover the crypto, this proves the live binary persists and reloads
// real sessions across a key rotation without data loss.
func TestSecurity_STDIO_SessionCryptoRoundTrip(t *testing.T) {
	const (
		keyA = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
		keyB = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
		goal = "verify session crypto round trip across rotation"
	)
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	cacheDir := t.TempDir()

	baseEnv := func(cur, prev string) []string {
		env := []string{
			"CACHE_ENCRYPTION_KEY=" + cur,
			"SESSION_DATA_DIR=" + sessionDir,
			"CACHE_DIR=" + cacheDir,
		}
		if prev != "" {
			env = append(env, "CACHE_ENCRYPTION_KEY_PREV="+prev)
		}
		return env
	}

	// --- Invocation 1: create an encrypted session under key A. ---
	var sessionID string
	func() {
		h := newProviderHarness(t, baseEnv(keyA, ""))
		h.initialize(t)
		defer h.shutdown()

		isErr, text := callToolResult(t, h, "sequential_search", map[string]interface{}{
			"searchStep":     "initial finding about session encryption",
			"stepNumber":     1,
			"nextStepNeeded": true,
			"researchGoal":   goal,
		})
		if isErr {
			t.Fatalf("sequential_search step 1 failed: %s", text)
		}
		var out struct {
			SessionID    string `json:"sessionId"`
			ResearchGoal string `json:"researchGoal"`
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("parse sequential_search output: %v\nraw: %s", err, text)
		}
		if out.SessionID == "" {
			t.Fatalf("expected a sessionId from step 1, got none: %s", text)
		}
		sessionID = out.SessionID
	}()

	// Confirm an encrypted blob actually hit disk (not plaintext).
	assertSessionFileEncrypted(t, sessionDir, goal)

	// --- Invocation 2: recover under the SAME key A (AAD-bound decrypt). ---
	func() {
		h := newProviderHarness(t, baseEnv(keyA, ""))
		h.initialize(t)
		defer h.shutdown()

		isErr, text := callToolResult(t, h, "get_research_session", map[string]interface{}{
			"sessionId": sessionID,
		})
		if isErr {
			t.Fatalf("get_research_session under key A failed: %s", text)
		}
		if !strings.Contains(text, goal) {
			t.Fatalf("recovered session missing original goal.\nwant substring: %q\ngot: %s", goal, text)
		}
	}()

	// --- Invocation 3: rotate — key B current, key A previous. ---
	func() {
		h := newProviderHarness(t, baseEnv(keyB, keyA))
		h.initialize(t)
		defer h.shutdown()

		isErr, text := callToolResult(t, h, "get_research_session", map[string]interface{}{
			"sessionId": sessionID,
		})
		if isErr {
			t.Fatalf("get_research_session after key rotation (B current, A prev) failed: %s", text)
		}
		if !strings.Contains(text, goal) {
			t.Fatalf("post-rotation recovery missing original goal.\nwant substring: %q\ngot: %s", goal, text)
		}
	}()
}

// assertSessionFileEncrypted confirms the session directory holds a non-empty
// file whose bytes do NOT contain the plaintext goal — i.e. it was encrypted.
func assertSessionFileEncrypted(t *testing.T, dir, plaintextMarker string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read session dir %s: %v", dir, err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || len(b) == 0 {
			continue
		}
		found = true
		if strings.Contains(string(b), plaintextMarker) {
			t.Fatalf("session file %s contains plaintext goal %q — encryption did not take hold",
				e.Name(), plaintextMarker)
		}
	}
	if !found {
		t.Fatalf("no session file persisted to %s", dir)
	}
}

// TestSecurity_STDIO_DomainAllowlist verifies C3 (the host-parse allowlist fix)
// over the live binary: with ALLOWED_DOMAINS set, a URL whose HOST is outside
// the list is rejected, AND the classic bypass — embedding an allowed domain in
// the path/query of a foreign host — does NOT slip through.
func TestSecurity_STDIO_DomainAllowlist(t *testing.T) {
	h := newProviderHarness(t, []string{"ALLOWED_DOMAINS=example.com"})
	h.initialize(t)
	defer h.shutdown()

	t.Run("rejects host outside allowlist", func(t *testing.T) {
		isErr, text := callScrape(t, h, map[string]interface{}{"url": "https://golang.org/"})
		if !isErr {
			t.Fatalf("expected rejection for host outside allowlist, got success: %s", text)
		}
		lower := strings.ToLower(text)
		if !strings.Contains(lower, "domain") && !strings.Contains(lower, "allow") &&
			!strings.Contains(lower, "blocked") {
			t.Fatalf("expected an allowlist rejection reason, got: %s", text)
		}
	})

	t.Run("blocks suffix-spoof bypass", func(t *testing.T) {
		// Host is example.com.attacker.test — must NOT match allowlist entry
		// example.com. (strings.Contains would have wrongly allowed this.)
		isErr, _ := callScrape(t, h, map[string]interface{}{
			"url": "https://example.com.attacker.test/",
		})
		if !isErr {
			t.Fatal("suffix-spoof host example.com.attacker.test was allowed — C3 bypass NOT closed")
		}
	})

	t.Run("blocks path/query injection bypass", func(t *testing.T) {
		// Foreign host with the allowed domain only in the query string.
		isErr, _ := callScrape(t, h, map[string]interface{}{
			"url": "https://attacker.test/?ref=example.com",
		})
		if !isErr {
			t.Fatal("query-injection URL was allowed — C3 bypass NOT closed")
		}
	})
}

// TestSecurity_STDIO_SecretsMaskedInAudit verifies M9 over the live binary: when
// a tool error echoes a URL carrying a credential, the AUDIT sink (operator/SIEM
// trust boundary) must record the error with the secret REDACTED. We point the
// audit log at a file, trigger an SSRF-blocked scrape whose URL embeds a token
// in an `api_key=` query parameter, then assert the secret never appears in the
// audit output. The sentinel is deliberately NOT shaped like any real provider
// key (it is masked via the api_key= query-param rule, not by key shape) so it
// never trips automated secret scanners.
func TestSecurity_STDIO_SecretsMaskedInAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	const fakeKey = "NOT-A-REAL-SECRET-sentinel-value-for-masking-test"

	h := newProviderHarness(t, []string{
		"AUDIT_ENABLED=true",
		"AUDIT_OUTPUT_PATH=" + auditPath,
	})
	h.initialize(t)

	// Private IP => SSRF-blocked before any network call; the error string
	// carries the full URL (with the embedded sentinel) into the audit sink.
	isErr, _ := callScrape(t, h, map[string]interface{}{
		"url": "http://10.0.0.1/data?api_key=" + fakeKey,
	})
	if !isErr {
		t.Fatal("expected SSRF rejection for the credential-bearing URL")
	}

	// Shut down so the async audit logger flushes to the file.
	h.shutdown()
	time.Sleep(300 * time.Millisecond)

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log %s: %v", auditPath, err)
	}
	audit := string(data)
	if audit == "" {
		t.Fatal("audit log is empty; expected a tool_call event for the blocked scrape")
	}
	if strings.Contains(audit, fakeKey) {
		t.Fatalf("AUDIT LEAK: the secret %q appears unmasked in the audit log:\n%s", fakeKey, audit)
	}
	if !strings.Contains(audit, "REDACTED") {
		t.Fatalf("expected a [REDACTED] marker proving masking ran; audit log:\n%s", audit)
	}
	// Sanity: the event is the scrape_page error we triggered.
	if !strings.Contains(audit, "scrape_page") {
		t.Fatalf("expected a scrape_page audit event, got:\n%s", audit)
	}
}

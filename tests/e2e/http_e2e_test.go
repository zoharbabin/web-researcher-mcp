//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// httpHarness runs the compiled binary as a real HTTP MCP server (PORT set) and
// drives it over the Streamable HTTP transport exactly as a network client
// would: raw POSTs to /mcp/, Mcp-Session-Id propagation, and SSE response
// framing. It reuses buildBinary/projectRoot from e2e_test.go.
//
// These tests are network-free by construction: CHROME_PATH=disabled keeps the
// browser tier off the test host, and the only tool exercised (web_search) is
// asserted for transport success, not provider results, so no API keys or
// outbound calls are required.
type httpHarness struct {
	t         *testing.T
	cmd       *exec.Cmd
	baseURL   string
	sessionID string
	stderr    *bytes.Buffer
	client    *http.Client
	extraHdr  map[string]string // headers injected on every /mcp/ request
}

// freePort asks the kernel for an unused TCP port, then releases it. A small
// TOCTOU window exists between Close and the child binding it; the readiness
// poll in newHTTPHarness closes that gap by retrying until the server answers.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// newHTTPHarness builds the binary, starts it in HTTP mode on a free port, and
// blocks until /health/ready answers. extraEnv is appended after the baseline
// env (CHROME_PATH=disabled) so callers can enable OAuth, scopes, CORS, etc.
func newHTTPHarness(t *testing.T, extraEnv ...string) *httpHarness {
	t.Helper()
	binPath := buildBinary(t)
	port := freePort(t)

	env := append([]string{
		"CHROME_PATH=disabled", // host has no Chromium; keep tests network-free
		fmt.Sprintf("PORT=%d", port),
	}, extraEnv...)

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), env...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start HTTP server: %v", err)
	}

	h := &httpHarness{
		t:        t,
		cmd:      cmd,
		baseURL:  fmt.Sprintf("http://127.0.0.1:%d", port),
		stderr:   &stderr,
		client:   &http.Client{Timeout: 10 * time.Second},
		extraHdr: map[string]string{},
	}
	t.Cleanup(h.shutdown)

	h.waitReady()
	return h
}

// waitReady polls /health/ready until it returns 200 "ready" or the deadline
// elapses. On timeout it dumps captured stderr so a startup failure (bad
// config, port clash, the lifecycle regression this whole suite guards) is
// immediately diagnosable rather than a bare "connection refused".
func (h *httpHarness) waitReady() {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		// Detect early process exit (the historical Docker bug: HTTP torn down
		// by stdin EOF) instead of polling a dead server for the full timeout.
		if h.cmd.ProcessState != nil {
			h.t.Fatalf("server exited before readiness:\n%s", h.stderr.String())
		}
		resp, err := h.client.Get(h.baseURL + "/health/ready")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "ready" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("server not ready within 15s:\n%s", h.stderr.String())
}

// post sends a JSON-RPC message to /mcp/ (trailing slash is mandatory: POSTing
// to /mcp would 308-redirect and Go drops the body on redirect). It sets the
// dual Accept the Streamable HTTP handler requires, propagates the negotiated
// Mcp-Session-Id, and applies any per-harness extra headers (auth, request-id).
// It returns the raw *http.Response so callers can assert status/headers before
// the body is consumed.
func (h *httpHarness) post(body any, hdr map[string]string) *http.Response {
	h.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		h.t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.baseURL+"/mcp/", bytes.NewReader(data))
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if h.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", h.sessionID)
	}
	for k, v := range h.extraHdr {
		req.Header.Set(k, v)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("POST /mcp/: %v", err)
	}
	return resp
}

// rpc sends a JSON-RPC request, asserts a 200, parses the (SSE-framed) body,
// and returns the decoded response. It captures the Mcp-Session-Id from the
// first (initialize) response so subsequent calls join the same session.
func (h *httpHarness) rpc(msg jsonRPCRequest) jsonRPCResponse {
	h.t.Helper()
	resp := h.post(msg, nil)
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && h.sessionID == "" {
		h.sessionID = sid
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	return parseRPCBody(h.t, resp)
}

// parseRPCBody decodes a Streamable HTTP response body. With the default
// handler options the body is text/event-stream: zero or more `data: <json>`
// lines. It returns the first JSON-RPC object found, tolerating SSE comment /
// event: / ping lines and (defensively) a plain application/json body.
func parseRPCBody(t *testing.T, resp *http.Response) jsonRPCResponse {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var out jsonRPCResponse
		if err := json.Unmarshal(bytes.TrimSpace(raw), &out); err != nil {
			t.Fatalf("parse JSON body: %v\nraw: %s", err, raw)
		}
		return out
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // comment, event:, id:, or blank framing line
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		var out jsonRPCResponse
		if err := json.Unmarshal([]byte(payload), &out); err != nil {
			continue // not the JSON-RPC data frame (e.g. a keep-alive ping)
		}
		return out
	}
	t.Fatalf("no JSON-RPC data frame in SSE response (content-type %q):\n%s", ct, raw)
	return jsonRPCResponse{}
}

// initialize performs the MCP handshake over HTTP and captures the session ID.
func (h *httpHarness) initialize() {
	h.t.Helper()
	resp := h.rpc(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "http-e2e", "version": "1.0.0"},
		},
	})
	if resp.Error != nil {
		h.t.Fatalf("initialize error: %s", resp.Error)
	}
	if h.sessionID == "" {
		h.t.Fatal("server did not return an Mcp-Session-Id on initialize")
	}
	// The notification has no response; fire-and-forget.
	notif := h.post(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}, nil)
	_ = notif.Body.Close()
}

// callTool invokes a tool over HTTP and returns the raw result payload.
func (h *httpHarness) callTool(id int, name string, args map[string]interface{}) jsonRPCResponse {
	h.t.Helper()
	return h.rpc(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  map[string]interface{}{"name": name, "arguments": args},
	})
}

func (h *httpHarness) shutdown() {
	if h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Signal(os.Interrupt)

	done := make(chan error, 1)
	go func() { done <- h.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = h.cmd.Process.Kill()
	}
}

// TestHTTP_Lifecycle is the core guard for the main.go fix: with PORT set the
// process must stay alive serving HTTP (not exit on stdin EOF) and complete a
// full MCP handshake + tool call over the network transport.
func TestHTTP_Lifecycle(t *testing.T) {
	h := newHTTPHarness(t)
	h.initialize()

	t.Run("ListTools", func(t *testing.T) {
		resp := h.rpc(jsonRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
		if resp.Error != nil {
			t.Fatalf("tools/list error: %s", resp.Error)
		}
		var result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("parse tools: %v", err)
		}
		if len(result.Tools) == 0 {
			t.Fatal("expected at least one tool over HTTP")
		}
	})

	t.Run("CallTool", func(t *testing.T) {
		resp := h.callTool(3, "web_search", map[string]interface{}{"query": "test query"})
		if resp.ID != float64(3) {
			t.Fatalf("expected ID 3, got %v", resp.ID)
		}
	})
}

// TestHTTP_ToolParity asserts the same tool call succeeds over HTTP just as it
// does over STDIO (TestMCPLifecycle/CallTool), proving transport parity: the
// MCP layer behaves identically regardless of transport.
func TestHTTP_ToolParity(t *testing.T) {
	h := newHTTPHarness(t)
	h.initialize()

	resp := h.callTool(7, "web_search", map[string]interface{}{"query": "parity check"})
	if resp.Error != nil {
		t.Fatalf("web_search over HTTP returned protocol error: %s", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected a tools/call result over HTTP")
	}
}

// TestHTTP_OversizedBody_413 proves the MaxBytesReader cap (C2) rejects an
// oversized POST before it can exhaust memory. The body must exceed
// MAX_REQUEST_BODY; the SDK surfaces the truncated read as an error status.
func TestHTTP_OversizedBody_413(t *testing.T) {
	h := newHTTPHarness(t, "MAX_REQUEST_BODY_BYTES=1024")
	h.initialize()

	// A ~64KB query dwarfs the 1KB cap. Send raw so we can read the status code
	// directly rather than through rpc()'s 200 assertion.
	big := strings.Repeat("a", 64*1024)
	resp := h.post(jsonRPCRequest{
		JSONRPC: "2.0", ID: 9, Method: "tools/call",
		Params: map[string]interface{}{"name": "web_search", "arguments": map[string]interface{}{"query": big}},
	}, nil)
	defer resp.Body.Close()

	// The SDK maps an over-cap body read to 4xx; we accept any client-error
	// status (413 Payload Too Large or 400 Bad Request) — the contract is
	// "rejected, not OK", not a specific code.
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("oversized body should be rejected with 4xx, got %d: %s", resp.StatusCode, body)
	}
}

// TestHTTP_SecurityHeaders asserts the static security headers are present on
// every response (set unconditionally by the securityHeaders middleware).
func TestHTTP_SecurityHeaders(t *testing.T) {
	h := newHTTPHarness(t)

	resp, err := h.client.Get(h.baseURL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	defer resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Cache-Control":             "no-store",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

// TestHTTP_RequestIDEcho asserts the requestIDMiddleware echoes a client-
// supplied X-Request-Id (sanitized) back on the response for audit correlation.
func TestHTTP_RequestIDEcho(t *testing.T) {
	h := newHTTPHarness(t)

	const want = "e2e-correlation-12345"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		h.baseURL+"/health/ready", nil)
	req.Header.Set("X-Request-Id", want)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Request-Id"); got != want {
		t.Errorf("X-Request-Id echoed as %q, want %q", got, want)
	}
}

// TestHTTP_CORSReflection asserts an allowed Origin is reflected and a
// disallowed one is not, when ALLOWED_ORIGINS is configured (fail-closed list).
func TestHTTP_CORSReflection(t *testing.T) {
	const allowed = "https://app.example.com"
	h := newHTTPHarness(t, "ALLOWED_ORIGINS="+allowed)

	t.Run("Allowed", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.baseURL+"/health/ready", nil)
		req.Header.Set("Origin", allowed)
		resp, err := h.client.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != allowed {
			t.Errorf("allowed origin reflected as %q, want %q", got, allowed)
		}
	})

	t.Run("Denied", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.baseURL+"/health/ready", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		resp, err := h.client.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("disallowed origin should not be reflected, got %q", got)
		}
	})
}

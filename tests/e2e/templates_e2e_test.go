//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// These tests exercise the MCP Resources (stats://*) and Prompts (research
// templates) surfaces end-to-end over both transports. Unit tests cover the
// handler logic directly; these prove the surfaces are discoverable and
// invocable through the real MCP protocol, over STDIO and HTTP alike.
//
// They are network-free: listing/reading resources and rendering prompts does
// no outbound I/O. CHROME_PATH=disabled keeps the browser tier off the host.

// expectedPrompts is the set of research templates the server registers
// (internal/resources/resources.go). The e2e test asserts all are advertised.
var expectedPrompts = []string{
	"comprehensive-research",
	"fact-check",
	"competitive-analysis",
	"literature-review",
}

// expectedResources is the set of stats resources the server registers.
var expectedResources = []string{
	"stats://tools",
	"stats://sessions",
	"stats://rate-limits",
	"stats://providers",
}

type promptListResult struct {
	Prompts []struct {
		Name      string `json:"name"`
		Arguments []struct {
			Name     string `json:"name"`
			Required bool   `json:"required"`
		} `json:"arguments"`
	} `json:"prompts"`
}

type resourceListResult struct {
	Resources []struct {
		URI      string `json:"uri"`
		Name     string `json:"name"`
		MIMEType string `json:"mimeType"`
	} `json:"resources"`
}

type getPromptResult struct {
	Messages []struct {
		Role    string `json:"role"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
}

type readResourceResult struct {
	Contents []struct {
		URI      string `json:"uri"`
		MIMEType string `json:"mimeType"`
		Text     string `json:"text"`
	} `json:"contents"`
}

func assertContainsAll(t *testing.T, label string, got, want []string) {
	t.Helper()
	have := make(map[string]bool, len(got))
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("%s: missing %q (got %v)", label, w, got)
		}
	}
}

// --- STDIO -------------------------------------------------------------------

// TestSecurity_STDIO_Templates verifies prompts + resources over STDIO. The
// name carries the TestSecurity_STDIO prefix so it runs in the network-free CI
// e2e job alongside the other STDIO suites.
func TestSecurity_STDIO_Templates(t *testing.T) {
	h := newSecurityHarness(t)
	h.initialize(t)
	defer h.shutdown()

	t.Run("PromptsList", func(t *testing.T) {
		resp := h.rpc(t, "prompts/list", nil)
		if resp.Error != nil {
			t.Fatalf("prompts/list error: %s", resp.Error)
		}
		var res promptListResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse prompts/list: %v\nraw: %s", err, resp.Result)
		}
		names := make([]string, len(res.Prompts))
		for i, p := range res.Prompts {
			names[i] = p.Name
		}
		assertContainsAll(t, "prompts/list", names, expectedPrompts)
	})

	t.Run("PromptsGet_Renders", func(t *testing.T) {
		resp := h.rpc(t, "prompts/get", map[string]interface{}{
			"name":      "comprehensive-research",
			"arguments": map[string]interface{}{"topic": "quantum networking", "depth": "deep"},
		})
		if resp.Error != nil {
			t.Fatalf("prompts/get error: %s", resp.Error)
		}
		var res getPromptResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse prompts/get: %v\nraw: %s", err, resp.Result)
		}
		if len(res.Messages) == 0 {
			t.Fatal("prompts/get returned no messages")
		}
		text := res.Messages[0].Content.Text
		// The rendered template must interpolate the topic and reflect the
		// "deep" depth (6 steps per resources.go).
		if !strings.Contains(text, "quantum networking") {
			t.Errorf("rendered prompt missing topic: %q", text)
		}
		if !strings.Contains(text, "6 steps") {
			t.Errorf("deep depth should render 6 steps, got: %q", text)
		}
	})

	t.Run("PromptsGet_MissingRequiredArg", func(t *testing.T) {
		// "topic" is required; omitting it must not crash the server. Accept
		// either a protocol error or a rendered prompt with an empty topic —
		// the contract under test is "no panic, server stays responsive".
		resp := h.rpc(t, "prompts/get", map[string]interface{}{
			"name":      "fact-check",
			"arguments": map[string]interface{}{}, // omit required "claim"
		})
		_ = resp // tolerate either outcome; the follow-up call proves liveness
		live := h.rpc(t, "prompts/list", nil)
		if live.Error != nil {
			t.Fatalf("server unresponsive after missing-arg prompt: %s", live.Error)
		}
	})

	t.Run("ResourcesList", func(t *testing.T) {
		resp := h.rpc(t, "resources/list", nil)
		if resp.Error != nil {
			t.Fatalf("resources/list error: %s", resp.Error)
		}
		var res resourceListResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse resources/list: %v\nraw: %s", err, resp.Result)
		}
		uris := make([]string, len(res.Resources))
		for i, r := range res.Resources {
			uris[i] = r.URI
		}
		assertContainsAll(t, "resources/list", uris, expectedResources)
	})

	t.Run("ResourcesRead_Stats", func(t *testing.T) {
		resp := h.rpc(t, "resources/read", map[string]interface{}{"uri": "stats://tools"})
		if resp.Error != nil {
			t.Fatalf("resources/read error: %s", resp.Error)
		}
		var res readResourceResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse resources/read: %v\nraw: %s", err, resp.Result)
		}
		if len(res.Contents) == 0 {
			t.Fatal("resources/read returned no contents")
		}
		c := res.Contents[0]
		if c.MIMEType != "application/json" {
			t.Errorf("stats://tools MIME = %q, want application/json", c.MIMEType)
		}
		// The body must be valid JSON carrying a "tools" key.
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.Text), &body); err != nil {
			t.Fatalf("stats://tools body is not valid JSON: %v\n%s", err, c.Text)
		}
		if _, ok := body["tools"]; !ok {
			t.Errorf("stats://tools body missing 'tools' key: %s", c.Text)
		}
	})
}

// --- HTTP --------------------------------------------------------------------

// TestHTTP_Templates verifies the same prompts + resources surfaces over the
// HTTP transport, proving transport parity for templates (not just tools).
func TestHTTP_Templates(t *testing.T) {
	h := newHTTPHarness(t)
	h.initialize()

	t.Run("PromptsList", func(t *testing.T) {
		resp := h.rpc(jsonRPCRequest{JSONRPC: "2.0", ID: 50, Method: "prompts/list"})
		if resp.Error != nil {
			t.Fatalf("prompts/list error: %s", resp.Error)
		}
		var res promptListResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse prompts/list: %v\nraw: %s", err, resp.Result)
		}
		names := make([]string, len(res.Prompts))
		for i, p := range res.Prompts {
			names[i] = p.Name
		}
		assertContainsAll(t, "prompts/list", names, expectedPrompts)
	})

	t.Run("PromptsGet_Renders", func(t *testing.T) {
		resp := h.rpc(jsonRPCRequest{
			JSONRPC: "2.0", ID: 51, Method: "prompts/get",
			Params: map[string]interface{}{
				"name":      "literature-review",
				"arguments": map[string]interface{}{"topic": "CRISPR ethics", "year_from": "2020", "year_to": "2024"},
			},
		})
		if resp.Error != nil {
			t.Fatalf("prompts/get error: %s", resp.Error)
		}
		var res getPromptResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse prompts/get: %v\nraw: %s", err, resp.Result)
		}
		if len(res.Messages) == 0 {
			t.Fatal("prompts/get returned no messages")
		}
		text := res.Messages[0].Content.Text
		if !strings.Contains(text, "CRISPR ethics") {
			t.Errorf("rendered prompt missing topic: %q", text)
		}
		if !strings.Contains(text, "2020") || !strings.Contains(text, "2024") {
			t.Errorf("rendered prompt missing year range: %q", text)
		}
	})

	t.Run("ResourcesList", func(t *testing.T) {
		resp := h.rpc(jsonRPCRequest{JSONRPC: "2.0", ID: 52, Method: "resources/list"})
		if resp.Error != nil {
			t.Fatalf("resources/list error: %s", resp.Error)
		}
		var res resourceListResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse resources/list: %v\nraw: %s", err, resp.Result)
		}
		uris := make([]string, len(res.Resources))
		for i, r := range res.Resources {
			uris[i] = r.URI
		}
		assertContainsAll(t, "resources/list", uris, expectedResources)
	})

	t.Run("ResourcesRead_Providers", func(t *testing.T) {
		resp := h.rpc(jsonRPCRequest{
			JSONRPC: "2.0", ID: 53, Method: "resources/read",
			Params: map[string]interface{}{"uri": "stats://providers"},
		})
		if resp.Error != nil {
			t.Fatalf("resources/read error: %s", resp.Error)
		}
		var res readResourceResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("parse resources/read: %v\nraw: %s", err, resp.Result)
		}
		if len(res.Contents) == 0 {
			t.Fatal("resources/read returned no contents")
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(res.Contents[0].Text), &body); err != nil {
			t.Fatalf("stats://providers body is not valid JSON: %v", err)
		}
		if _, ok := body["providers"]; !ok {
			t.Errorf("stats://providers body missing 'providers' key: %s", res.Contents[0].Text)
		}
	})
}

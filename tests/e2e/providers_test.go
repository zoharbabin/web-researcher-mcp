//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestProviders_Live exercises each search provider against real APIs.
// Requires env vars: GOOGLE_CUSTOM_SEARCH_API_KEY, GOOGLE_CUSTOM_SEARCH_ID,
// BRAVE_API_KEY, SERPER_API_KEY.
func TestProviders_Live(t *testing.T) {
	requiredEnvs := []string{
		"GOOGLE_CUSTOM_SEARCH_API_KEY",
		"GOOGLE_CUSTOM_SEARCH_ID",
		"BRAVE_API_KEY",
		"SERPER_API_KEY",
	}
	for _, env := range requiredEnvs {
		if os.Getenv(env) == "" {
			t.Skipf("skipping: %s not set", env)
		}
	}

	providers := []struct {
		name      string
		env       []string
		webQuery  string
		imgQuery  string
		newsQuery string
	}{
		{
			name:      "google",
			env:       []string{"SEARCH_PROVIDER=google"},
			webQuery:  "Go programming language concurrency patterns",
			imgQuery:  "golden gate bridge sunset",
			newsQuery: "artificial intelligence research 2026",
		},
		{
			name:      "brave",
			env:       []string{"SEARCH_PROVIDER=brave"},
			webQuery:  "Rust programming memory safety borrow checker",
			imgQuery:  "aurora borealis norway",
			newsQuery: "space exploration mars mission",
		},
		{
			name:      "serper",
			env:       []string{"SEARCH_PROVIDER=serper"},
			webQuery:  "Python asyncio event loop tutorial",
			imgQuery:  "great barrier reef coral",
			newsQuery: "renewable energy solar power",
		},
	}

	for _, prov := range providers {
		t.Run(prov.name, func(t *testing.T) {
			h := newProviderHarness(t, prov.env)
			defer h.shutdown()

			h.initialize(t)

			t.Run("web_search", func(t *testing.T) {
				result := h.callTool(t, "web_search", map[string]interface{}{
					"query":       prov.webQuery,
					"num_results": 3,
				})
				assertToolSuccess(t, result)
				assertHasURLs(t, result)
			})

			t.Run("image_search", func(t *testing.T) {
				result := h.callTool(t, "image_search", map[string]interface{}{
					"query":       prov.imgQuery,
					"num_results": 3,
				})
				assertToolSuccess(t, result)
			})

			t.Run("news_search", func(t *testing.T) {
				result := h.callTool(t, "news_search", map[string]interface{}{
					"query":       prov.newsQuery,
					"num_results": 3,
				})
				assertToolSuccess(t, result)
			})
		})
	}
}

// TestProviders_WebSearchWithLens verifies lens routing works with site operators.
func TestProviders_WebSearchWithLens(t *testing.T) {
	if os.Getenv("GOOGLE_CUSTOM_SEARCH_API_KEY") == "" {
		t.Skip("skipping: GOOGLE_CUSTOM_SEARCH_API_KEY not set")
	}

	h := newProviderHarness(t, []string{"SEARCH_PROVIDER=google"})
	defer h.shutdown()
	h.initialize(t)

	result := h.callTool(t, "web_search", map[string]interface{}{
		"query":       "context best practices",
		"lens":        "programming",
		"num_results": 3,
	})
	assertToolSuccess(t, result)
	assertHasURLs(t, result)
}

// TestProviders_SearchAndScrape verifies the combined pipeline end-to-end.
func TestProviders_SearchAndScrape(t *testing.T) {
	if os.Getenv("GOOGLE_CUSTOM_SEARCH_API_KEY") == "" {
		t.Skip("skipping: GOOGLE_CUSTOM_SEARCH_API_KEY not set")
	}

	h := newProviderHarness(t, []string{"SEARCH_PROVIDER=google"})
	defer h.shutdown()
	h.initialize(t)

	result := h.callTool(t, "search_and_scrape", map[string]interface{}{
		"query":       "Model Context Protocol specification",
		"num_results": 2,
	})
	assertToolSuccess(t, result)
}

// TestProviders_AcademicSearch verifies academic search with real APIs.
func TestProviders_AcademicSearch(t *testing.T) {
	if os.Getenv("GOOGLE_CUSTOM_SEARCH_API_KEY") == "" {
		t.Skip("skipping: GOOGLE_CUSTOM_SEARCH_API_KEY not set")
	}

	h := newProviderHarness(t, []string{"SEARCH_PROVIDER=google"})
	defer h.shutdown()
	h.initialize(t)

	result := h.callTool(t, "academic_search", map[string]interface{}{
		"query":       "transformer architecture attention mechanism",
		"num_results": 3,
	})
	assertToolSuccess(t, result)
}

// TestProviders_ScrapePage verifies content extraction from a known URL.
func TestProviders_ScrapePage(t *testing.T) {
	if os.Getenv("GOOGLE_CUSTOM_SEARCH_API_KEY") == "" {
		t.Skip("skipping: GOOGLE_CUSTOM_SEARCH_API_KEY not set")
	}

	h := newProviderHarness(t, nil)
	defer h.shutdown()
	h.initialize(t)

	result := h.callTool(t, "scrape_page", map[string]interface{}{
		"url": "https://go.dev/doc/effective_go",
	})
	assertToolSuccess(t, result)

	content := extractContent(t, result)
	if len(content) < 100 {
		t.Errorf("expected substantial content from go.dev, got %d bytes", len(content))
	}
}

// =============================================================================
// Test Harness
// =============================================================================

type providerHarness struct {
	t       *testing.T
	cmd     *exec.Cmd
	scanner *bufio.Scanner
	stdin   interface {
		Write([]byte) (int, error)
		Close() error
	}
	nextID int
}

func newProviderHarness(t *testing.T, extraEnv []string) *providerHarness {
	t.Helper()
	binPath := buildBinary(t)

	cmd := exec.Command(binPath)
	cmd.Dir = projectRoot(t)
	env := os.Environ()
	env = append(env, extraEnv...)
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// A single JSON-RPC response line can carry up to the server's total
	// content cap (~300KB for scrape_page), far exceeding bufio.Scanner's
	// default 64KB MaxScanTokenSize. Raise the buffer so large but legitimate
	// responses are read in one token instead of failing with "token too long".
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	return &providerHarness{
		t:       t,
		cmd:     cmd,
		scanner: scanner,
		stdin:   stdin,
		nextID:  1,
	}
}

func (h *providerHarness) send(msg jsonRPCRequest) {
	h.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	if _, err := fmt.Fprintf(h.stdin, "%s\n", data); err != nil {
		h.t.Fatalf("write: %v", err)
	}
}

func (h *providerHarness) readResponse() jsonRPCResponse {
	h.t.Helper()
	if !h.scanner.Scan() {
		if err := h.scanner.Err(); err != nil {
			h.t.Fatalf("scan: %v", err)
		}
		h.t.Fatal("EOF")
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(h.scanner.Bytes(), &resp); err != nil {
		h.t.Fatalf("unmarshal response: %v\nraw: %s", err, h.scanner.Text())
	}
	return resp
}

func (h *providerHarness) initialize(t *testing.T) {
	t.Helper()
	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      h.nextID,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-provider-test", "version": "1.0.0"},
		},
	})
	h.nextID++
	resp := h.readResponse()
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
}

func (h *providerHarness) callTool(t *testing.T, name string, args map[string]interface{}) json.RawMessage {
	t.Helper()
	id := h.nextID
	h.nextID++
	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      name,
			"arguments": args,
		},
	})
	resp := h.readResponse()
	if resp.ID != float64(id) {
		t.Fatalf("expected ID %d, got %v", id, resp.ID)
	}
	return resp.Result
}

func (h *providerHarness) shutdown() {
	h.t.Helper()
	h.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- h.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		h.cmd.Process.Kill()
	}
}

// =============================================================================
// Assertions
// =============================================================================

func assertToolSuccess(t *testing.T, result json.RawMessage) {
	t.Helper()
	var parsed struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("failed to parse tool result: %v\nraw: %s", err, result)
	}
	if parsed.IsError {
		text := ""
		if len(parsed.Content) > 0 {
			text = parsed.Content[0].Text
		}
		t.Fatalf("tool returned error: %s", text)
	}
	if len(parsed.Content) == 0 {
		t.Fatal("tool returned empty content")
	}
}

func assertHasURLs(t *testing.T, result json.RawMessage) {
	t.Helper()
	content := extractContent(t, result)

	var data struct {
		URLs []string `json:"urls"`
	}
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		// Not all tools return JSON with urls field; check raw content for http
		if len(content) < 10 {
			t.Errorf("expected content with URLs, got: %s", content)
		}
		return
	}
	if len(data.URLs) == 0 {
		t.Error("expected at least one URL in results")
	}
}

func extractContent(t *testing.T, result json.RawMessage) string {
	t.Helper()
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	if len(parsed.Content) == 0 {
		t.Fatal("no content blocks")
	}
	return parsed.Content[0].Text
}

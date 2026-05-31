//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "web-researcher-mcp")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/web-researcher-mcp")
	cmd.Dir = projectRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, out)
	}
	return binPath
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type mcpTestHarness struct {
	t       *testing.T
	cmd     *exec.Cmd
	scanner *bufio.Scanner
	stdin   interface {
		Write([]byte) (int, error)
		Close() error
	}
}

func newMCPTestHarness(t *testing.T) *mcpTestHarness {
	t.Helper()
	binPath := buildBinary(t)

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"GOOGLE_CUSTOM_SEARCH_API_KEY=test",
		"GOOGLE_CUSTOM_SEARCH_ID=test",
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Raise the scanner buffer above bufio's default 64KB so large but
	// legitimate single-line JSON-RPC responses (scrape content can approach
	// the server's ~300KB total cap) are read without "token too long".
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	return &mcpTestHarness{
		t:       t,
		cmd:     cmd,
		scanner: scanner,
		stdin:   stdin,
	}
}

func (h *mcpTestHarness) send(msg jsonRPCRequest) {
	h.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		h.t.Fatalf("failed to marshal request: %v", err)
	}
	_, err = fmt.Fprintf(h.stdin, "%s\n", data)
	if err != nil {
		h.t.Fatalf("failed to write to stdin: %v", err)
	}
}

func (h *mcpTestHarness) readResponse() jsonRPCResponse {
	h.t.Helper()
	if !h.scanner.Scan() {
		if err := h.scanner.Err(); err != nil {
			h.t.Fatalf("scanner error: %v", err)
		}
		h.t.Fatal("no response received (EOF)")
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(h.scanner.Bytes(), &resp); err != nil {
		h.t.Fatalf("failed to parse response: %v\nraw: %s", err, h.scanner.Text())
	}
	return resp
}

func (h *mcpTestHarness) shutdown() {
	h.t.Helper()
	h.stdin.Close()

	done := make(chan error, 1)
	go func() {
		done <- h.cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			h.t.Fatalf("server exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		h.cmd.Process.Kill()
		h.t.Fatal("server did not shut down within 10 seconds")
	}
}

func TestMCPLifecycle(t *testing.T) {
	h := newMCPTestHarness(t)

	t.Run("Initialize", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "initialize",
			Params: map[string]interface{}{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]interface{}{},
				"clientInfo": map[string]interface{}{
					"name":    "e2e-test",
					"version": "1.0.0",
				},
			},
		})

		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("initialize returned error: %s", resp.Error)
		}
		if resp.ID != float64(1) {
			t.Fatalf("expected ID 1, got %v", resp.ID)
		}
	})

	t.Run("Initialized", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			Method:  "notifications/initialized",
		})
	})

	t.Run("ListTools", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      2,
			Method:  "tools/list",
		})

		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("tools/list returned error: %s", resp.Error)
		}

		var result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("failed to parse tools result: %v", err)
		}
		if len(result.Tools) == 0 {
			t.Fatal("expected at least one tool registered")
		}
	})

	t.Run("CallTool", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name": "web_search",
				"arguments": map[string]interface{}{
					"query": "test query",
				},
			},
		})

		resp := h.readResponse()
		if resp.ID != float64(3) {
			t.Fatalf("expected ID 3, got %v", resp.ID)
		}
	})

	t.Run("Shutdown", func(t *testing.T) {
		h.shutdown()
	})
}

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
	// Walk up from test file to find go.mod
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

func TestMCPLifecycle(t *testing.T) {
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

	scanner := bufio.NewScanner(stdout)

	send := func(msg jsonRPCRequest) {
		t.Helper()
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}
		_, err = fmt.Fprintf(stdin, "%s\n", data)
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}
	}

	readResponse := func() jsonRPCResponse {
		t.Helper()
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("scanner error: %v", err)
			}
			t.Fatal("no response received (EOF)")
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v\nraw: %s", err, scanner.Text())
		}
		return resp
	}

	// Step 1: Initialize
	send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "e2e-test",
				"version": "1.0.0",
			},
		},
	})

	initResp := readResponse()
	if initResp.Error != nil {
		t.Fatalf("initialize returned error: %s", initResp.Error)
	}
	if initResp.ID != float64(1) {
		t.Fatalf("expected ID 1, got %v", initResp.ID)
	}

	// Step 2: Initialized notification (no response expected)
	send(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})

	// Step 3: List tools
	send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})

	toolsResp := readResponse()
	if toolsResp.Error != nil {
		t.Fatalf("tools/list returned error: %s", toolsResp.Error)
	}

	var toolsResult struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &toolsResult); err != nil {
		t.Fatalf("failed to parse tools result: %v", err)
	}
	if len(toolsResult.Tools) == 0 {
		t.Fatal("expected at least one tool registered")
	}

	// Step 4: Call web_search (expected to fail without real API key, but should not crash)
	send(jsonRPCRequest{
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

	searchResp := readResponse()
	// The tool call may return a tool-level error (isError in content) or a JSON-RPC error.
	// Either is acceptable as long as the server didn't crash.
	if searchResp.ID != float64(3) {
		t.Fatalf("expected ID 3, got %v", searchResp.ID)
	}

	// Step 5: Close stdin to signal shutdown
	stdin.Close()

	// Wait for the process to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("server did not shut down within 10 seconds")
	}
}

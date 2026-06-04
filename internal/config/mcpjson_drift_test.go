package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"testing"
)

// repoRootForTest resolves the repository root relative to this test file,
// independent of the working directory the test runs from.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/config/mcpjson_drift_test.go -> up two dirs to repo root.
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// serverEnvVars scans config.go for every environment variable the server
// reads (os.Getenv / envInt / envBool / envOrDefault). This is the source of
// truth for "what the server actually understands", derived mechanically so it
// never drifts from the code.
func serverEnvVars(t *testing.T, root string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("read config.go: %v", err)
	}
	// Matches os.Getenv("X"), envInt("X", …), envBool("X", …), envOrDefault("X", …).
	re := regexp.MustCompile(`(?:os\.Getenv|envInt|envBool|envOrDefault)\("([A-Z0-9_]+)"`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = true
	}
	if len(out) < 20 {
		t.Fatalf("expected many env vars parsed from config.go, got %d — regex may be stale", len(out))
	}
	return out
}

// httpOnlyVars are server vars that only take effect in HTTP transport mode
// (PORT>0): OAuth, CORS, rate limiting, distributed state, admin endpoints, and
// HTTP response-header tuning. They are meaningless in a STDIO client config, so
// .mcp.json (a STDIO example) must NOT list them. HTTP operators configure these
// via docs/DEPLOYMENT.md + .env.example instead.
var httpOnlyVars = map[string]bool{
	"PORT":                       true,
	"OAUTH_ISSUER_URL":           true,
	"OAUTH_AUDIENCE":             true,
	"ENFORCE_SCOPES":             true,
	"REQUIRED_SCOPES":            true,
	"CORS_STRICT":                true,
	"ALLOWED_ORIGINS":            true,
	"RATE_LIMIT_GLOBAL":          true,
	"RATE_LIMIT_PER_IP":          true,
	"RATE_LIMIT_PER_TENANT":      true,
	"RATE_LIMIT_PERSIST":         true,
	"DAILY_QUOTA_PER_TENANT":     true,
	"MAX_CALLS_PER_DAY":          true,
	"REDIS_URL":                  true,
	"ADMIN_API_KEY":              true,
	"CACHE_ADMIN_KEY":            true,
	"CACHE_ISOLATION":            true,
	"DATA_REGION":                true,
	"METRICS_ENABLED":            true,
	"TRUST_PROXY":                true,
	"MAX_REQUEST_BODY_BYTES":     true,
	"HTTP_CSP":                   true,
	"HTTP_MAX_HEADER_BYTES":      true,
	"HTTP_PERMISSIONS_POLICY":    true,
	"HTTP_REFERRER_POLICY":       true,
	"CACHE_ENCRYPTION_KEY_PREV":  true, // key rotation — paired with HTTP/Redis shared-cache deployments
	"AUDIT_BUFFER_SIZE":          true,
	"AUDIT_MAX_BYTES":            true,
	"AUDIT_INCLUDE_REQUEST_BODY": true,
	"AUDIT_RETENTION_DAYS":       true,
}

// mcpJSONEnvKeys extracts the env keys configured in the repo-root .mcp.json.
func mcpJSONEnvKeys(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf(".mcp.json is not valid JSON: %v", err)
	}
	srv, ok := doc.MCPServers["web-researcher"]
	if !ok {
		t.Fatal(`.mcp.json missing mcpServers["web-researcher"]`)
	}
	keys := make([]string, 0, len(srv.Env))
	for k := range srv.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestMCPJSONOnlyReferencesRealEnvVars guards the reference STDIO config against
// drift: every env key in .mcp.json must be a variable the server actually reads
// (no typos, no stale keys). This is why a newly-added provider/feature var that
// is forgotten in .mcp.json — or a renamed one — fails CI instead of silently
// shipping a broken example.
func TestMCPJSONOnlyReferencesRealEnvVars(t *testing.T) {
	root := repoRootForTest(t)
	known := serverEnvVars(t, root)
	for _, k := range mcpJSONEnvKeys(t, root) {
		if !known[k] {
			t.Errorf(".mcp.json references %q, which the server does not read (typo or stale key — see internal/config/config.go)", k)
		}
	}
}

// TestMCPJSONExcludesHTTPOnlyVars enforces the design rule that .mcp.json is a
// STDIO client example: HTTP-transport-only vars (OAuth/CORS/rate-limit/Redis/
// admin/HTTP headers) must not appear, since they mislead local users. HTTP
// operators use docs/DEPLOYMENT.md.
func TestMCPJSONExcludesHTTPOnlyVars(t *testing.T) {
	root := repoRootForTest(t)
	for _, k := range mcpJSONEnvKeys(t, root) {
		if httpOnlyVars[k] {
			t.Errorf(".mcp.json lists HTTP-only var %q — it is a STDIO example; document HTTP-mode vars in docs/DEPLOYMENT.md instead", k)
		}
	}
}

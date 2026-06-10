package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// callArchive drives archive_source through the in-memory MCP client.
func callArchive(t *testing.T, deps Dependencies, url string) (map[string]any, bool) {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "archive_source", Arguments: map[string]any{"url": url}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
	return out, res.IsError
}

// archiveDeps wires a LinkVerifier whose SPN + wayback bases point at the given
// test servers (private IPs allowed so 127.0.0.1 works).
func archiveDeps(t *testing.T, saveBase, waybackBase string) Dependencies {
	t.Helper()
	deps := setupTestDeps()
	lv := scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})
	if saveBase != "" {
		lv.SetSaveBase(saveBase)
	}
	if waybackBase != "" {
		lv.SetWaybackBase(waybackBase)
	}
	deps.LinkVerifier = lv
	return deps
}

func TestArchiveSource_Archived(t *testing.T) {
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Location", "/web/20260101000000/https://example.com/x")
		w.WriteHeader(200)
	}))
	defer spn.Close()

	out, isErr := callArchive(t, archiveDeps(t, spn.URL+"/save/", ""), "https://example.com/x")
	if isErr {
		t.Fatal("unexpected tool error")
	}
	if out["status"] != "archived" {
		t.Errorf("status = %v, want archived", out["status"])
	}
	if out["captured"] != true {
		t.Errorf("captured = %v, want true", out["captured"])
	}
	if out["snapshotUrl"] != "https://web.archive.org/web/20260101000000/https://example.com/x" {
		t.Errorf("snapshotUrl = %v", out["snapshotUrl"])
	}
	if out["archivedAt"] == nil {
		t.Error("archived result should carry archivedAt")
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
}

func TestArchiveSource_ExistingFallback(t *testing.T) {
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }))
	defer spn.Close()
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"https://web.archive.org/web/2019/https://example.com/x","status":"200"}}}`))
	}))
	defer wb.Close()

	out, _ := callArchive(t, archiveDeps(t, spn.URL+"/save/", wb.URL), "https://example.com/x")
	if out["status"] != "existing" {
		t.Errorf("status = %v, want existing", out["status"])
	}
	if out["captured"] != false {
		t.Errorf("captured = %v, want false", out["captured"])
	}
	if out["snapshotUrl"] == nil {
		t.Error("existing fallback should carry a snapshotUrl")
	}
}

func TestArchiveSource_Pending(t *testing.T) {
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer spn.Close()
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{}}`))
	}))
	defer wb.Close()

	out, _ := callArchive(t, archiveDeps(t, spn.URL+"/save/", wb.URL), "https://example.com/x")
	if out["status"] != "pending" {
		t.Errorf("status = %v, want pending", out["status"])
	}
	if _, present := out["snapshotUrl"]; present {
		t.Errorf("pending should have no snapshotUrl, got %v", out["snapshotUrl"])
	}
}

func TestArchiveSource_Unavailable(t *testing.T) {
	// setupTestDeps has a nil LinkVerifier.
	out, isErr := callArchive(t, setupTestDeps(), "https://example.com/x")
	if isErr {
		t.Fatal("unavailable should be a graceful result, not a tool error")
	}
	if out["status"] != "unavailable" {
		t.Errorf("status = %v, want unavailable", out["status"])
	}
}

func TestArchiveSource_InputValidation(t *testing.T) {
	deps := setupTestDeps()
	cases := []struct {
		name, url string
	}{
		{"empty", ""},
		{"bad scheme", "ftp://example.com/x"},
		{"localhost", "http://localhost/x"},
		{"loopback ip", "http://127.0.0.1/x"},
		{"private ip", "http://10.0.0.5/x"},
		{"internal tld", "http://service.internal/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, isErr := callArchive(t, deps, c.url)
			if !isErr {
				t.Errorf("url %q should be rejected with a tool error", c.url)
			}
		})
	}
}

// TestArchiveSource_NoKeyLeak: the audit/result path must never contain IA keys.
func TestArchiveSource_NoKeyLeak(t *testing.T) {
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Location", "/web/20260101000000/https://example.com/x")
		w.WriteHeader(200)
	}))
	defer spn.Close()
	deps := setupTestDeps()
	lv := scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true, IAAccessKey: "SECRET_AK", IASecretKey: "SECRET_SK"})
	lv.SetSaveBase(spn.URL + "/save/")
	deps.LinkVerifier = lv

	out, _ := callArchive(t, deps, "https://example.com/x")
	blob, _ := json.Marshal(out)
	for _, secret := range []string{"SECRET_AK", "SECRET_SK"} {
		if strings.Contains(string(blob), secret) {
			t.Errorf("IA key %q leaked into the result payload: %s", secret, blob)
		}
	}
}

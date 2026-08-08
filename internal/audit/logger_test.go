package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHashChain_TwoInstancesIndependent proves the hash chain lives on the
// Logger instance, not in package-level state (#466 half 2, issue #555 §1.3):
// two Loggers fed byte-identical events must produce byte-identical hashes,
// and interleaving writes across both instances concurrently must never let
// one instance's chain affect the other's.
func TestHashChain_TwoInstancesIndependent(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	pathA := filepath.Join(dirA, "audit.log")
	pathB := filepath.Join(dirB, "audit.log")

	loggerA, err := NewLogger(Config{Enabled: true, OutputPath: pathA, BufferSize: 100})
	if err != nil {
		t.Fatalf("failed to create logger A: %v", err)
	}
	loggerB, err := NewLogger(Config{Enabled: true, OutputPath: pathB, BufferSize: 100})
	if err != nil {
		t.Fatalf("failed to create logger B: %v", err)
	}

	// Fixed timestamps/request IDs so the two instances see byte-identical
	// events — any divergence in the resulting hashes would mean chain state
	// leaked across instances (e.g. via a package-level var).
	events := make([]AuditEvent, 20)
	for i := range events {
		events[i] = AuditEvent{
			Timestamp: "2026-01-01T00:00:00Z",
			EventType: "tool_call",
			TenantID:  "tenant-x",
			UserID:    "user-x",
			RequestID: "req-fixed",
			Success:   true,
		}
	}

	// Interleave writes across both instances concurrently to maximize the
	// chance of exposing any shared/global state.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for _, e := range events {
			loggerA.Log(e)
		}
	}()
	go func() {
		defer wg.Done()
		for _, e := range events {
			loggerB.Log(e)
		}
	}()
	wg.Wait()

	loggerA.Close()
	loggerB.Close()

	dataA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatalf("failed to read log A: %v", err)
	}
	dataB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatalf("failed to read log B: %v", err)
	}

	if !bytes.Equal(dataA, dataB) {
		t.Fatalf("expected identical logs (byte-identical inputs) to produce byte-identical hash chains; got:\nA=%s\nB=%s", dataA, dataB)
	}

	// Both chains must independently verify.
	if err := VerifyChain(bytes.NewReader(dataA)); err != nil {
		t.Errorf("logger A's chain failed to verify: %v", err)
	}
	if err := VerifyChain(bytes.NewReader(dataB)); err != nil {
		t.Errorf("logger B's chain failed to verify: %v", err)
	}

	// Sanity: the first line's hash must match what a fresh chain (prevHash
	// "") would produce — confirms no residual state from a prior instance
	// or test leaked into this one.
	lines := bytes.Split(bytes.TrimSpace(dataA), []byte("\n"))
	var first AuditEvent
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("failed to unmarshal first line: %v", err)
	}
	if first.PrevHash != "" {
		t.Errorf("expected first event's PrevHash to be empty (fresh chain), got %q", first.PrevHash)
	}
	wantHash, err := hashEvent("", first)
	if err != nil {
		t.Fatalf("hashEvent: %v", err)
	}
	if first.Hash != wantHash {
		t.Errorf("first event hash %q does not match fresh-chain hash %q", first.Hash, wantHash)
	}
}

// TestHashChain_MemoryBoundedPerEvent proves chain-state memory is O(1) per
// event (#466 half 2, issue #555 §4.1): a Logger only ever retains the most
// recent event's Hash in l.prevHash — a single fixed-size (64 hex char)
// SHA-256 digest — regardless of how many events have already been written.
func TestHashChain_MemoryBoundedPerEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := NewLogger(Config{Enabled: true, OutputPath: path, BufferSize: 1000})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	const sha256HexLen = 64
	const numEvents = 5000
	for i := 0; i < numEvents; i++ {
		logger.Log(NewEvent("tool_call", "tenant-1", "user-1"))
	}
	// l.prevHash is deliberately unsynchronized (only processLoop, the single
	// writer goroutine, ever touches it — see the field's doc comment), so it
	// must only be read from the test goroutine after Close() joins that
	// goroutine (wg.Wait()), never while events may still be in flight.
	logger.Close()

	if got := len(logger.prevHash); got != sha256HexLen {
		t.Fatalf("after %d events, prevHash should still be exactly %d bytes (one fixed-size hash), got %d", numEvents, sha256HexLen, got)
	}

	// Confirm the Logger struct itself carries no per-event accumulator: the
	// only chain-related field is the single prevHash string. This is a
	// structural sanity check, not a reflection-based size assertion, since
	// Go gives no portable way to assert a struct's static size grew with N.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != numEvents {
		t.Fatalf("expected %d written events, got %d", numEvents, len(lines))
	}
	if err := VerifyChain(bytes.NewReader(data)); err != nil {
		t.Errorf("chain of %d events failed to verify: %v", numEvents, err)
	}
}

// TestVerifyChain_DetectsTampering proves VerifyChain flags a corrupted line
// (#466 half 2, issue #555 Proof lines): an intact chain verifies cleanly, and
// editing any single field in any single line breaks verification at that
// line.
func TestVerifyChain_DetectsTampering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := NewLogger(Config{Enabled: true, OutputPath: path, BufferSize: 100})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	for i := 0; i < 5; i++ {
		logger.Log(NewEvent("tool_call", "tenant-1", "user-1"))
	}
	logger.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}

	// Intact chain verifies.
	if err := VerifyChain(bytes.NewReader(data)); err != nil {
		t.Fatalf("intact chain should verify, got error: %v", err)
	}

	// Tamper with the middle line: edit a field's value (simulates an
	// operator editing the log to hide/alter an action) without recomputing
	// the hash.
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	var tampered AuditEvent
	if err := json.Unmarshal(lines[2], &tampered); err != nil {
		t.Fatalf("failed to unmarshal line 2: %v", err)
	}
	tampered.TenantID = "tenant-attacker-modified"
	tamperedLine, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("failed to marshal tampered event: %v", err)
	}
	lines[2] = tamperedLine
	tamperedData := bytes.Join(lines, []byte("\n"))

	err = VerifyChain(bytes.NewReader(tamperedData))
	if err == nil {
		t.Fatal("expected VerifyChain to detect tampering, got nil error")
	}
	if !errors.Is(err, ErrChainBroken) {
		t.Errorf("expected error to wrap ErrChainBroken, got: %v", err)
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("expected error to name line 3 (1-indexed), got: %v", err)
	}

	// Tampering with the recorded hash itself (not just content) must also
	// be caught. Start fresh from the untampered data and forge only line
	// 4's Hash field.
	var line4 AuditEvent
	origLines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if err := json.Unmarshal(origLines[3], &line4); err != nil {
		t.Fatalf("failed to unmarshal line 4: %v", err)
	}
	forgedHash := strings.Repeat("0", len(line4.Hash))
	lines2 := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	lines2[3] = []byte(strings.Replace(string(lines2[3]), line4.Hash, forgedHash, 1))
	tamperedHashData := bytes.Join(lines2, []byte("\n"))
	err = VerifyChain(bytes.NewReader(tamperedHashData))
	if err == nil {
		t.Fatal("expected VerifyChain to detect a forged hash, got nil error")
	}
	if !errors.Is(err, ErrChainBroken) {
		t.Errorf("expected error to wrap ErrChainBroken, got: %v", err)
	}

	// A log with no hash fields at all (pre-feature format) is reported as
	// broken, not silently accepted.
	legacy := `{"timestamp":"2026-01-01T00:00:00Z","event_type":"tool_call","tenant_id":"t","user_id":"u","request_id":"r","success":true}`
	if err := VerifyChain(strings.NewReader(legacy)); err == nil {
		t.Error("expected a legacy (no-hash) log line to fail verification")
	} else if !errors.Is(err, ErrChainBroken) {
		t.Errorf("expected legacy-log error to wrap ErrChainBroken, got: %v", err)
	}
}

// TestHashChain_SurvivesRestart proves a Logger that restarts (a new NewLogger
// call, e.g. after a process restart) against a pre-existing, non-empty audit
// log continues the hash chain from the file's last recorded event rather
// than resetting prevHash to "" — otherwise the first post-restart event's
// PrevHash="" would not match the pre-restart file's real last Hash, and
// VerifyChain would misreport tampering at every restart boundary (#466 half
// 2, GitHub Copilot review feedback on PR #566).
func TestHashChain_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger1, err := NewLogger(Config{Enabled: true, OutputPath: path, BufferSize: 100})
	if err != nil {
		t.Fatalf("failed to create first logger: %v", err)
	}
	for i := 0; i < 3; i++ {
		logger1.Log(NewEvent("tool_call", "tenant-1", "user-1"))
	}
	logger1.Close()

	// Simulate a process restart: a brand new Logger instance opens the same
	// (now non-empty) file in append mode.
	logger2, err := NewLogger(Config{Enabled: true, OutputPath: path, BufferSize: 100})
	if err != nil {
		t.Fatalf("failed to create second logger: %v", err)
	}
	for i := 0; i < 3; i++ {
		logger2.Log(NewEvent("tool_call", "tenant-1", "user-1"))
	}
	logger2.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 6 {
		t.Fatalf("expected 6 combined events, got %d", len(lines))
	}

	// The whole file — pre- and post-restart events together — must verify as
	// one unbroken chain.
	if err := VerifyChain(bytes.NewReader(data)); err != nil {
		t.Errorf("expected chain to survive a Logger restart with no break, got: %v", err)
	}

	// Explicitly confirm the seam: line 4 (the first post-restart event) must
	// carry PrevHash equal to line 3's (the last pre-restart event's) Hash —
	// not the empty string a fresh chain would use.
	var line3, line4 AuditEvent
	if err := json.Unmarshal(lines[2], &line3); err != nil {
		t.Fatalf("failed to unmarshal line 3: %v", err)
	}
	if err := json.Unmarshal(lines[3], &line4); err != nil {
		t.Fatalf("failed to unmarshal line 4: %v", err)
	}
	if line4.PrevHash != line3.Hash {
		t.Errorf("first post-restart event's PrevHash = %q, want %q (last pre-restart event's Hash)", line4.PrevHash, line3.Hash)
	}
	if line4.PrevHash == "" {
		t.Error("first post-restart event's PrevHash is empty — restart reset the chain instead of continuing it")
	}
}

// TestWebhookExport_NonBlockingTimeout proves the SIEM webhook export never
// blocks the audit write path beyond its configured timeout, even against an
// unresponsive receiver, and that local file writes proceed unaffected (#466
// half 2).
func TestWebhookExport_NonBlockingTimeout(t *testing.T) {
	release := make(chan struct{})
	var gotRequests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequests.Add(1)
		<-release // hang until the test explicitly releases the handler
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	const webhookTimeout = 200 * time.Millisecond
	logger, err := NewLogger(Config{
		Enabled:        true,
		OutputPath:     path,
		BufferSize:     100,
		WebhookURL:     srv.URL,
		WebhookTimeout: webhookTimeout,
	})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	logger.Log(NewEvent("tool_call", "tenant-1", "user-1"))

	closeStart := time.Now()
	logger.Close()
	closeElapsed := time.Since(closeStart)

	// Close() waits for in-flight webhook POSTs, each bounded by
	// webhookTimeout, so it must return in a small bounded multiple of that
	// timeout — never hang indefinitely on the unresponsive server.
	if closeElapsed > 3*webhookTimeout {
		t.Errorf("Close() took %v, expected it to return within a small multiple of the %v webhook timeout", closeElapsed, webhookTimeout)
	}

	// The local write path must be unaffected by the slow/hanging webhook.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 event written to local file despite webhook hang, got %d", len(lines))
	}
	if err := VerifyChain(bytes.NewReader(data)); err != nil {
		t.Errorf("local chain should still verify: %v", err)
	}

	if gotRequests.Load() != 1 {
		t.Errorf("expected the webhook to have received exactly 1 request, got %d", gotRequests.Load())
	}
}

// TestWebhookExport_DeliversEvent proves a healthy webhook receiver actually
// gets the event JSON (including the hash-chain fields), so the SIEM export
// is a real integration and not just a fire-and-forget no-op.
func TestWebhookExport_DeliversEvent(t *testing.T) {
	received := make(chan AuditEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		var event AuditEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("failed to decode webhook body: %v", err)
		}
		received <- event
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := NewLogger(Config{
		Enabled:    true,
		OutputPath: path,
		BufferSize: 100,
		WebhookURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	logger.Log(NewEvent("admin_action", "tenant-9", "user-9"))
	logger.Close()

	select {
	case event := <-received:
		if event.EventType != "admin_action" {
			t.Errorf("expected event_type 'admin_action', got %q", event.EventType)
		}
		if event.Hash == "" {
			t.Error("expected webhook payload to include the computed Hash field")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook never received the event")
	}

	if got := logger.WebhookDroppedCount(); got != 0 {
		t.Errorf("expected 0 dropped webhook exports, got %d", got)
	}
}

// TestWebhookExport_Disabled proves that leaving WebhookURL empty (the
// default) makes exportToWebhook a pure no-op — no goroutines, no network
// calls, no behavior change to the local write path.
func TestWebhookExport_Disabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := NewLogger(Config{Enabled: true, OutputPath: path, BufferSize: 100})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	logger.Log(NewEvent("tool_call", "tenant-1", "user-1"))
	logger.Close()

	if got := logger.WebhookDroppedCount(); got != 0 {
		t.Errorf("expected 0 (webhook disabled), got %d", got)
	}
}

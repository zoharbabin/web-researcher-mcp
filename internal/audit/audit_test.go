package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEventSerialization(t *testing.T) {
	event := AuditEvent{
		Timestamp: "2025-01-15T10:30:00Z",
		EventType: "tool_call",
		TenantID:  "tenant-123",
		UserID:    "user-456",
		SessionID: "sess-789",
		ToolName:  "web_search",
		RequestID: "req-001",
		SourceIP:  "192.168.1.1",
		Duration:  150,
		Success:   true,
		ErrorCode: "",
		Metadata:  map[string]any{"query": "test search"},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}

	var decoded AuditEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal event: %v", err)
	}

	if decoded.EventType != "tool_call" {
		t.Errorf("expected event_type 'tool_call', got %q", decoded.EventType)
	}
	if decoded.TenantID != "tenant-123" {
		t.Errorf("expected tenant_id 'tenant-123', got %q", decoded.TenantID)
	}
	if decoded.ToolName != "web_search" {
		t.Errorf("expected tool_name 'web_search', got %q", decoded.ToolName)
	}
	if decoded.Duration != 150 {
		t.Errorf("expected duration 150, got %d", decoded.Duration)
	}
	if decoded.Metadata["query"] != "test search" {
		t.Errorf("expected metadata query 'test search', got %v", decoded.Metadata["query"])
	}

	// Verify optional fields are omitted when empty
	eventMinimal := AuditEvent{
		Timestamp: "2025-01-15T10:30:00Z",
		EventType: "auth_success",
		TenantID:  "t1",
		UserID:    "u1",
		RequestID: "r1",
		Success:   true,
	}
	data, _ = json.Marshal(eventMinimal)
	dataStr := string(data)
	if bytes.Contains([]byte(dataStr), []byte("session_id")) {
		t.Error("empty session_id should be omitted with omitempty")
	}
	if bytes.Contains([]byte(dataStr), []byte("tool_name")) {
		t.Error("empty tool_name should be omitted with omitempty")
	}
}

func TestAsyncDelivery(t *testing.T) {
	var buf safeBuffer
	logger := &Logger{
		writer:  &buf,
		eventCh: make(chan AuditEvent, 1000),
		done:    make(chan struct{}),
	}
	logger.wg.Add(1)
	go logger.processLoop()

	const numEvents = 100
	for i := 0; i < numEvents; i++ {
		logger.Log(NewEvent("tool_call", "tenant-1", "user-1"))
	}

	logger.Close()

	// Count lines written
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != numEvents {
		t.Fatalf("expected %d events written, got %d", numEvents, len(lines))
	}

	// Verify each line is valid JSON
	for i, line := range lines {
		var event AuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if event.EventType != "tool_call" {
			t.Fatalf("line %d: expected event_type 'tool_call', got %q", i, event.EventType)
		}
	}
}

func TestGracefulDrain(t *testing.T) {
	var buf safeBuffer
	logger := &Logger{
		writer:  &buf,
		eventCh: make(chan AuditEvent, 100),
		done:    make(chan struct{}),
	}
	logger.wg.Add(1)
	go logger.processLoop()

	// Enqueue events without giving the goroutine time to process
	for i := 0; i < 50; i++ {
		logger.Log(NewEvent("session_created", "t1", "u1"))
	}

	// Close should drain all buffered events
	done := make(chan struct{})
	go func() {
		logger.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success — Close returned
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return within 5 seconds — drain may be stuck")
	}

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 50 {
		t.Fatalf("expected 50 drained events, got %d", len(lines))
	}
}

func TestNoopDoesNotPanic(t *testing.T) {
	noop := NewNoop()

	// Should not panic
	noop.Log(AuditEvent{EventType: "test"})
	noop.Log(NewEvent("auth_failure", "t", "u"))
	noop.Close()
	// Call again after close — still should not panic
	noop.Log(AuditEvent{})
	noop.Close()
}

func TestFileOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	cfg := Config{
		Enabled:    true,
		OutputPath: path,
		BufferSize: 100,
	}

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	logger.Log(NewEvent("admin_action", "tenant-abc", "admin-user"))
	logger.Log(NewEvent("token_revoked", "tenant-abc", "admin-user"))
	logger.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read audit log file: %v", err)
	}

	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines in file, got %d", len(lines))
	}

	var event AuditEvent
	if err := json.Unmarshal(lines[0], &event); err != nil {
		t.Fatalf("line 0 is not valid JSON: %v", err)
	}
	if event.EventType != "admin_action" {
		t.Errorf("expected 'admin_action', got %q", event.EventType)
	}
}

func TestNewLoggerReturnsNilWhenDisabled(t *testing.T) {
	logger, err := NewLogger(Config{Enabled: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger != nil {
		t.Fatal("expected nil logger when disabled")
	}
}

func TestNewEventFields(t *testing.T) {
	event := NewEvent("rate_limited", "t-1", "u-1")
	if event.EventType != "rate_limited" {
		t.Errorf("expected 'rate_limited', got %q", event.EventType)
	}
	if event.TenantID != "t-1" {
		t.Errorf("expected 't-1', got %q", event.TenantID)
	}
	if event.RequestID == "" {
		t.Error("expected non-empty RequestID")
	}
	if event.Timestamp == "" {
		t.Error("expected non-empty Timestamp")
	}
	if !event.Success {
		t.Error("expected Success=true by default")
	}
}

// TestNewEventPodID verifies every event carries a pod identifier for
// cross-pod correlation in multi-instance HTTP deployments (#43). The value is
// resolved once at package init from HOSTNAME or os.Hostname(); on any normal
// host at least one of those is non-empty.
func TestNewEventPodID(t *testing.T) {
	event := NewEvent("tool_call", "t-1", "u-1")
	if event.PodID != podID {
		t.Errorf("expected PodID=%q (package value), got %q", podID, event.PodID)
	}
	if event.PodID == "" {
		t.Skip("no hostname available in this environment; PodID legitimately empty")
	}
}

func TestSwapFileSpill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Tiny buffer (2) forces swap spill quickly
	cfg := Config{
		Enabled:      true,
		OutputPath:   path,
		BufferSize:   2,
		MaxSwapBytes: 1024 * 1024,
	}

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Fill the channel (buffer=2) by pausing the processLoop
	// We can't truly pause it, but if we blast enough events fast enough some will spill
	const numEvents = 200
	for i := 0; i < numEvents; i++ {
		logger.Log(NewEvent("tool_call", "tenant-swap", "user-swap"))
	}

	logger.Close()

	// All events should have been written (channel + swap replay)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read audit log: %v", err)
	}

	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))

	// The key invariant: spilled + written via channel == numEvents (no drops)
	if logger.Dropped.Load() != 0 {
		t.Errorf("expected 0 dropped events, got %d", logger.Dropped.Load())
	}

	// All non-dropped events should appear in the output
	totalProcessed := int64(len(lines))
	totalExpected := int64(numEvents) - logger.Dropped.Load()
	if totalProcessed < totalExpected {
		t.Fatalf("expected %d events in output, got %d (spilled=%d, dropped=%d)",
			totalExpected, totalProcessed, logger.Spilled.Load(), logger.Dropped.Load())
	}

	// Verify swap file is cleaned up
	for _, p := range []string{logger.swapPath, logger.swapPath + ".replay"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("swap file %q should be removed after replay", p)
		}
	}

	// Verify spill counter > 0 (with buffer=2, most events hit the swap)
	if logger.Spilled.Load() == 0 {
		t.Log("warning: no events spilled (processLoop drained fast enough)")
	}
	t.Logf("written=%d spilled=%d dropped=%d", len(lines), logger.Spilled.Load(), logger.Dropped.Load())
}

func TestSwapFileMaxSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Very small max swap: 500 bytes — will fill up quickly
	cfg := Config{
		Enabled:      true,
		OutputPath:   path,
		BufferSize:   1,
		MaxSwapBytes: 500,
	}

	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Blast many events — some will be dropped when swap is full
	for i := 0; i < 500; i++ {
		logger.Log(NewEvent("tool_call", "tenant-max", "user-max"))
	}

	logger.Close()

	// Some events should have been dropped due to swap cap
	if logger.Dropped.Load() == 0 {
		t.Log("warning: expected some dropped events with 500-byte swap cap")
	}

	t.Logf("spilled=%d dropped=%d", logger.Spilled.Load(), logger.Dropped.Load())
}

func TestMaskSecrets(t *testing.T) {
	t.Parallel()

	// Test fixtures are assembled at runtime from prefix + filler so no
	// contiguous credential-shaped literal ever appears in source — this keeps
	// the values out of automated secret scanners (GitGuardian, gitleaks) while
	// still producing a fully key-shaped string for the regexes to match. None
	// of these are real keys; they are synthetic, fixed test vectors.
	// 64-char filler so every slice below is in bounds.
	const filler = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	googleKey := "AIza" + filler[:35] // "AIza" + 35 chars matches reGoogleKey
	skKey := "sk-" + "proj-" + filler[:24]
	bsaKey := "BSA" + filler[:24]
	hexKey := filler // exactly 64 hex chars matches reHex64
	bearerTok := "tok-" + filler[:20]
	tokenVal := "val-" + filler[:16]
	accessTok := "at-" + filler[:20]
	// Exa key: UUID-shaped, no fixed prefix — only the x-api-key header rule can
	// catch it. Assembled from filler so no literal UUID appears in source.
	exaKey := filler[:8] + "-" + filler[8:12] + "-" + filler[12:16] + "-" + filler[16:20] + "-" + filler[20:32]

	tests := []struct {
		name         string
		in           string
		wantRedacted bool   // a [REDACTED] marker must appear
		wantAbsent   string // this substring must NOT survive in the output
		wantExact    string // if set, output must equal this exactly
	}{
		{name: "empty", in: "", wantExact: ""},
		{
			name:         "google api key",
			in:           "request failed: https://www.googleapis.com/customsearch/v1?key=" + googleKey + "&cx=1",
			wantRedacted: true,
			wantAbsent:   googleKey,
		},
		{
			name:         "bearer token",
			in:           "Authorization: Bearer " + bearerTok,
			wantRedacted: true,
			wantAbsent:   bearerTok,
		},
		{
			name:         "openai sk key",
			in:           "auth error with " + skKey,
			wantRedacted: true,
			wantAbsent:   skKey,
		},
		{
			name:         "brave bsa key",
			in:           "X-Subscription-Token: " + bsaKey,
			wantRedacted: true,
			wantAbsent:   bsaKey,
		},
		{
			name:         "64 hex encryption key",
			in:           "CACHE_ENCRYPTION_KEY=" + hexKey,
			wantRedacted: true,
			wantAbsent:   hexKey,
		},
		{
			name:         "token query param",
			in:           "https://api.example.com/v1/search?q=cats&token=" + tokenVal + "&page=2",
			wantRedacted: true,
			wantAbsent:   tokenVal,
		},
		{
			name:         "access_token query param",
			in:           "callback?access_token=" + accessTok + "&state=xyz",
			wantRedacted: true,
			wantAbsent:   accessTok,
		},
		{
			name:         "exa x-api-key header",
			in:           "x-api-key: " + exaKey,
			wantRedacted: true,
			wantAbsent:   exaKey,
		},
		{
			name:         "exa key in header map dump",
			in:           `map[X-Api-Key:[` + exaKey + `] Accept:[application/json]]`,
			wantRedacted: true,
			wantAbsent:   exaKey,
		},
		{
			name:      "bare uuid (e.g. requestId) stays readable",
			in:        "exa requestId " + exaKey + " completed",
			wantExact: "exa requestId " + exaKey + " completed",
		},
		{
			name:      "normal text untouched",
			in:        "search_and_scrape failed: no results for quantum computing",
			wantExact: "search_and_scrape failed: no results for quantum computing",
		},
		{
			name:      "short hex not matched",
			in:        "color #abc123 and id deadbeef",
			wantExact: "color #abc123 and id deadbeef",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MaskSecrets(tt.in)
			if tt.wantExact != "" || tt.name == "empty" {
				if got != tt.wantExact {
					t.Fatalf("MaskSecrets(%q) = %q, want exact %q", tt.in, got, tt.wantExact)
				}
				return
			}
			if tt.wantRedacted && !strings.Contains(got, redacted) {
				t.Errorf("MaskSecrets(%q) = %q, expected a %s marker", tt.in, got, redacted)
			}
			if tt.wantAbsent != "" && strings.Contains(got, tt.wantAbsent) {
				t.Errorf("MaskSecrets(%q) = %q, secret %q survived", tt.in, got, tt.wantAbsent)
			}
		})
	}
}

func TestMaskSecretsIdempotent(t *testing.T) {
	t.Parallel()
	// Assembled at runtime (see TestMaskSecrets) so no key-shaped literal lands
	// in source; still exercises the google-key + bearer rules.
	in := "key=" + "AIza" + "0123456789abcdefghijklmnopqrstuv012" + " and Bearer " + "tok-0123456789abcdef"
	once := MaskSecrets(in)
	twice := MaskSecrets(once)
	if once != twice {
		t.Errorf("MaskSecrets not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}

func TestRotationNoEventLoss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Tiny MaxBytes forces frequent rotation; big buffer so nothing spills/drops.
	cfg := Config{
		Enabled:    true,
		OutputPath: path,
		BufferSize: 10000,
		MaxBytes:   2048, // ~tens of events per file
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	const numEvents = 2000
	for i := 0; i < numEvents; i++ {
		ev := NewEvent("tool_call", "tenant-rot", "user-rot")
		ev.Metadata = map[string]any{"seq": i}
		logger.Log(ev)
	}
	logger.Close()

	if logger.Dropped.Load() != 0 {
		t.Fatalf("expected 0 dropped events, got %d", logger.Dropped.Load())
	}
	if logger.Rotations.Load() == 0 {
		t.Fatal("expected at least one rotation with 2KB MaxBytes")
	}

	// Count events across the active file + every rotated sibling.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	total := 0
	for _, e := range entries {
		name := e.Name()
		if name != "audit.log" && !strings.HasPrefix(name, "audit.log.") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			t.Fatalf("ReadFile %s: %v", name, rerr)
		}
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 {
			continue
		}
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			var ev AuditEvent
			if json.Unmarshal(line, &ev) != nil {
				t.Fatalf("invalid JSON line in %s: %q", name, line)
			}
			total++
		}
	}
	if total != numEvents {
		t.Fatalf("expected %d events across active+rotated files, got %d (rotations=%d)",
			numEvents, total, logger.Rotations.Load())
	}
}

func TestRetentionDeletesOldFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Pre-create rotated siblings: two old (beyond retention), one recent.
	oldName := filepath.Join(dir, "audit.log.20200101T000000.000000000Z")
	old2Name := filepath.Join(dir, "audit.log.20200102T000000.000000000Z")
	recentName := filepath.Join(dir, "audit.log.recent")
	for _, n := range []string{oldName, old2Name, recentName} {
		if err := os.WriteFile(n, []byte("{}\n"), 0600); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}
	// Age the two "old" files well beyond the retention window.
	past := time.Now().Add(-200 * 24 * time.Hour)
	for _, n := range []string{oldName, old2Name} {
		if err := os.Chtimes(n, past, past); err != nil {
			t.Fatalf("chtimes %s: %v", n, err)
		}
	}

	cfg := Config{
		Enabled:       true,
		OutputPath:    path,
		BufferSize:    100,
		RetentionDays: 180,
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	// Cleanup runs at startup inside processLoop; give it a moment.
	logger.Log(NewEvent("tool_call", "t", "u"))
	logger.Close()

	if _, err := os.Stat(oldName); !os.IsNotExist(err) {
		t.Errorf("old rotated file should have been deleted: %v", err)
	}
	if _, err := os.Stat(old2Name); !os.IsNotExist(err) {
		t.Errorf("second old rotated file should have been deleted: %v", err)
	}
	if _, err := os.Stat(recentName); err != nil {
		t.Errorf("recent rotated file should be retained: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("active audit file must never be deleted: %v", err)
	}
}

func TestRetentionDisabledByZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	oldName := filepath.Join(dir, "audit.log.20200101T000000.000000000Z")
	if err := os.WriteFile(oldName, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	past := time.Now().Add(-500 * 24 * time.Hour)
	if err := os.Chtimes(oldName, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	cfg := Config{
		Enabled:       true,
		OutputPath:    path,
		BufferSize:    100,
		RetentionDays: 0, // disabled
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	logger.Log(NewEvent("tool_call", "t", "u"))
	logger.Close()

	if _, err := os.Stat(oldName); err != nil {
		t.Errorf("with RetentionDays=0 nothing should be deleted: %v", err)
	}
}

func TestRotationDisabledByZeroMaxBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	cfg := Config{
		Enabled:    true,
		OutputPath: path,
		BufferSize: 1000,
		MaxBytes:   0, // disabled
	}
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	for i := 0; i < 500; i++ {
		logger.Log(NewEvent("tool_call", "t", "u"))
	}
	logger.Close()

	if logger.Rotations.Load() != 0 {
		t.Errorf("expected 0 rotations with MaxBytes=0, got %d", logger.Rotations.Load())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audit.log.") {
			t.Errorf("unexpected rotated sibling with rotation disabled: %s", e.Name())
		}
	}
}

func TestIncludeRequestBodyFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	on, err := NewLogger(Config{Enabled: true, OutputPath: filepath.Join(dir, "on.log"), IncludeRequestBody: true})
	if err != nil {
		t.Fatalf("NewLogger on: %v", err)
	}
	defer on.Close()
	if !on.IncludeRequestBody() {
		t.Error("expected IncludeRequestBody()=true")
	}

	off, err := NewLogger(Config{Enabled: true, OutputPath: filepath.Join(dir, "off.log")})
	if err != nil {
		t.Fatalf("NewLogger off: %v", err)
	}
	defer off.Close()
	if off.IncludeRequestBody() {
		t.Error("expected IncludeRequestBody()=false by default")
	}

	if NewNoop().IncludeRequestBody() {
		t.Error("Noop.IncludeRequestBody() must be false")
	}
}

// safeBuffer is a concurrency-safe bytes.Buffer for use in tests.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

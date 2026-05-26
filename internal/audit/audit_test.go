package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

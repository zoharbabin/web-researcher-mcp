package audit

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// AuditEvent represents a single auditable action in the system.
type AuditEvent struct {
	Timestamp string         `json:"timestamp"`
	EventType string         `json:"event_type"`
	TenantID  string         `json:"tenant_id"`
	UserID    string         `json:"user_id"`
	SessionID string         `json:"session_id,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	RequestID string         `json:"request_id"`
	SourceIP  string         `json:"source_ip,omitempty"`
	Duration  int64          `json:"duration_ms,omitempty"`
	Success   bool           `json:"success"`
	ErrorCode string         `json:"error_code,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// NewEvent creates a new AuditEvent with timestamp and request ID pre-filled.
func NewEvent(eventType, tenantID, userID string) AuditEvent {
	return AuditEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		EventType: eventType,
		TenantID:  tenantID,
		UserID:    userID,
		RequestID: uuid.New().String(),
		Success:   true,
	}
}

// Auditor is the interface for audit logging.
type Auditor interface {
	Log(event AuditEvent)
	Close()
}

// Config holds configuration for the audit logger.
type Config struct {
	Enabled            bool
	OutputPath         string // empty = stderr, path = file
	BufferSize         int
	MaxSwapBytes       int64  // max swap file size before dropping (default 50MB)
	IncludeRequestBody bool
}

// Logger is a goroutine-safe, channel-based audit logger that writes
// structured JSON audit events to a dedicated writer.
type Logger struct {
	writer  io.Writer
	file    *os.File // non-nil if we opened a file (for closing)
	eventCh chan AuditEvent
	done    chan struct{}
	wg      sync.WaitGroup

	// Swap file spill — preserves events when channel is full
	swapMu       sync.Mutex
	swapFile     *os.File
	swapPath     string
	swapSize     int64
	maxSwapBytes int64
	Spilled      atomic.Int64 // count of events spilled to swap (exported for metrics)
	Dropped      atomic.Int64 // count of events dropped (swap also full)
}

// NewLogger creates a new audit Logger from the given config.
// It starts a background goroutine to process events.
func NewLogger(cfg Config) (*Logger, error) {
	if !cfg.Enabled {
		return nil, nil // caller should use NewNoop() instead
	}

	bufSize := cfg.BufferSize
	if bufSize <= 0 {
		bufSize = 1000
	}

	var writer io.Writer
	var file *os.File

	if cfg.OutputPath == "" {
		writer = os.Stderr
	} else {
		f, err := os.OpenFile(cfg.OutputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, err
		}
		file = f
		writer = f
	}

	maxSwap := cfg.MaxSwapBytes
	if maxSwap <= 0 {
		maxSwap = 50 * 1024 * 1024 // 50MB
	}

	// Swap file lives next to the audit log (or in temp dir for stderr mode)
	swapDir := os.TempDir()
	if cfg.OutputPath != "" {
		swapDir = filepath.Dir(cfg.OutputPath)
	}
	swapPath := filepath.Join(swapDir, ".audit-swap.jsonl")

	l := &Logger{
		writer:       writer,
		file:         file,
		eventCh:      make(chan AuditEvent, bufSize),
		done:         make(chan struct{}),
		swapPath:     swapPath,
		maxSwapBytes: maxSwap,
	}

	l.wg.Add(1)
	go l.processLoop()

	return l, nil
}

// Log enqueues an audit event for async writing. Non-blocking; if the
// channel buffer is full, the event is spilled to a swap file on disk.
// Events are only dropped if the swap file also exceeds its size cap.
func (l *Logger) Log(event AuditEvent) {
	select {
	case l.eventCh <- event:
	default:
		l.spillToSwap(event)
	}
}

func (l *Logger) spillToSwap(event AuditEvent) {
	l.swapMu.Lock()
	defer l.swapMu.Unlock()

	if l.swapSize >= l.maxSwapBytes {
		l.Dropped.Add(1)
		return
	}

	if l.swapFile == nil {
		f, err := os.OpenFile(l.swapPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			l.Dropped.Add(1)
			return
		}
		l.swapFile = f
	}

	data, err := json.Marshal(event)
	if err != nil {
		l.Dropped.Add(1)
		return
	}
	data = append(data, '\n')

	n, err := l.swapFile.Write(data)
	if err != nil {
		l.Dropped.Add(1)
		return
	}
	l.swapSize += int64(n)
	l.Spilled.Add(1)
}

// Close signals the logger to drain remaining events and stop.
// It blocks until all buffered events (channel + swap file) are written.
func (l *Logger) Close() {
	close(l.eventCh)
	l.wg.Wait()
	close(l.done)

	l.swapMu.Lock()
	if l.swapFile != nil {
		l.swapFile.Close()
		l.swapFile = nil
	}
	l.swapMu.Unlock()

	if l.file != nil {
		l.file.Close()
	}
}

func (l *Logger) processLoop() {
	defer l.wg.Done()
	enc := json.NewEncoder(l.writer)

	for event := range l.eventCh {
		_ = enc.Encode(event)

		// After draining available channel events, replay swap if it has data
		if len(l.eventCh) == 0 {
			l.replaySwap(enc)
		}
	}

	// Final replay on shutdown
	l.replaySwap(enc)
}

func (l *Logger) replaySwap(enc *json.Encoder) {
	l.swapMu.Lock()
	if l.swapFile == nil || l.swapSize == 0 {
		l.swapMu.Unlock()
		return
	}
	// Close the write handle and atomically rename so concurrent spillToSwap
	// creates a fresh file rather than appending to the one we're about to read.
	l.swapFile.Close()
	l.swapFile = nil
	l.swapSize = 0
	replayPath := l.swapPath + ".replay"
	if err := os.Rename(l.swapPath, replayPath); err != nil {
		l.swapMu.Unlock()
		return
	}
	l.swapMu.Unlock()

	f, err := os.Open(replayPath)
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	for scanner.Scan() {
		var event AuditEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			_ = enc.Encode(event)
		}
	}
	f.Close()
	os.Remove(replayPath)
}

// Noop is an Auditor implementation that does nothing.
// Used when auditing is disabled.
type Noop struct{}

// NewNoop returns a no-op auditor.
func NewNoop() *Noop {
	return &Noop{}
}

func (n *Noop) Log(_ AuditEvent) {}
func (n *Noop) Close()           {}

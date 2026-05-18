package audit

import (
	"encoding/json"
	"io"
	"os"
	"sync"
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

	l := &Logger{
		writer:  writer,
		file:    file,
		eventCh: make(chan AuditEvent, bufSize),
		done:    make(chan struct{}),
	}

	l.wg.Add(1)
	go l.processLoop()

	return l, nil
}

// Log enqueues an audit event for async writing. Non-blocking; if the
// buffer is full the event is dropped (defense against backpressure).
func (l *Logger) Log(event AuditEvent) {
	select {
	case l.eventCh <- event:
	default:
		// Buffer full — drop event to avoid blocking callers.
	}
}

// Close signals the logger to drain remaining events and stop.
// It blocks until all buffered events are written.
func (l *Logger) Close() {
	close(l.eventCh)
	l.wg.Wait()
	close(l.done)
	if l.file != nil {
		l.file.Close()
	}
}

func (l *Logger) processLoop() {
	defer l.wg.Done()
	enc := json.NewEncoder(l.writer)
	for event := range l.eventCh {
		_ = enc.Encode(event)
	}
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

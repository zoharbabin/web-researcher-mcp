package audit

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	PodID     string         `json:"pod_id,omitempty"`
	SourceIP  string         `json:"source_ip,omitempty"`
	Duration  int64          `json:"duration_ms,omitempty"`
	Success   bool           `json:"success"`
	ErrorCode string         `json:"error_code,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// podID identifies the process/instance emitting audit events, enabling
// cross-pod correlation in multi-instance HTTP deployments (e.g. which pod
// dropped events under backpressure). Resolved once: HOSTNAME if set (the
// convention in container orchestrators), else os.Hostname(). Empty when
// neither is available, in which case the field is omitted from output.
var podID = resolvePodID()

func resolvePodID() string {
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return ""
}

// NewEvent creates a new AuditEvent with timestamp, request ID, and pod ID
// pre-filled.
func NewEvent(eventType, tenantID, userID string) AuditEvent {
	return AuditEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		EventType: eventType,
		TenantID:  tenantID,
		UserID:    userID,
		RequestID: uuid.New().String(),
		PodID:     podID,
		Success:   true,
	}
}

// Auditor is the interface for audit logging.
type Auditor interface {
	Log(event AuditEvent)
	// IncludeRequestBody reports whether request bodies (e.g. raw query text)
	// may be attached to audit metadata. When false, callers must record only
	// non-sensitive derivatives (length/hash). Controlled by
	// AUDIT_INCLUDE_REQUEST_BODY; defaults to false (privacy-preserving).
	IncludeRequestBody() bool
	Close()
}

// Config holds configuration for the audit logger.
type Config struct {
	Enabled            bool
	OutputPath         string // empty = stderr, path = file
	BufferSize         int
	MaxSwapBytes       int64 // max swap file size before dropping (default 50MB)
	IncludeRequestBody bool
	// MaxBytes is the size threshold (in bytes) at which the active audit file
	// is rotated to a timestamped sibling. <=0 disables rotation. File output
	// only — ignored for stderr/STDIO mode.
	MaxBytes int64
	// RetentionDays deletes rotated audit siblings older than this many days,
	// at startup and hourly. 0 disables cleanup. File output only.
	RetentionDays int
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

	// Rotation + retention (file output only). Mutated/read only by
	// processLoop (and NewLogger before the goroutine starts), so they need
	// no lock — Log() never touches them, keeping it non-blocking.
	outputPath    string
	maxBytes      int64
	retentionDays int
	curSize       int64        // bytes written to the active file since (re)open
	Rotations     atomic.Int64 // count of file rotations (exported for metrics)

	// includeRequestBody mirrors Config.IncludeRequestBody; read-only after
	// construction, so it is safe to expose without synchronization.
	includeRequestBody bool
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
	var curSize int64

	if cfg.OutputPath == "" {
		writer = os.Stderr
	} else {
		f, err := os.OpenFile(cfg.OutputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, err
		}
		file = f
		writer = f
		if info, statErr := f.Stat(); statErr == nil {
			curSize = info.Size()
		}
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
		writer:             writer,
		file:               file,
		eventCh:            make(chan AuditEvent, bufSize),
		done:               make(chan struct{}),
		swapPath:           swapPath,
		maxSwapBytes:       maxSwap,
		outputPath:         cfg.OutputPath,
		maxBytes:           cfg.MaxBytes,
		retentionDays:      cfg.RetentionDays,
		curSize:            curSize,
		includeRequestBody: cfg.IncludeRequestBody,
	}

	l.wg.Add(1)
	go l.processLoop()

	return l, nil
}

// IncludeRequestBody reports whether request bodies may be attached to audit
// metadata. See the Auditor interface for semantics.
func (l *Logger) IncludeRequestBody() bool { return l.includeRequestBody }

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
		_ = l.swapFile.Close()
		l.swapFile = nil
	}
	l.swapMu.Unlock()

	if l.file != nil {
		_ = l.file.Close()
	}
}

func (l *Logger) processLoop() {
	defer l.wg.Done()

	// Retention cleanup at startup, then hourly on a ticker. Both run on this
	// single goroutine so Log() stays non-blocking and no extra goroutine is
	// spawned. The ticker is a no-op for stderr mode or when retention is off.
	l.cleanupRetention()
	var tickC <-chan time.Time
	if l.file != nil && l.retentionDays > 0 {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		tickC = ticker.C
	}

	enc := json.NewEncoder(l.writer)

	for {
		select {
		case event, ok := <-l.eventCh:
			if !ok {
				// Channel closed: final replay and return.
				l.replaySwap(enc)
				return
			}
			enc = l.writeEvent(enc, event)
			if len(l.eventCh) == 0 {
				enc = l.replaySwap(enc)
			}
		case <-tickC:
			l.cleanupRetention()
		}
	}
}

// writeEvent encodes one event, tracks the active file size, and rotates the
// file when it would exceed maxBytes. Encoding is best-effort (errors ignored)
// to match the prior behavior; rotation is file-output only. It returns the
// encoder to use for the next write (rebound after a rotation).
func (l *Logger) writeEvent(enc *json.Encoder, event AuditEvent) *json.Encoder {
	if l.file == nil || l.maxBytes <= 0 {
		_ = enc.Encode(event)
		return enc
	}

	data, err := json.Marshal(event)
	if err != nil {
		return enc
	}
	data = append(data, '\n')

	// Rotate before writing if this line would push us over the threshold and
	// the file already holds at least one event (avoid rotating an empty file).
	if l.curSize > 0 && l.curSize+int64(len(data)) > l.maxBytes {
		if newEnc, ok := l.rotate(); ok {
			enc = newEnc
		}
	}

	n, werr := l.file.Write(data)
	if werr == nil {
		l.curSize += int64(n)
	}
	return enc
}

// rotate renames the active audit file to a timestamped sibling and reopens a
// fresh active file. Returns the encoder for the new file. On any error the
// existing file/encoder are kept (fail-soft: never lose the ability to log).
func (l *Logger) rotate() (*json.Encoder, bool) {
	if l.file == nil || l.outputPath == "" {
		return nil, false
	}
	if err := l.file.Close(); err != nil {
		// Reopen append handle so logging continues even if Close failed.
		if f, oerr := os.OpenFile(l.outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); oerr == nil {
			l.file = f
			l.writer = f
		}
		return json.NewEncoder(l.writer), false
	}

	ts := time.Now().UTC().Format("20060102T150405.000000000Z")
	rotated := l.outputPath + "." + ts
	if err := os.Rename(l.outputPath, rotated); err != nil {
		// Rename failed — reopen the original and keep appending.
		f, oerr := os.OpenFile(l.outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if oerr != nil {
			return json.NewEncoder(l.writer), false
		}
		l.file = f
		l.writer = f
		return json.NewEncoder(l.writer), false
	}

	f, err := os.OpenFile(l.outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return json.NewEncoder(l.writer), false
	}
	l.file = f
	l.writer = f
	l.curSize = 0
	l.Rotations.Add(1)
	return json.NewEncoder(l.writer), true
}

// cleanupRetention deletes rotated audit siblings older than retentionDays.
// No-op for stderr mode or when retention is disabled (0). Rotated files are
// named "<outputPath>.<timestamp>"; the active file itself is never deleted.
func (l *Logger) cleanupRetention() {
	if l.file == nil || l.outputPath == "" || l.retentionDays <= 0 {
		return
	}
	dir := filepath.Dir(l.outputPath)
	base := filepath.Base(l.outputPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(l.retentionDays) * 24 * time.Hour)
	prefix := base + "."
	for _, e := range entries {
		name := e.Name()
		// Only consider rotated siblings; never the active file or the swap.
		if name == base || !strings.HasPrefix(name, prefix) {
			continue
		}
		if strings.HasPrefix(name, ".") { // skip hidden helpers like .audit-swap
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// replaySwap drains the spill file through writeEvent so size tracking and
// rotation apply uniformly to replayed events. It returns the (possibly
// rebound) encoder for the active writer; callers must use the return value
// since a rotation during replay swaps the underlying file.
func (l *Logger) replaySwap(enc *json.Encoder) *json.Encoder {
	l.swapMu.Lock()
	if l.swapFile == nil || l.swapSize == 0 {
		l.swapMu.Unlock()
		return enc
	}
	// Close the write handle and atomically rename so concurrent spillToSwap
	// creates a fresh file rather than appending to the one we're about to read.
	_ = l.swapFile.Close()
	l.swapFile = nil
	l.swapSize = 0
	replayPath := l.swapPath + ".replay"
	if err := os.Rename(l.swapPath, replayPath); err != nil {
		l.swapMu.Unlock()
		return enc
	}
	l.swapMu.Unlock()

	// #nosec G304 -- internal path (hash/fixed name under our own dir), not user input
	f, err := os.Open(replayPath)
	if err != nil {
		return enc
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	for scanner.Scan() {
		var event AuditEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			beforeFile := l.file
			l.writeEvent(enc, event)
			// writeEvent may have rotated; refresh the encoder to the current
			// writer so subsequent encodes target the right file.
			if l.file != beforeFile {
				enc = json.NewEncoder(l.writer)
			}
		}
	}
	_ = f.Close()
	_ = os.Remove(replayPath)
	return enc
}

// Noop is an Auditor implementation that does nothing.
// Used when auditing is disabled.
type Noop struct{}

// NewNoop returns a no-op auditor.
func NewNoop() *Noop {
	return &Noop{}
}

func (n *Noop) Log(_ AuditEvent) {}

// IncludeRequestBody always reports false for the no-op auditor.
func (n *Noop) IncludeRequestBody() bool { return false }

func (n *Noop) Close() {}

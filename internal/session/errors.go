package session

import "errors"

// Sentinel errors for session lookup failures. Callers use errors.Is /
// errors.As to distinguish them and surface a typed, recoverable response
// rather than a bare string — important in multi-pod HTTP deployments where a
// client may reconnect to a pod that does not hold its (in-memory) session.
var (
	// ErrSessionNotFound indicates no session exists for the given
	// tenant/session key (never created on this instance, evicted, or the
	// client reconnected to a different pod).
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionExpired indicates the session existed but exceeded its TTL.
	ErrSessionExpired = errors.New("session expired")

	// ErrSessionCorrupt indicates the index referenced a session whose stored
	// payload could not be loaded/decoded.
	ErrSessionCorrupt = errors.New("session data corrupt")
)

// SessionNotFoundError is a typed error carrying enough context for a client
// to decide whether to resume or restart. It wraps ErrSessionNotFound so
// errors.Is(err, ErrSessionNotFound) holds.
type SessionNotFoundError struct {
	TenantID  string
	SessionID string
	// LastKnownStep is the step number the caller was attempting, minus one —
	// i.e. the last step the caller believed it had completed. It lets the
	// client offer "resume from step N" without the server retaining the lost
	// session's data.
	LastKnownStep int
}

func (e *SessionNotFoundError) Error() string { return "session not found" }

func (e *SessionNotFoundError) Unwrap() error { return ErrSessionNotFound }

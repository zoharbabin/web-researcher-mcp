package session

// Manager is the session-store contract. Callers depend on this interface, not
// a concrete type, so an alternative backend (e.g. a Redis-backed manager for
// multi-pod HTTP deployments, #42) can be swapped in at construction time in
// main.go with no caller changes. The method set is the exact surface the
// in-memory implementation already exposed.
type Manager interface {
	// Create starts a new session for the tenant and returns its index.
	Create(tenantID string) (*SessionIndex, error)
	// AppendStep records a research step. Returns a typed *SessionNotFoundError
	// (wrapping ErrSessionNotFound) when the session is absent, ErrSessionExpired
	// when past TTL.
	AppendStep(tenantID, sessionID string, step ResearchStep, gap *KnowledgeGap, summary string) (*SessionIndex, error)
	// SetResearchGoal sets the goal on an existing session.
	SetResearchGoal(tenantID, sessionID, goal string) error
	// AddSources appends de-duplicated sources to a session.
	AddSources(tenantID, sessionID string, sources []ResearchSource) error
	// GetIndex returns the lightweight index for a session, or ok=false.
	GetIndex(tenantID, sessionID string) (*SessionIndex, bool)
	// GetFull loads the full session payload.
	GetFull(tenantID, sessionID string) (*Session, error)
	// GetStep returns a single step by number.
	GetStep(tenantID, sessionID string, stepNum int) (*ResearchStep, error)
	// Delete removes a single session.
	Delete(tenantID, sessionID string)
	// DeleteAll removes every session (admin flush).
	DeleteAll()
	// Close stops background goroutines and releases resources.
	Close()
	// ActiveCount returns the number of live sessions (for stats).
	ActiveCount() int
}

// Compile-time assertion that the in-memory implementation satisfies Manager.
var _ Manager = (*MemoryManager)(nil)

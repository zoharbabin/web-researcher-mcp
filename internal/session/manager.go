package session

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type Config struct {
	MaxSessions int
	SessionTTL  time.Duration
	RedisURL    string
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	config   Config
	done     chan struct{}
}

type Session struct {
	ID        string
	TenantID  string
	CreatedAt time.Time
	LastUsed  time.Time
	Steps     []ResearchStep
	Sources   []ResearchSource
	Gaps      []KnowledgeGap
}

type ResearchStep struct {
	StepNumber  int    `json:"stepNumber"`
	Description string `json:"description"`
	IsRevision  bool   `json:"isRevision,omitempty"`
	RevisesStep int    `json:"revisesStep,omitempty"`
	BranchID    string `json:"branchId,omitempty"`
	Timestamp   string `json:"timestamp"`
}

type ResearchSource struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Relevance   string `json:"relevance,omitempty"`
	FoundInStep int    `json:"foundInStep"`
}

type KnowledgeGap struct {
	Description string `json:"description"`
	FoundInStep int    `json:"foundInStep"`
}

func NewManager(cfg Config) *Manager {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 50
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 30 * time.Minute
	}

	m := &Manager{
		sessions: make(map[string]*Session),
		config:   cfg,
		done:     make(chan struct{}),
	}

	go m.cleanup()
	return m
}

func (m *Manager) Create(tenantID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Count tenant sessions
	count := 0
	for _, s := range m.sessions {
		if s.TenantID == tenantID {
			count++
		}
	}
	if count >= m.config.MaxSessions {
		m.evictOldest(tenantID)
	}

	sess := &Session{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
	}

	key := tenantID + ":" + sess.ID
	m.sessions[key] = sess
	return sess, nil
}

func (m *Manager) Get(tenantID, sessionID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := tenantID + ":" + sessionID
	sess, ok := m.sessions[key]
	if !ok {
		return nil, false
	}
	if time.Since(sess.LastUsed) > m.config.SessionTTL {
		return nil, false
	}
	return sess, true
}

func (m *Manager) Update(tenantID string, sess *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess.LastUsed = time.Now()
	key := tenantID + ":" + sess.ID
	m.sessions[key] = sess
}

func (m *Manager) Delete(tenantID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tenantID + ":" + sessionID
	delete(m.sessions, key)
}

func (m *Manager) DeleteAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = make(map[string]*Session)
}

func (m *Manager) Close() {
	close(m.done)
}

func (m *Manager) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			for key, sess := range m.sessions {
				if now.Sub(sess.LastUsed) > m.config.SessionTTL {
					delete(m.sessions, key)
				}
			}
			m.mu.Unlock()
		case <-m.done:
			return
		}
	}
}

func (m *Manager) evictOldest(tenantID string) {
	var oldestKey string
	var oldestTime time.Time

	for key, sess := range m.sessions {
		if sess.TenantID != tenantID {
			continue
		}
		if oldestKey == "" || sess.LastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = sess.LastUsed
		}
	}
	if oldestKey != "" {
		delete(m.sessions, oldestKey)
	}
}

func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

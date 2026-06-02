package session

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Config struct {
	MaxSessions        int
	MaxStepsPerSession int
	SessionTTL         time.Duration
	DataDir            string
	EncryptionKey      string
	EncryptionKeyPrev  string
	RedisURL           string
}

type MemoryManager struct {
	mu     sync.Mutex
	index  map[string]*SessionIndex
	keys   map[string]string // fileHash → compound key (for rebuild)
	store  *Store
	config Config
	done   chan struct{}
}

func NewManager(cfg Config) (*MemoryManager, error) {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 50
	}
	if cfg.MaxStepsPerSession <= 0 {
		cfg.MaxStepsPerSession = 200
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 4 * time.Hour
	}

	store, err := NewStoreWithPrev(cfg.DataDir, cfg.EncryptionKey, cfg.EncryptionKeyPrev)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}

	m := &MemoryManager{
		index:  make(map[string]*SessionIndex),
		keys:   make(map[string]string),
		store:  store,
		config: cfg,
		done:   make(chan struct{}),
	}

	_ = store.CleanOrphans()
	m.rebuildIndex()
	go m.cleanup()
	return m, nil
}

// sessionKey namespaces a session by (tenant, user, id) so a session is private
// to the user that created it. userID is normalized to "anonymous" when empty
// (STDIO / unauthenticated HTTP), keeping single-user behavior intact.
func sessionKey(tenantID, userID, sessionID string) string {
	if userID == "" {
		userID = "anonymous"
	}
	return tenantID + ":" + userID + ":" + sessionID
}

func (m *MemoryManager) Create(tenantID, userID string) (*SessionIndex, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if userID == "" {
		userID = "anonymous"
	}

	// Per-(tenant,user) session cap: count and evict within the owner's own
	// sessions so one user cannot evict another's.
	count := 0
	for _, idx := range m.index {
		if idx.TenantID == tenantID && idx.CreatedByUserID == userID {
			count++
		}
	}
	if count >= m.config.MaxSessions {
		m.evictOldest(tenantID, userID)
	}

	sess := &Session{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		CreatedByUserID: userID,
		CreatedAt:       time.Now(),
		LastUsed:        time.Now(),
	}

	key := sessionKey(tenantID, userID, sess.ID)
	if err := m.store.Save(key, sess, m.config.SessionTTL); err != nil {
		return nil, err
	}

	idx := buildIndexFromSession(sess)
	m.index[key] = idx
	m.keys[fileHash(key)] = key
	return idx, nil
}

func (m *MemoryManager) AppendStep(tenantID, userID, sessionID string, step ResearchStep, gap *KnowledgeGap, summary string) (*SessionIndex, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sessionKey(tenantID, userID, sessionID)
	idx, ok := m.index[key]
	if !ok {
		return nil, &SessionNotFoundError{TenantID: tenantID, SessionID: sessionID, LastKnownStep: step.StepNumber - 1}
	}
	if time.Since(idx.LastUsed) > m.config.SessionTTL {
		m.deleteUnlocked(key)
		return nil, ErrSessionExpired
	}

	if idx.StepCount >= m.config.MaxStepsPerSession {
		idx.Warning = "session_limit_reached"
		return idx, nil
	}

	sess, err := m.store.Load(key)
	if err != nil {
		m.deleteUnlocked(key)
		return nil, ErrSessionCorrupt
	}

	sess.Steps = append(sess.Steps, step)
	sess.LastUsed = time.Now()

	if gap != nil {
		sess.Gaps = append(sess.Gaps, *gap)
	}

	if err := m.store.Save(key, sess, m.config.SessionTTL); err != nil {
		return nil, err
	}

	idx = buildIndexFromSession(sess)
	if summary != "" {
		idx.Summary = summary
	}
	m.index[key] = idx
	return idx, nil
}

func (m *MemoryManager) SetResearchGoal(tenantID, userID, sessionID, goal string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sessionKey(tenantID, userID, sessionID)
	idx, ok := m.index[key]
	if !ok {
		return ErrSessionNotFound
	}

	sess, err := m.store.Load(key)
	if err != nil {
		return err
	}

	sess.ResearchGoal = goal
	sess.LastUsed = time.Now()
	if err := m.store.Save(key, sess, m.config.SessionTTL); err != nil {
		return err
	}

	idx.ResearchGoal = goal
	idx.LastUsed = sess.LastUsed
	return nil
}

func (m *MemoryManager) AddSources(tenantID, userID, sessionID string, sources []ResearchSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sessionKey(tenantID, userID, sessionID)
	_, ok := m.index[key]
	if !ok {
		return ErrSessionNotFound
	}

	sess, err := m.store.Load(key)
	if err != nil {
		return err
	}

	existing := make(map[string]struct{}, len(sess.Sources))
	for _, s := range sess.Sources {
		existing[s.URL] = struct{}{}
	}
	for _, s := range sources {
		if _, dup := existing[s.URL]; !dup {
			sess.Sources = append(sess.Sources, s)
			existing[s.URL] = struct{}{}
		}
	}

	sess.LastUsed = time.Now()
	if err := m.store.Save(key, sess, m.config.SessionTTL); err != nil {
		return err
	}

	idx := buildIndexFromSession(sess)
	m.index[key] = idx
	return nil
}

func (m *MemoryManager) GetIndex(tenantID, userID, sessionID string) (*SessionIndex, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sessionKey(tenantID, userID, sessionID)
	idx, ok := m.index[key]
	if !ok {
		return nil, false
	}
	if time.Since(idx.LastUsed) > m.config.SessionTTL {
		m.deleteUnlocked(key)
		return nil, false
	}
	idx.LastUsed = time.Now()
	return idx, true
}

func (m *MemoryManager) GetFull(tenantID, userID, sessionID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sessionKey(tenantID, userID, sessionID)
	idx, ok := m.index[key]
	if !ok {
		return nil, &SessionNotFoundError{TenantID: tenantID, SessionID: sessionID}
	}
	if time.Since(idx.LastUsed) > m.config.SessionTTL {
		m.deleteUnlocked(key)
		return nil, ErrSessionExpired
	}

	idx.LastUsed = time.Now()

	sess, err := m.store.Load(key)
	if err != nil {
		m.deleteUnlocked(key)
		return nil, ErrSessionCorrupt
	}
	return sess, nil
}

func (m *MemoryManager) GetStep(tenantID, userID, sessionID string, stepNum int) (*ResearchStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sessionKey(tenantID, userID, sessionID)
	idx, ok := m.index[key]
	if !ok {
		return nil, &SessionNotFoundError{TenantID: tenantID, SessionID: sessionID}
	}
	if time.Since(idx.LastUsed) > m.config.SessionTTL {
		m.deleteUnlocked(key)
		return nil, ErrSessionExpired
	}

	idx.LastUsed = time.Now()

	sess, err := m.store.Load(key)
	if err != nil {
		m.deleteUnlocked(key)
		return nil, ErrSessionCorrupt
	}

	for i := range sess.Steps {
		if sess.Steps[i].StepNumber == stepNum {
			return &sess.Steps[i], nil
		}
	}
	return nil, fmt.Errorf("step %d not found", stepNum)
}

func (m *MemoryManager) Delete(tenantID, userID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteUnlocked(sessionKey(tenantID, userID, sessionID))
}

func (m *MemoryManager) DeleteAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.index {
		_ = m.store.Delete(key)
	}
	m.index = make(map[string]*SessionIndex)
	m.keys = make(map[string]string)
}

// ListByTenant returns a copy of the index entries for one tenant. Used by the
// data-subject access/portability export (#85). Sessions carry no per-user
// field, so this is tenant-scoped (the finest granularity the session data
// supports); the caller documents that scope to the subject.
func (m *MemoryManager) ListByTenant(tenantID string) []*SessionIndex {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*SessionIndex
	for _, idx := range m.index {
		if idx.TenantID == tenantID {
			cp := *idx
			out = append(out, &cp)
		}
	}
	return out
}

// DeleteByTenant purges every session for a tenant from memory and disk,
// returning the count removed. Used by the data-subject erasure path (#85).
func (m *MemoryManager) DeleteByTenant(tenantID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for key, idx := range m.index {
		if idx.TenantID == tenantID {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		m.deleteUnlocked(key)
	}
	return len(keys)
}

func (m *MemoryManager) Close() {
	close(m.done)
}

func (m *MemoryManager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.index)
}

func (m *MemoryManager) deleteUnlocked(key string) {
	delete(m.index, key)
	delete(m.keys, fileHash(key))
	_ = m.store.Delete(key)
}

// evictOldest removes the least-recently-used session belonging to the given
// (tenant, user) — never another principal's session.
func (m *MemoryManager) evictOldest(tenantID, userID string) {
	var oldestKey string
	var oldestTime time.Time

	for key, idx := range m.index {
		if idx.TenantID != tenantID || idx.CreatedByUserID != userID {
			continue
		}
		if oldestKey == "" || idx.LastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = idx.LastUsed
		}
	}
	if oldestKey != "" {
		m.deleteUnlocked(oldestKey)
	}
}

func (m *MemoryManager) cleanup() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			for key, idx := range m.index {
				if now.Sub(idx.LastUsed) > m.config.SessionTTL {
					m.deleteUnlocked(key)
				}
			}
			m.mu.Unlock()
		case <-m.done:
			return
		}
	}
}

func (m *MemoryManager) rebuildIndex() {
	hashes, err := m.store.ListValid(time.Now())
	if err != nil {
		slog.Warn("failed to list session files", "err", err)
		return
	}

	for _, hash := range hashes {
		// We need to try loading each file. Since ListValid returns filename hashes
		// and we can't reverse SHA-256, we read directly using the file path.
		// The AAD is derived from the on-disk filename hash so it matches the AAD
		// the Save/Load paths derive via aadForKey(key) (M7).
		fp := filepath.Join(m.store.dir, hash+".session")
		sess, plaintext, expiry, usedPrev, err := m.store.loadFile(fp, hash)
		if err != nil {
			slog.Warn("corrupt session file during rebuild, removing", "hash", hash, "err", err)
			_ = os.Remove(fp)
			continue
		}

		key := sessionKey(sess.TenantID, sess.CreatedByUserID, sess.ID)
		if fileHash(key) != hash {
			slog.Warn("session file hash mismatch, removing", "hash", hash)
			_ = os.Remove(fp)
			continue
		}

		if usedPrev {
			// Blob was decrypted under the previous key during rotation: lazily
			// re-encrypt with the current key so it is upgraded on rebuild (M1).
			if err := m.store.rewrite(key, plaintext, expiry); err != nil {
				slog.Warn("failed to re-encrypt rotated session on rebuild", "hash", hash, "err", err)
			}
		}

		idx := buildIndexFromSession(sess)
		m.index[key] = idx
		m.keys[hash] = key
	}

	if len(m.index) > 0 {
		slog.Info("sessions rebuilt from disk", "count", len(m.index))
	}
}

func buildIndexFromSession(sess *Session) *SessionIndex {
	idx := &SessionIndex{
		ID:              sess.ID,
		TenantID:        sess.TenantID,
		CreatedByUserID: sess.CreatedByUserID,
		ResearchGoal:    sess.ResearchGoal,
		CreatedAt:       sess.CreatedAt,
		LastUsed:        sess.LastUsed,
		StepCount:       len(sess.Steps),
		ActiveGaps:      sess.Gaps,
		Sources:         sess.Sources,
	}

	for _, step := range sess.Steps {
		oneLiner := step.Description
		if len(oneLiner) > 120 {
			oneLiner = oneLiner[:120]
		}
		idx.StepIndex = append(idx.StepIndex, StepIndexEntry{
			StepNumber: step.StepNumber,
			BranchID:   step.BranchID,
			OneLiner:   oneLiner,
			Confidence: step.Confidence,
		})
	}

	// Keep last 3 steps
	if len(sess.Steps) > 3 {
		idx.LastSteps = sess.Steps[len(sess.Steps)-3:]
	} else {
		idx.LastSteps = sess.Steps
	}

	// Auto-generate summary if not externally provided
	if sess.ResearchGoal != "" && len(sess.Steps) > 0 {
		parts := []string{}
		start := len(sess.Steps) - 5
		if start < 0 {
			start = 0
		}
		for _, s := range sess.Steps[start:] {
			ol := s.Description
			if len(ol) > 80 {
				ol = ol[:80]
			}
			parts = append(parts, ol)
		}
		idx.Summary = sess.ResearchGoal + ". Progress: " + strings.Join(parts, "; ") + "."
	}

	return idx
}

package session

import (
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
	RedisURL           string
}

type Manager struct {
	mu     sync.Mutex
	index  map[string]*SessionIndex
	keys   map[string]string // fileHash → compound key (for rebuild)
	store  *Store
	config Config
	done   chan struct{}
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 50
	}
	if cfg.MaxStepsPerSession <= 0 {
		cfg.MaxStepsPerSession = 200
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 4 * time.Hour
	}

	store, err := NewStore(cfg.DataDir, cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}

	m := &Manager{
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

func (m *Manager) Create(tenantID string) (*SessionIndex, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, idx := range m.index {
		if idx.TenantID == tenantID {
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
	if err := m.store.Save(key, sess, m.config.SessionTTL); err != nil {
		return nil, err
	}

	idx := buildIndexFromSession(sess)
	m.index[key] = idx
	m.keys[fileHash(key)] = key
	return idx, nil
}

func (m *Manager) AppendStep(tenantID, sessionID string, step ResearchStep, gap *KnowledgeGap, summary string) (*SessionIndex, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tenantID + ":" + sessionID
	idx, ok := m.index[key]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	if time.Since(idx.LastUsed) > m.config.SessionTTL {
		m.deleteUnlocked(key)
		return nil, fmt.Errorf("session expired")
	}

	if idx.StepCount >= m.config.MaxStepsPerSession {
		idx.Warning = "session_limit_reached"
		return idx, nil
	}

	sess, err := m.store.Load(key)
	if err != nil {
		m.deleteUnlocked(key)
		return nil, fmt.Errorf("session data corrupt")
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

func (m *Manager) SetResearchGoal(tenantID, sessionID, goal string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tenantID + ":" + sessionID
	idx, ok := m.index[key]
	if !ok {
		return fmt.Errorf("session not found")
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

func (m *Manager) AddSources(tenantID, sessionID string, sources []ResearchSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tenantID + ":" + sessionID
	_, ok := m.index[key]
	if !ok {
		return fmt.Errorf("session not found")
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

func (m *Manager) GetIndex(tenantID, sessionID string) (*SessionIndex, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tenantID + ":" + sessionID
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

func (m *Manager) GetFull(tenantID, sessionID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tenantID + ":" + sessionID
	idx, ok := m.index[key]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	if time.Since(idx.LastUsed) > m.config.SessionTTL {
		m.deleteUnlocked(key)
		return nil, fmt.Errorf("session expired")
	}

	idx.LastUsed = time.Now()

	sess, err := m.store.Load(key)
	if err != nil {
		m.deleteUnlocked(key)
		return nil, fmt.Errorf("session data corrupt")
	}
	return sess, nil
}

func (m *Manager) GetStep(tenantID, sessionID string, stepNum int) (*ResearchStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tenantID + ":" + sessionID
	idx, ok := m.index[key]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	if time.Since(idx.LastUsed) > m.config.SessionTTL {
		m.deleteUnlocked(key)
		return nil, fmt.Errorf("session expired")
	}

	idx.LastUsed = time.Now()

	sess, err := m.store.Load(key)
	if err != nil {
		m.deleteUnlocked(key)
		return nil, fmt.Errorf("session data corrupt")
	}

	for i := range sess.Steps {
		if sess.Steps[i].StepNumber == stepNum {
			return &sess.Steps[i], nil
		}
	}
	return nil, fmt.Errorf("step %d not found", stepNum)
}

func (m *Manager) Delete(tenantID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteUnlocked(tenantID + ":" + sessionID)
}

func (m *Manager) DeleteAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.index {
		_ = m.store.Delete(key)
	}
	m.index = make(map[string]*SessionIndex)
	m.keys = make(map[string]string)
}

func (m *Manager) Close() {
	close(m.done)
}

func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.index)
}

func (m *Manager) deleteUnlocked(key string) {
	delete(m.index, key)
	delete(m.keys, fileHash(key))
	_ = m.store.Delete(key)
}

func (m *Manager) evictOldest(tenantID string) {
	var oldestKey string
	var oldestTime time.Time

	for key, idx := range m.index {
		if idx.TenantID != tenantID {
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

func (m *Manager) cleanup() {
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

func (m *Manager) rebuildIndex() {
	hashes, err := m.store.ListValid(time.Now())
	if err != nil {
		slog.Warn("failed to list session files", "err", err)
		return
	}

	for _, hash := range hashes {
		// We need to try loading each file. Since ListValid returns filename hashes
		// and we can't reverse SHA-256, we read directly using the file path.
		fp := m.store.dir + "/" + hash + ".session"
		data, err := readSessionFile(fp, m.store.gcm)
		if err != nil {
			slog.Warn("corrupt session file during rebuild, removing", "hash", hash, "err", err)
			os.Remove(fp)
			continue
		}

		key := data.TenantID + ":" + data.ID
		if fileHash(key) != hash {
			slog.Warn("session file hash mismatch, removing", "hash", hash)
			os.Remove(fp)
			continue
		}

		idx := buildIndexFromSession(data)
		m.index[key] = idx
		m.keys[hash] = key
	}

	if len(m.index) > 0 {
		slog.Info("sessions rebuilt from disk", "count", len(m.index))
	}
}

func readSessionFile(fp string, gcm cipher.AEAD) (*Session, error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, ErrCorrupt
	}

	payload := data[8:]
	if gcm != nil {
		nonceSize := gcm.NonceSize()
		if len(payload) < nonceSize {
			return nil, ErrCorrupt
		}
		nonce, ct := payload[:nonceSize], payload[nonceSize:]
		decrypted, err := gcm.Open(nil, nonce, ct, nil)
		if err != nil {
			return nil, ErrCorrupt
		}
		payload = decrypted
	}

	var sess Session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return nil, ErrCorrupt
	}
	return &sess, nil
}

func buildIndexFromSession(sess *Session) *SessionIndex {
	idx := &SessionIndex{
		ID:           sess.ID,
		TenantID:     sess.TenantID,
		ResearchGoal: sess.ResearchGoal,
		CreatedAt:    sess.CreatedAt,
		LastUsed:     sess.LastUsed,
		StepCount:    len(sess.Steps),
		ActiveGaps:   sess.Gaps,
		Sources:      sess.Sources,
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

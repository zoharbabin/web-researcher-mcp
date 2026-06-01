package redisbackend

import (
	"context"
	"crypto/cipher"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// SessionManager is a Redis-backed session.Manager: sessions survive pod
// restarts and are visible to every pod (so a client reconnecting to a
// different pod still finds its research). Each session is stored as one
// AES-256-GCM-encrypted JSON blob with a server-side EXPIRE matching the TTL,
// plus a per-tenant index set for listing/eviction. Satisfies the same
// session.Manager interface as the in-memory manager — callers are identical.
type SessionManager struct {
	b       *Backends
	gcm     cipher.AEAD
	gcmPrev cipher.AEAD
	ttl     time.Duration
	maxPer  int
	ctx     context.Context
}

// SessionManager builds a Redis-backed session manager.
func (b *Backends) SessionManager() *SessionManager {
	ttl := b.cfg.SessionTTL
	if ttl <= 0 {
		ttl = 4 * time.Hour
	}
	return &SessionManager{b: b, gcm: b.gcm, gcmPrev: b.gcmPrev, ttl: ttl, maxPer: b.cfg.MaxSessionsPerTenant, ctx: context.Background()}
}

func (m *SessionManager) blobKey(tenantID, sessionID string) string {
	return m.b.key("session", tenantID+":"+sessionID)
}

func (m *SessionManager) tenantSetKey(tenantID string) string {
	return m.b.key("session:index", tenantID)
}

func (m *SessionManager) save(sess *session.Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	ct := cache.GCMEncrypt(m.gcm, data, []byte(sess.ID))
	pipe := m.b.client.TxPipeline()
	bk := m.blobKey(sess.TenantID, sess.ID)
	pipe.Set(m.ctx, bk, ct, m.ttl)
	pipe.SAdd(m.ctx, m.tenantSetKey(sess.TenantID), sess.ID)
	pipe.Expire(m.ctx, m.tenantSetKey(sess.TenantID), m.ttl)
	_, err = pipe.Exec(m.ctx)
	return err
}

func (m *SessionManager) load(tenantID, sessionID string) (*session.Session, error) {
	data, err := m.b.client.Get(m.ctx, m.blobKey(tenantID, sessionID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, &session.SessionNotFoundError{TenantID: tenantID, SessionID: sessionID}
		}
		return nil, err
	}
	aad := []byte(sessionID)
	pt, derr := cache.GCMDecrypt(m.gcm, data, aad)
	if derr != nil && m.gcmPrev != nil {
		pt, derr = cache.GCMDecrypt(m.gcmPrev, data, aad)
	}
	if derr != nil {
		return nil, session.ErrSessionCorrupt
	}
	var sess session.Session
	if err := json.Unmarshal(pt, &sess); err != nil {
		return nil, session.ErrSessionCorrupt
	}
	return &sess, nil
}

func (m *SessionManager) Create(tenantID string) (*session.SessionIndex, error) {
	if m.maxPer > 0 {
		if n, err := m.b.client.SCard(m.ctx, m.tenantSetKey(tenantID)).Result(); err == nil && int(n) >= m.maxPer {
			m.evictOldest(tenantID)
		}
	}
	sess := &session.Session{
		ID:        newID(),
		TenantID:  tenantID,
		CreatedAt: nowUTC(),
		LastUsed:  nowUTC(),
	}
	if err := m.save(sess); err != nil {
		return nil, err
	}
	return session.BuildIndex(sess), nil
}

func (m *SessionManager) AppendStep(tenantID, sessionID string, step session.ResearchStep, gap *session.KnowledgeGap, summary string) (*session.SessionIndex, error) {
	sess, err := m.load(tenantID, sessionID)
	if err != nil {
		if _, ok := err.(*session.SessionNotFoundError); ok {
			return nil, &session.SessionNotFoundError{TenantID: tenantID, SessionID: sessionID, LastKnownStep: step.StepNumber - 1}
		}
		return nil, err
	}
	if len(sess.Steps) >= session.DefaultMaxSteps {
		idx := session.BuildIndex(sess)
		idx.Warning = "session_limit_reached"
		return idx, nil
	}
	sess.Steps = append(sess.Steps, step)
	sess.LastUsed = nowUTC()
	if gap != nil {
		sess.Gaps = append(sess.Gaps, *gap)
	}
	if err := m.save(sess); err != nil {
		return nil, err
	}
	idx := session.BuildIndex(sess)
	if summary != "" {
		idx.Summary = summary
	}
	return idx, nil
}

func (m *SessionManager) SetResearchGoal(tenantID, sessionID, goal string) error {
	sess, err := m.load(tenantID, sessionID)
	if err != nil {
		return err
	}
	sess.ResearchGoal = goal
	sess.LastUsed = nowUTC()
	return m.save(sess)
}

func (m *SessionManager) AddSources(tenantID, sessionID string, sources []session.ResearchSource) error {
	sess, err := m.load(tenantID, sessionID)
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
	sess.LastUsed = nowUTC()
	return m.save(sess)
}

func (m *SessionManager) GetIndex(tenantID, sessionID string) (*session.SessionIndex, bool) {
	sess, err := m.load(tenantID, sessionID)
	if err != nil {
		return nil, false
	}
	return session.BuildIndex(sess), true
}

func (m *SessionManager) GetFull(tenantID, sessionID string) (*session.Session, error) {
	return m.load(tenantID, sessionID)
}

func (m *SessionManager) GetStep(tenantID, sessionID string, stepNum int) (*session.ResearchStep, error) {
	sess, err := m.load(tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	for i := range sess.Steps {
		if sess.Steps[i].StepNumber == stepNum {
			return &sess.Steps[i], nil
		}
	}
	return nil, session.ErrSessionNotFound
}

func (m *SessionManager) Delete(tenantID, sessionID string) {
	pipe := m.b.client.TxPipeline()
	pipe.Del(m.ctx, m.blobKey(tenantID, sessionID))
	pipe.SRem(m.ctx, m.tenantSetKey(tenantID), sessionID)
	_, _ = pipe.Exec(m.ctx)
}

func (m *SessionManager) DeleteAll() {
	// Scan the namespace and delete in batches. Admin flush is rare; SCAN avoids
	// blocking Redis with KEYS.
	iter := m.b.client.Scan(m.ctx, 0, m.b.key("session", "*"), 100).Iterator()
	for iter.Next(m.ctx) {
		_ = m.b.client.Del(m.ctx, iter.Val()).Err()
	}
	idxIter := m.b.client.Scan(m.ctx, 0, m.b.key("session:index", "*"), 100).Iterator()
	for idxIter.Next(m.ctx) {
		_ = m.b.client.Del(m.ctx, idxIter.Val()).Err()
	}
}

func (m *SessionManager) ListByTenant(tenantID string) []*session.SessionIndex {
	ids, err := m.b.client.SMembers(m.ctx, m.tenantSetKey(tenantID)).Result()
	if err != nil {
		return nil
	}
	var out []*session.SessionIndex
	for _, id := range ids {
		if sess, err := m.load(tenantID, id); err == nil {
			out = append(out, session.BuildIndex(sess))
		}
	}
	return out
}

func (m *SessionManager) DeleteByTenant(tenantID string) int {
	ids, err := m.b.client.SMembers(m.ctx, m.tenantSetKey(tenantID)).Result()
	if err != nil {
		return 0
	}
	for _, id := range ids {
		m.Delete(tenantID, id)
	}
	_ = m.b.client.Del(m.ctx, m.tenantSetKey(tenantID)).Err()
	return len(ids)
}

func (m *SessionManager) ActiveCount() int {
	var total int
	iter := m.b.client.Scan(m.ctx, 0, m.b.key("session:index", "*"), 100).Iterator()
	for iter.Next(m.ctx) {
		if n, err := m.b.client.SCard(m.ctx, iter.Val()).Result(); err == nil {
			total += int(n)
		}
	}
	return total
}

func (m *SessionManager) Close() {} // client lifecycle owned by Backends.Close

// evictOldest removes the least-recently-used session for a tenant when the cap
// is hit, mirroring the in-memory manager's eviction.
func (m *SessionManager) evictOldest(tenantID string) {
	idxs := m.ListByTenant(tenantID)
	if len(idxs) == 0 {
		return
	}
	oldest := idxs[0]
	for _, idx := range idxs[1:] {
		if idx.LastUsed.Before(oldest.LastUsed) {
			oldest = idx
		}
	}
	m.Delete(tenantID, oldest.ID)
}

var _ session.Manager = (*SessionManager)(nil)

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

// normUser normalizes an empty userID to "anonymous" so STDIO / unauthenticated
// callers map to a single stable owner (parity with the in-memory manager).
func normUser(userID string) string {
	if userID == "" {
		return "anonymous"
	}
	return userID
}

// blobKey namespaces the session blob by (tenant, user, id) so a session is
// private to its owning user — a co-tenant user cannot read another's session.
func (m *SessionManager) blobKey(tenantID, userID, sessionID string) string {
	return m.b.key("session", tenantID+":"+normUser(userID)+":"+sessionID)
}

// tenantSetKey indexes every session in a tenant (across users) so the
// data-subject erasure (DeleteByTenant) still covers the whole tenant. Members
// are "userID:sessionID" so per-session keys can be reconstructed.
func (m *SessionManager) tenantSetKey(tenantID string) string {
	return m.b.key("session:index", tenantID)
}

func setMember(userID, sessionID string) string { return normUser(userID) + ":" + sessionID }

func (m *SessionManager) save(sess *session.Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	// AAD binds the ciphertext to user+id so a blob can't be decrypted under a
	// different owner's key path.
	ct := cache.GCMEncrypt(m.gcm, data, []byte(normUser(sess.CreatedByUserID)+":"+sess.ID))
	pipe := m.b.client.TxPipeline()
	bk := m.blobKey(sess.TenantID, sess.CreatedByUserID, sess.ID)
	pipe.Set(m.ctx, bk, ct, m.ttl)
	pipe.SAdd(m.ctx, m.tenantSetKey(sess.TenantID), setMember(sess.CreatedByUserID, sess.ID))
	pipe.Expire(m.ctx, m.tenantSetKey(sess.TenantID), m.ttl)
	_, err = pipe.Exec(m.ctx)
	return err
}

func (m *SessionManager) load(tenantID, userID, sessionID string) (*session.Session, error) {
	data, err := m.b.client.Get(m.ctx, m.blobKey(tenantID, userID, sessionID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, &session.SessionNotFoundError{TenantID: tenantID, SessionID: sessionID}
		}
		return nil, err
	}
	aad := []byte(normUser(userID) + ":" + sessionID)
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

func (m *SessionManager) Create(tenantID, userID string) (*session.SessionIndex, error) {
	if m.maxPer > 0 {
		// Per-(tenant,user) cap — parity with the in-memory manager. Count only
		// this user's own sessions (members prefixed "userID:") and evict within
		// the user, so one user can't evict another's sessions.
		if m.userSessionCount(tenantID, normUser(userID)) >= m.maxPer {
			m.evictOldestForUser(tenantID, normUser(userID))
		}
	}
	sess := &session.Session{
		ID:              newID(),
		TenantID:        tenantID,
		CreatedByUserID: normUser(userID),
		CreatedAt:       nowUTC(),
		LastUsed:        nowUTC(),
	}
	if err := m.save(sess); err != nil {
		return nil, err
	}
	return session.BuildIndex(sess), nil
}

func (m *SessionManager) AppendStep(tenantID, userID, sessionID string, step session.ResearchStep, gap *session.KnowledgeGap, summary string) (*session.SessionIndex, error) {
	sess, err := m.load(tenantID, userID, sessionID)
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

func (m *SessionManager) SetResearchGoal(tenantID, userID, sessionID, goal string) error {
	sess, err := m.load(tenantID, userID, sessionID)
	if err != nil {
		return err
	}
	sess.ResearchGoal = goal
	sess.LastUsed = nowUTC()
	return m.save(sess)
}

func (m *SessionManager) AddSources(tenantID, userID, sessionID string, sources []session.ResearchSource) error {
	sess, err := m.load(tenantID, userID, sessionID)
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

func (m *SessionManager) GetIndex(tenantID, userID, sessionID string) (*session.SessionIndex, bool) {
	sess, err := m.load(tenantID, userID, sessionID)
	if err != nil {
		return nil, false
	}
	return session.BuildIndex(sess), true
}

func (m *SessionManager) GetFull(tenantID, userID, sessionID string) (*session.Session, error) {
	return m.load(tenantID, userID, sessionID)
}

func (m *SessionManager) GetStep(tenantID, userID, sessionID string, stepNum int) (*session.ResearchStep, error) {
	sess, err := m.load(tenantID, userID, sessionID)
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

func (m *SessionManager) Delete(tenantID, userID, sessionID string) {
	pipe := m.b.client.TxPipeline()
	pipe.Del(m.ctx, m.blobKey(tenantID, userID, sessionID))
	// Also delete the pre-user-binding blob key (tenant:sessionID) so tenant
	// erasure / admin flush removes legacy blobs too, not just TTL-expires them.
	pipe.Del(m.ctx, m.b.key("session", tenantID+":"+sessionID))
	// Remove both the new member form ("userID:sessionID") and the legacy bare
	// "sessionID" member, so no stale index entry inflates SCard/eviction.
	pipe.SRem(m.ctx, m.tenantSetKey(tenantID), setMember(userID, sessionID), sessionID)
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

// splitMember parses a tenant-set member "userID:sessionID" back into its parts.
// A bare colon-less member maps to "anonymous" so DeleteByTenant can still SREM
// it from the index. NOTE: blobs written by a pre-user-binding release used a
// different key and GCM AAD and are NOT decryptable here — they simply fail to
// load and expire via their TTL (≤4h). Session continuity across that one
// upgrade is intentionally not preserved (documented in the PR); there is no
// security or data-subject impact because erasure is tenant-scoped and the
// blobs self-expire.
func splitMember(member string) (userID, sessionID string) {
	if i := indexByte(member, ':'); i >= 0 {
		return member[:i], member[i+1:]
	}
	return "anonymous", member
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func (m *SessionManager) ListByTenant(tenantID string) []*session.SessionIndex {
	members, err := m.b.client.SMembers(m.ctx, m.tenantSetKey(tenantID)).Result()
	if err != nil {
		return nil
	}
	var out []*session.SessionIndex
	for _, member := range members {
		uid, sid := splitMember(member)
		if sess, err := m.load(tenantID, uid, sid); err == nil {
			out = append(out, session.BuildIndex(sess))
		}
	}
	return out
}

func (m *SessionManager) DeleteByTenant(tenantID string) int {
	members, err := m.b.client.SMembers(m.ctx, m.tenantSetKey(tenantID)).Result()
	if err != nil {
		return 0
	}
	for _, member := range members {
		uid, sid := splitMember(member)
		m.Delete(tenantID, uid, sid)
	}
	_ = m.b.client.Del(m.ctx, m.tenantSetKey(tenantID)).Err()
	return len(members)
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

// userSessionCount counts the sessions owned by (tenantID,userID) — members of
// the tenant index whose owner matches.
func (m *SessionManager) userSessionCount(tenantID, userID string) int {
	members, err := m.b.client.SMembers(m.ctx, m.tenantSetKey(tenantID)).Result()
	if err != nil {
		return 0
	}
	n := 0
	for _, member := range members {
		if uid, _ := splitMember(member); uid == userID {
			n++
		}
	}
	return n
}

// evictOldestForUser removes the least-recently-used session belonging to the
// given (tenant,user) when their cap is hit — never another user's session.
func (m *SessionManager) evictOldestForUser(tenantID, userID string) {
	var oldest *session.SessionIndex
	for _, idx := range m.ListByTenant(tenantID) {
		if idx.CreatedByUserID != userID {
			continue
		}
		if oldest == nil || idx.LastUsed.Before(oldest.LastUsed) {
			oldest = idx
		}
	}
	if oldest != nil {
		m.Delete(tenantID, oldest.CreatedByUserID, oldest.ID)
	}
}

var _ session.Manager = (*SessionManager)(nil)

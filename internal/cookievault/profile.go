package cookievault

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

// Profile store tuning (see issue #775).
const (
	// DefaultProfileTTL is the absolute lifetime of a stored profile.
	DefaultProfileTTL = 168 * time.Hour
	// MaxProfilesPerSubject bounds disk use per user.
	MaxProfilesPerSubject = 10
	// DefaultProfileMaxTotalBytes bounds all stored archives on this node.
	DefaultProfileMaxTotalBytes int64 = 2 << 30

	profileMinGap   = 5 * time.Second
	profileLockWait = 30 * time.Second
	staleWorkDirAge = time.Hour
	// deadOwnerWorkDirAge removes a work folder whose owner process is still
	// running (or cannot be told) after this long. No lease lasts that long.
	deadOwnerWorkDirAge = 6 * time.Hour
	sweepEvery          = 10 * time.Minute
	// tombstoneAge outlives any lease; older tombstones are removed.
	tombstoneAge     = 6 * time.Hour
	tombstoneExt     = ".del"
	profileFileExt   = ".wrp"
	profileAADPrefix = "wrmcp-profile-v1|"
)

// Errors returned by ProfileStore.
var (
	ErrNoProfile        = errors.New("cookievault: no saved browser profile for this site")
	ErrProfileBusy      = errors.New("cookievault: this site's browser profile is in use")
	ErrTooManyProfiles  = errors.New("cookievault: too many saved browser profiles")
	ErrProfileStoreFull = errors.New("cookievault: the saved browser profile storage is full")
	errProfileKeyNeeded = errors.New("cookievault: profile encryption key is required")
)

// ProfileSummary is the only view of a stored profile allowed in logs, audit
// events, tool output and data-subject exports.
type ProfileSummary struct {
	Domain    string `json:"domain"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

func (m ProfileMeta) summary() ProfileSummary {
	return ProfileSummary{
		Domain:    m.Domain,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
		ExpiresAt: m.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

// keyState is the per-profile lock, spacing clock and erase generation.
type keyState struct {
	sem     chan struct{}
	refs    int
	lastEnd time.Time
	gen     int
}

// ProfileStore keeps each user's browser profile as an encrypted archive, one
// per (tenant, user, registrable domain). A profile is unpacked into a private
// work dir only while a login or a scrape uses it (ProfileLease), and is
// repacked afterwards. Archives are node-local. State is per instance.
type ProfileStore struct {
	dir     string
	workDir string
	gcm     cipher.AEAD
	ttl     time.Duration
	clock   func() time.Time
	// wait sleeps for d or until ctx ends; replaceable in tests.
	wait     func(ctx context.Context, d time.Duration) error
	minGap   time.Duration
	lockWait time.Duration
	// maxTotal caps the size of all archives in dir (every user on this node).
	maxTotal int64

	mu        sync.Mutex
	keys      map[string]*keyState
	lastSweep time.Time
}

// NewProfileStore opens (creating if needed) the archive directory dir, sealed
// with the 64-hex-character key. ttl is the absolute lifetime of an archive.
// Work dirs left behind by a crash and expired archives are removed now.
// Construction launches no browser.
func NewProfileStore(dir, hexKey string, ttl time.Duration) (*ProfileStore, error) {
	if hexKey == "" {
		return nil, errProfileKeyNeeded
	}
	gcm, err := cache.NewGCM(hexKey)
	if err != nil {
		return nil, fmt.Errorf("profile store key: %w", err)
	}
	if ttl <= 0 {
		ttl = DefaultProfileTTL
	}
	work := filepath.Join(dir, "work")
	for _, d := range []string{dir, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("profile store dir: %w", err)
		}
		if err := os.Chmod(d, 0o700); err != nil { // #nosec G302 -- directory: owner-only, needs the execute bit
			return nil, fmt.Errorf("profile store dir: %w", err)
		}
	}
	s := &ProfileStore{
		dir: dir, workDir: work, gcm: gcm, ttl: ttl,
		clock: time.Now, wait: sleepCtx, minGap: profileMinGap, lockWait: profileLockWait,
		maxTotal: DefaultProfileMaxTotalBytes, keys: map[string]*keyState{},
	}
	s.sweep()
	return s, nil
}

// WithMaxTotalBytes sets the cap on the combined size of all stored archives.
// Zero or less keeps the default. Call it before the store is shared.
func (s *ProfileStore) WithMaxTotalBytes(n int64) *ProfileStore {
	if n > 0 {
		s.maxTotal = n
	}
	return s
}

// archivesSize sums the archive sizes, skipping the one for exclude (an archive
// about to be replaced). The caller holds s.mu.
func (s *ProfileStore) archivesSize(exclude string) int64 {
	var total int64
	for _, k := range s.keysForPrefix("") {
		if k == exclude {
			continue
		}
		if info, err := os.Stat(s.path(k)); err == nil {
			total += info.Size()
		}
	}
	return total
}

// hasRoom reports whether an archive of size bytes for key fits under the cap.
// When it does not fit, expired and unreadable archives are removed first. The
// caller holds s.mu.
func (s *ProfileStore) hasRoom(key string, size int64) bool {
	if s.archivesSize(key)+size <= s.maxTotal {
		return true
	}
	for _, k := range s.keysForPrefix("") {
		if k == key {
			continue
		}
		if _, ok := s.readMeta(k); !ok {
			_ = os.Remove(s.path(k))
		}
	}
	return s.archivesSize(key)+size <= s.maxTotal
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sweep removes abandoned work dirs, old tombstones, and expired or unreadable
// archives.
func (s *ProfileStore) sweep() {
	s.sweepWork()
	now := s.clock()
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".tmp"):
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > staleWorkDirAge {
				_ = os.Remove(filepath.Join(s.dir, name))
			}
		case strings.HasSuffix(name, tombstoneExt):
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > tombstoneAge {
				_ = os.Remove(filepath.Join(s.dir, name))
			}
		case strings.HasSuffix(name, profileFileExt):
			s.dropIfDead(strings.TrimSuffix(name, profileFileExt))
		}
	}
}

// maybeSweep runs sweep at most once per sweepEvery, so a long-running server
// cleans up after crashed leases without scanning on every call.
func (s *ProfileStore) maybeSweep() {
	s.mu.Lock()
	due := s.clock().Sub(s.lastSweep) > sweepEvery
	if due {
		s.lastSweep = s.clock()
	}
	s.mu.Unlock()
	if due {
		s.sweep()
	}
}

// sweepWork removes work dirs that no live lease can own: the owner process (the
// pid in the name) is gone, or the dir is older than the age limit. Dirs of
// other running processes are left alone.
func (s *ProfileStore) sweepWork() {
	now := s.clock()
	entries, err := os.ReadDir(s.workDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		pid, named := workDirPID(e.Name())
		stale := age > staleWorkDirAge
		if named {
			stale = !pidAlive(pid) || age > deadOwnerWorkDirAge
		}
		if stale {
			removeDir(filepath.Join(s.workDir, e.Name()))
		}
	}
}

// workDirPID extracts the owner pid from a "p-<pid>-<random>" work dir name.
func workDirPID(name string) (int, bool) {
	parts := strings.SplitN(name, "-", 3)
	if len(parts) != 3 || parts[0] != "p" {
		return 0, false
	}
	pid, err := strconv.Atoi(parts[1])
	return pid, err == nil && pid > 0
}

// dropIfDead deletes the archive for key when it is expired or unreadable. It
// holds s.mu so it cannot delete an archive a Commit just renamed into place.
func (s *ProfileStore) dropIfDead(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.readMeta(key); !ok {
		_ = os.Remove(s.path(key))
	}
}

func profileKey(sub Subject, domain string) string {
	return hashPart(sub.TenantID) + "." + hashPart(sub.UserID) + "." + hashPart(domain)
}

func subjectPrefix(sub Subject) string {
	return hashPart(sub.TenantID) + "." + hashPart(sub.UserID) + "."
}

// keyPrefix returns the subject part of a profile key (through its last dot).
func keyPrefix(key string) string { return key[:strings.LastIndex(key, ".")+1] }

// A tombstone is an empty marker file written when a profile or a subject's
// profiles are deleted. A lease that began before it must not save, even when
// the deletion ran in another process that shares the directory.
func (s *ProfileStore) keyTombstone(key string) string { return filepath.Join(s.dir, key+tombstoneExt) }
func (s *ProfileStore) subjectTombstone(prefix string) string {
	return filepath.Join(s.dir, prefix+"erased"+tombstoneExt)
}

func (s *ProfileStore) writeTombstone(path string) {
	now := time.Now()
	if err := os.WriteFile(path, nil, 0o600); err == nil {
		err = os.Chtimes(path, now, now)
		if err == nil {
			return
		}
	}
	slog.Warn("could not write a deletion marker; other server processes may not see this deletion")
}

// revokedSince reports whether key (or its subject) was deleted after t.
func (s *ProfileStore) revokedSince(key string, t time.Time) bool {
	for _, p := range []string{s.keyTombstone(key), s.subjectTombstone(keyPrefix(key))} {
		if info, err := os.Stat(p); err == nil && !info.ModTime().Before(t) {
			return true
		}
	}
	return false
}

func (s *ProfileStore) path(key string) string { return filepath.Join(s.dir, key+profileFileExt) }

func profileAAD(key string) []byte { return []byte(profileAADPrefix + key) }

// readMeta returns the live meta for key; expired and unreadable read as false.
func (s *ProfileStore) readMeta(key string) (ProfileMeta, bool) {
	f, err := os.Open(s.path(key))
	if err != nil {
		return ProfileMeta{}, false
	}
	defer func() { _ = f.Close() }()
	meta, err := readProfileMeta(f, s.gcm, profileAAD(key))
	if err != nil || !meta.ExpiresAt.After(s.clock()) {
		return ProfileMeta{}, false
	}
	return meta, true
}

// resolve validates sub and host and returns the domain and storage key.
func resolve(sub Subject, host string) (domain, key string, err error) {
	if !sub.valid() {
		return "", "", ErrNoSubject
	}
	domain = RegistrableDomain(host)
	if domain == "" {
		return "", "", ErrInvalidDomain
	}
	return domain, profileKey(sub, domain), nil
}

// state returns the keyState for key, creating it, and prunes idle ones.
// The caller holds s.mu.
func (s *ProfileStore) state(key string) *keyState {
	now := s.clock()
	for k, ks := range s.keys {
		if k != key && ks.refs == 0 && now.Sub(ks.lastEnd) > s.minGap {
			delete(s.keys, k)
		}
	}
	ks := s.keys[key]
	if ks == nil {
		ks = &keyState{sem: make(chan struct{}, 1)}
		s.keys[key] = ks
	}
	return ks
}

// lock takes the profile's single-user lock and waits out the minimum gap since
// its last use. It returns ErrProfileBusy after lockWait.
func (s *ProfileStore) lock(ctx context.Context, key string) (*keyState, error) {
	s.mu.Lock()
	ks := s.state(key)
	ks.refs++
	s.mu.Unlock()

	giveUp := func() {
		s.mu.Lock()
		ks.refs--
		s.mu.Unlock()
	}
	lockCtx, cancel := context.WithTimeout(ctx, s.lockWait)
	defer cancel()
	select {
	case ks.sem <- struct{}{}:
	case <-lockCtx.Done():
		giveUp()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrProfileBusy
	}
	s.mu.Lock()
	gap := s.minGap - s.clock().Sub(ks.lastEnd)
	s.mu.Unlock()
	if gap > 0 {
		if err := s.wait(ctx, gap); err != nil {
			s.unlock(ks)
			return nil, err
		}
	}
	return ks, nil
}

func (s *ProfileStore) unlock(ks *keyState) {
	s.mu.Lock()
	ks.lastEnd = s.clock()
	ks.refs--
	s.mu.Unlock()
	<-ks.sem
}

// ProfileLease is exclusive use of one subject's profile for one login or one
// scrape. Dir holds the unpacked profile. The caller must end the lease with
// Commit (keep changes) or Discard; both are safe to call again and both remove Dir.
type ProfileLease struct {
	// Dir is the private work dir holding the unpacked profile.
	Dir string
	// Meta describes the profile; set Meta.Browser before committing a login.
	Meta ProfileMeta
	// Fresh is true when no usable archive existed and Dir is empty.
	Fresh bool

	store *ProfileStore
	key   string
	ks    *keyState
	gen   int
	// started is when the lease began (wall clock), compared with the mtime of
	// deletion tombstones left by other processes.
	started time.Time
	once    sync.Once
}

// Has reports whether a live saved profile exists for host's registrable domain.
func (s *ProfileStore) Has(sub Subject, host string) bool {
	_, key, err := resolve(sub, host)
	if err != nil {
		return false
	}
	_, ok := s.readMeta(key)
	return ok
}

// Acquire leases the saved profile for host's registrable domain, unpacked
// into a private work dir. It returns ErrNoProfile when there is none (an
// expired or unreadable archive is deleted), and ErrProfileBusy when another
// login or scrape holds it for longer than 30 s.
func (s *ProfileStore) Acquire(ctx context.Context, sub Subject, host string) (*ProfileLease, error) {
	domain, key, err := resolve(sub, host)
	if err != nil {
		return nil, err
	}
	if _, ok := s.readMeta(key); !ok {
		s.dropIfDead(key)
		return nil, ErrNoProfile
	}
	lease, err := s.begin(ctx, key, domain)
	if err != nil {
		return nil, err
	}
	if lease.Fresh {
		// The archive was unreadable or mismatched: remove it so it stops counting
		// toward the cap and is not unpacked on every scrape. Deleted while the key
		// lock is still held, so a concurrent login cannot lose a good profile.
		s.mu.Lock()
		_, _ = s.deleteLocked(key)
		s.mu.Unlock()
		lease.Discard()
		return nil, ErrNoProfile
	}
	return lease, nil
}

// Begin leases the profile for a login. It unpacks the saved profile when one
// exists so the user can sign in again over it; otherwise Dir is empty and
// Fresh is true. A new login always gets a new absolute lifetime.
func (s *ProfileStore) Begin(ctx context.Context, sub Subject, host string) (*ProfileLease, error) {
	domain, key, err := resolve(sub, host)
	if err != nil {
		return nil, err
	}
	if _, ok := s.readMeta(key); !ok {
		s.dropIfDead(key)
		if s.countFor(sub) >= MaxProfilesPerSubject {
			return nil, ErrTooManyProfiles
		}
		s.mu.Lock()
		room := s.hasRoom(key, 1)
		s.mu.Unlock()
		if !room {
			return nil, ErrProfileStoreFull
		}
	}
	lease, err := s.begin(ctx, key, domain)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	lease.Meta.CreatedAt, lease.Meta.ExpiresAt = now, now.Add(s.ttl)
	return lease, nil
}

func (s *ProfileStore) begin(ctx context.Context, key, domain string) (*ProfileLease, error) {
	s.maybeSweep()
	ks, err := s.lock(ctx, key)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	dir, err := os.MkdirTemp(s.workDir, "p-"+strconv.Itoa(os.Getpid())+"-*")
	if err != nil {
		s.unlock(ks)
		return nil, fmt.Errorf("create profile work dir: %w", err)
	}
	s.mu.Lock()
	gen := ks.gen
	s.mu.Unlock()
	l := &ProfileLease{Dir: dir, store: s, key: key, ks: ks, gen: gen, started: started, Fresh: true,
		Meta: ProfileMeta{Domain: domain, CreatedAt: s.clock(), ExpiresAt: s.clock().Add(s.ttl)}}

	f, err := os.Open(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		l.Discard()
		return nil, err
	}
	defer func() { _ = f.Close() }()
	meta, err := extractProfileArchive(f, s.gcm, profileAAD(key), dir)
	if err != nil || meta.Domain != domain || !meta.ExpiresAt.After(s.clock()) {
		// Unreadable, mismatched or expired: start over with an empty profile.
		slog.Warn("saved browser profile unreadable; starting fresh", "domain", domain)
		removeDir(dir)
		if err := os.Mkdir(dir, 0o700); err != nil {
			l.Discard()
			return nil, err
		}
		return l, nil
	}
	l.Meta, l.Fresh = meta, false
	return l, nil
}

// countFor counts the subject's live archives.
func (s *ProfileStore) countFor(sub Subject) int { return s.countPrefix(subjectPrefix(sub)) }

func (s *ProfileStore) countPrefix(prefix string) int {
	n := 0
	for _, k := range s.keysForPrefix(prefix) {
		if _, ok := s.readMeta(k); ok {
			n++
		}
	}
	return n
}

func (s *ProfileStore) keysFor(sub Subject) []string { return s.keysForPrefix(subjectPrefix(sub)) }

func (s *ProfileStore) keysForPrefix(prefix string) []string {
	matches, _ := filepath.Glob(filepath.Join(s.dir, prefix+"*"+profileFileExt))
	keys := make([]string, 0, len(matches))
	for _, m := range matches {
		keys = append(keys, strings.TrimSuffix(filepath.Base(m), profileFileExt))
	}
	return keys
}

// Commit repacks Dir into the encrypted archive (replacing the old one),
// removes Dir and ends the lease. The profile keeps its original expiry unless
// it came from Begin. If the profile was deleted while leased, nothing is
// written and ErrNoProfile is returned. The browser must be closed first.
func (l *ProfileLease) Commit(ctx context.Context) (ProfileSummary, error) {
	var sum ProfileSummary
	err := ErrNoProfile
	l.once.Do(func() {
		defer l.end()
		sum, err = l.store.commit(ctx, l)
	})
	return sum, err
}

// Discard removes Dir and ends the lease without saving. Idempotent.
func (l *ProfileLease) Discard() { l.once.Do(l.end) }

// removeDir deletes a work dir, retrying briefly: Chrome helper processes can
// still write for a moment after the debug port closes, which fails the first
// RemoveAll with "directory not empty". The dir holds plaintext cookies, so a
// final failure is logged without the error (it embeds the path) and the
// stale-dir sweep retries.
func removeDir(dir string) {
	for i := 0; i < 20; i++ {
		if os.RemoveAll(dir) == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	slog.Warn("could not remove a temporary browser folder; the next sweep will retry")
}

func (l *ProfileLease) end() {
	removeDir(l.Dir)
	l.store.unlock(l.ks)
}

func (s *ProfileStore) commit(ctx context.Context, l *ProfileLease) (ProfileSummary, error) {
	if err := ctx.Err(); err != nil {
		return ProfileSummary{}, err
	}
	tmp, err := os.CreateTemp(s.dir, "w-*.tmp")
	if err != nil {
		return ProfileSummary{}, fmt.Errorf("write profile archive: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	werr := writeProfileArchive(tmp, s.gcm, profileAAD(l.key), l.Dir, l.Meta)
	if werr == nil {
		werr = tmp.Sync()
	}
	if err := errors.Join(werr, tmp.Close()); err != nil {
		cleanup()
		var tooLarge *TooLargeError
		if errors.As(err, &tooLarge) {
			return ProfileSummary{}, tooLarge
		}
		if errors.Is(err, ErrProfileTooLarge) {
			return ProfileSummary{}, ErrProfileTooLarge
		}
		return ProfileSummary{}, fmt.Errorf("write profile archive: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.ks.gen != l.gen || s.revokedSince(l.key, l.started) {
		cleanup()
		return ProfileSummary{}, ErrNoProfile
	}
	// Begin checked the cap before the browser ran; another login may have used
	// the last slot since.
	if _, err := os.Stat(s.path(l.key)); errors.Is(err, os.ErrNotExist) && s.countPrefix(keyPrefix(l.key)) >= MaxProfilesPerSubject {
		cleanup()
		return ProfileSummary{}, ErrTooManyProfiles
	}
	if info, err := os.Stat(tmpName); err == nil && !s.hasRoom(l.key, info.Size()) {
		cleanup()
		return ProfileSummary{}, ErrProfileStoreFull
	}
	if err := os.Rename(tmpName, s.path(l.key)); err != nil {
		cleanup()
		return ProfileSummary{}, fmt.Errorf("store profile archive: %w", err)
	}
	return l.Meta.summary(), nil
}

// Delete removes the saved profile for host's registrable domain and reports
// whether one existed. A login or scrape in flight will not recreate it.
func (s *ProfileStore) Delete(ctx context.Context, sub Subject, host string) (bool, error) {
	_, key, err := resolve(sub, host)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	ok, err := s.deleteLocked(key)
	s.mu.Unlock()
	s.sweepWork() // plaintext folders of crashed leases go with the login
	return ok, err
}

// deleteLocked removes key's archive and invalidates any lease in flight. A
// missing file is (false, nil); any other failure is returned, so an erasure is
// never reported as done when the file is still there. The caller holds s.mu.
func (s *ProfileStore) deleteLocked(key string) (bool, error) {
	if ks := s.keys[key]; ks != nil {
		ks.gen++
	}
	err := os.Remove(s.path(key))
	s.writeTombstone(s.keyTombstone(key))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// List returns redacted summaries of the subject's live profiles.
func (s *ProfileStore) List(ctx context.Context, sub Subject) []ProfileSummary {
	out := []ProfileSummary{}
	if !sub.valid() {
		return out
	}
	for _, k := range s.keysFor(sub) {
		if m, ok := s.readMeta(k); ok {
			out = append(out, m.summary())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}

// EraseAll removes every profile of the subject and returns how many.
func (s *ProfileStore) EraseAll(ctx context.Context, sub Subject) (int, error) {
	if !sub.valid() {
		return 0, ErrNoSubject
	}
	s.mu.Lock()
	n, err := s.eraseLocked(sub)
	s.mu.Unlock()
	s.sweepWork()
	return n, err
}

func (s *ProfileStore) eraseLocked(sub Subject) (int, error) {
	// A first-time login has no archive yet; bump its generation so it cannot
	// commit after the erasure.
	prefix := subjectPrefix(sub)
	for k, ks := range s.keys {
		if strings.HasPrefix(k, prefix) {
			ks.gen++
		}
	}
	s.writeTombstone(s.subjectTombstone(prefix))
	n := 0
	var errs []error
	for _, k := range s.keysFor(sub) {
		ok, err := s.deleteLocked(k)
		if err != nil {
			errs = append(errs, fmt.Errorf("remove saved profile: %w", err))
		}
		if ok {
			n++
		}
	}
	return n, errors.Join(errs...)
}

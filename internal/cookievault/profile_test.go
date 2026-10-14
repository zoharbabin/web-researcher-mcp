package cookievault

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

const profileTestKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

var (
	subjA = Subject{TenantID: "t1", UserID: "u1"}
	subjB = Subject{TenantID: "t2", UserID: "u1"} // same user ID, other tenant
)

func newTestProfileStore(t *testing.T) *ProfileStore {
	t.Helper()
	s, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles"), profileTestKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.minGap = 0
	return s
}

// saveProfile logs subject in to host with one marker file in the profile.
func saveProfile(t *testing.T, s *ProfileStore, sub Subject, host, marker string) {
	t.Helper()
	l, err := s.Begin(context.Background(), sub, host)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(l.Dir, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, "Default", "Cookies"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	l.Meta.Browser = "/usr/bin/chrome"
	if _, err := l.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func readMarker(t *testing.T, l *ProfileLease) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(l.Dir, "Default", "Cookies"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProfileStoreSubjectIsolation(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "www.linkedin.com", "A-secret")
	ctx := context.Background()

	if _, err := s.Acquire(ctx, subjB, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("other tenant, same user ID: got %v, want ErrNoProfile", err)
	}
	if _, err := s.Acquire(ctx, Subject{TenantID: "t1", UserID: "u2"}, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("other user: got %v, want ErrNoProfile", err)
	}
	if got := s.List(ctx, subjB); len(got) != 0 {
		t.Fatalf("other tenant lists %v", got)
	}
	if n, _ := s.EraseAll(ctx, subjB); n != 0 {
		t.Fatalf("other tenant erased %d", n)
	}
	for _, bad := range []Subject{{}, {TenantID: "t1"}, {UserID: "u1"}, {TenantID: "t1", UserID: "anonymous"}} {
		if _, err := s.Begin(ctx, bad, "linkedin.com"); !errors.Is(err, ErrNoSubject) {
			t.Fatalf("subject %+v: got %v, want ErrNoSubject", bad, err)
		}
	}
	l, err := s.Acquire(ctx, subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Discard()
	if readMarker(t, l) != "A-secret" {
		t.Fatal("owner did not get own profile back")
	}
	if _, err := s.Begin(ctx, subjA, "localhost"); !errors.Is(err, ErrInvalidDomain) {
		t.Fatalf("localhost: got %v, want ErrInvalidDomain", err)
	}
}

func TestMultiInstanceProfileStore(t *testing.T) {
	t.Parallel()
	a, b := newTestProfileStore(t), newTestProfileStore(t)
	saveProfile(t, a, subjA, "linkedin.com", "only-in-a")
	if _, err := b.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("second instance saw first instance's profile: %v", err)
	}
	if len(b.List(context.Background(), subjA)) != 0 {
		t.Fatal("second instance lists first instance's profile")
	}
	// Lock state is per instance: holding a lease in a must not block b.
	la, err := a.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	defer la.Discard()
	saveProfile(t, b, subjA, "linkedin.com", "b")
}

func TestProfileArchiveEncryptedAtRest(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "linkedin.com", "COOKIE-VALUE-li_at-AQED123456")
	entries, _ := os.ReadDir(s.dir)
	found := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), profileFileExt) {
			continue
		}
		found = true
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range []string{"COOKIE-VALUE", "li_at", "linkedin", "Cookies"} {
			if bytes.Contains(raw, []byte(needle)) {
				t.Fatalf("%q visible in archive bytes", needle)
			}
		}
		if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
			t.Fatalf("archive mode %v, want 0600", info.Mode().Perm())
		}
		if strings.Contains(e.Name(), "linkedin") || strings.Contains(e.Name(), "u1") {
			t.Fatalf("file name leaks identity: %s", e.Name())
		}
	}
	if !found {
		t.Fatal("no archive written")
	}
	// A different key cannot open it.
	other, err := NewProfileStore(s.dir, strings.Repeat("ab", 32), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("wrong key opened archive: %v", err)
	}
}

func TestProfileTempDirRemovedOnAllExits(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	ctx := context.Background()
	workEmpty := func(when string) {
		t.Helper()
		if e, _ := os.ReadDir(s.workDir); len(e) != 0 {
			t.Fatalf("%s: work dir not empty: %d entries", when, len(e))
		}
	}

	saveProfile(t, s, subjA, "linkedin.com", "x")
	workEmpty("after commit")

	l, _ := s.Acquire(ctx, subjA, "linkedin.com")
	l.Discard()
	l.Discard()
	if _, err := l.Commit(ctx); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("commit after discard: %v", err)
	}
	workEmpty("after discard")

	l, _ = s.Acquire(ctx, subjA, "linkedin.com")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := l.Commit(cctx); err == nil {
		t.Fatal("commit with cancelled ctx succeeded")
	}
	workEmpty("after cancelled commit")

	// The lock was released on every path: a new lease is immediately possible.
	l, err := s.Acquire(ctx, subjA, "linkedin.com")
	if err != nil {
		t.Fatalf("lock leaked: %v", err)
	}
	l.Discard()

	func() {
		defer func() { _ = recover() }()
		l, _ := s.Acquire(ctx, subjA, "linkedin.com")
		defer l.Discard()
		panic("boom")
	}()
	workEmpty("after panic")
}

func TestStaleProfileTempDirsSwept(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "profiles")
	s, err := NewProfileStore(dir, profileTestKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(s.workDir, "p-stale")
	fresh := filepath.Join(s.workDir, "p-fresh")
	for _, d := range []string{stale, fresh} {
		if err := os.MkdirAll(filepath.Join(d, "Default"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProfileStore(dir, profileTestKey, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale work dir survived startup sweep")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh work dir (maybe another process's) was removed")
	}
}

// forgeArchive seals a hand-built tar so extraction hardening can be tested.
func forgeArchive(t *testing.T, key string, build func(tw *tar.Writer)) []byte {
	t.Helper()
	gcm, _ := cache.NewGCM(profileTestKey)
	var out bytes.Buffer
	enc, err := cache.NewStreamEncrypter(&out, gcm, profileAAD(key))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	meta := `{"v":1,"domain":"linkedin.com","browser":"b","createdAt":"2030-01-01T00:00:00Z","expiresAt":"2099-01-01T00:00:00Z"}`
	_ = tw.WriteHeader(&tar.Header{Name: profileMetaName, Mode: 0o600, Size: int64(len(meta)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(meta))
	build(tw)
	_ = tw.Close()
	_ = gz.Close()
	_ = enc.Close()
	return out.Bytes()
}

func TestProfileExtractRejectsTraversalAndLinks(t *testing.T) {
	t.Parallel()
	cases := map[string]func(tw *tar.Writer){
		"dotdot": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("x"))
		},
		"absolute": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "/tmp/evil", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("x"))
		},
		"symlink": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
		},
		"hardlink": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "hl", Linkname: "x", Typeflag: tar.TypeLink})
		},
		"device": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "dev", Typeflag: tar.TypeChar})
		},
	}
	for name, build := range cases {
		s := newTestProfileStore(t)
		_, key, _ := resolve(subjA, "linkedin.com")
		if err := os.WriteFile(s.path(key), forgeArchive(t, key, build), 0o600); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(filepath.Dir(s.dir), "evil")
		l, err := s.Begin(context.Background(), subjA, "linkedin.com")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !l.Fresh {
			t.Errorf("%s: hostile archive was accepted", name)
		}
		if entries, _ := os.ReadDir(l.Dir); len(entries) != 0 {
			t.Errorf("%s: files extracted from hostile archive", name)
		}
		if _, err := os.Stat(outside); err == nil {
			t.Errorf("%s: wrote outside the work dir", name)
		}
		l.Discard()
	}
}

func TestProfileScrapeSerialized(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "linkedin.com", "x")
	var inside, maxInside atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := s.Acquire(context.Background(), subjA, "linkedin.com")
			if err != nil {
				t.Error(err)
				return
			}
			n := inside.Add(1)
			for {
				m := maxInside.Load()
				if n <= m || maxInside.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inside.Add(-1)
			l.Discard()
		}()
	}
	wg.Wait()
	if maxInside.Load() != 1 {
		t.Fatalf("up to %d scrapes held one profile at once", maxInside.Load())
	}
}

func TestProfileScrapeMinSpacing(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return now }
	saveProfile(t, s, subjA, "linkedin.com", "x")
	s.minGap = 5 * time.Second
	var waits []time.Duration
	s.wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }

	l, err := s.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	l.Discard()
	now = now.Add(2 * time.Second)
	l, err = s.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	l.Discard()
	// The first lease follows the save at the same instant (full 5s gap); the
	// second follows 2s after the first (3s left).
	if len(waits) != 2 || waits[0] != 5*time.Second || waits[1] != 3*time.Second {
		t.Fatalf("waits = %v, want [5s 3s]", waits)
	}
}

func TestProfileScrapeBusyAfterWait(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "linkedin.com", "x")
	s.lockWait = 40 * time.Millisecond
	held, err := s.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrProfileBusy) {
		t.Fatalf("got %v, want ErrProfileBusy", err)
	}
	held.Discard()
	l, err := s.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	l.Discard()
}

func TestProfileArchiveExcludesCaches(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	l, _ := s.Begin(context.Background(), subjA, "linkedin.com")
	mk := func(rel, body string) {
		p := filepath.Join(l.Dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte(body), 0o600)
	}
	mk("Default/Cookies", "keep")
	mk("Default/Cache/data_0", "drop")
	mk("Default/Code Cache/js/x", "drop")
	mk("Default/GPUCache/x", "drop")
	mk("Default/Service Worker/CacheStorage/x", "drop")
	mk("Default/Local Storage/leveldb/x", "keep")
	mk("SingletonLock", "drop")
	mk("TranslateKit/models/m.bin", "drop")
	mk("WasmTtsEngine/1/x", "drop")
	mk("BrowserMetrics-spare.pma", "drop")
	mk("Default/.org.chromium.Chromium.AbC123", "drop")
	if err := os.Symlink("/etc/passwd", filepath.Join(l.Dir, "Default", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Discard()
	for rel, want := range map[string]bool{
		"Default/Cookies": true, "Default/Local Storage/leveldb/x": true,
		"Default/Cache/data_0": false, "Default/Code Cache/js/x": false, "Default/GPUCache/x": false,
		"Default/Service Worker/CacheStorage/x": false, "SingletonLock": false, "Default/link": false,
		"Default/.org.chromium.Chromium.AbC123": false,
		"TranslateKit/models/m.bin":             false, "WasmTtsEngine/1/x": false, "BrowserMetrics-spare.pma": false,
	} {
		_, err := os.Lstat(filepath.Join(got.Dir, rel))
		if (err == nil) != want {
			t.Errorf("%s present=%v, want %v", rel, err == nil, want)
		}
	}
}

func TestProfileArchiveSizeCap(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	l, _ := s.Begin(context.Background(), subjA, "linkedin.com")
	big := make([]byte, MaxProfileArchiveBytes+(2<<20))
	_, _ = rand.Read(big) // incompressible
	if err := os.WriteFile(filepath.Join(l.Dir, "blob"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := l.Commit(context.Background())
	if !errors.Is(err, ErrProfileTooLarge) {
		t.Fatalf("got %v, want ErrProfileTooLarge", err)
	}
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) || !strings.Contains(tooLarge.Detail, "blob") {
		t.Errorf("error %v does not name the large entry", err)
	}
	if entries, _ := os.ReadDir(s.dir); len(entries) != 1 { // only work/
		t.Fatalf("oversize attempt left files behind: %v", entries)
	}
	if len(s.List(context.Background(), subjA)) != 0 {
		t.Fatal("oversize profile was stored")
	}
}

func TestProfileArchiveTTL(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return now }
	saveProfile(t, s, subjA, "linkedin.com", "x")
	first := s.List(context.Background(), subjA)
	if len(first) != 1 || first[0].ExpiresAt != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("summary = %+v", first)
	}

	// Using the profile later does not extend its life.
	now = now.Add(40 * time.Minute)
	l, err := s.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.List(context.Background(), subjA); got[0].ExpiresAt != first[0].ExpiresAt {
		t.Fatalf("expiry slid: %s -> %s", first[0].ExpiresAt, got[0].ExpiresAt)
	}

	now = now.Add(21 * time.Minute) // past the original 1h
	if _, err := s.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("expired profile: %v", err)
	}
	if e, _ := filepath.Glob(filepath.Join(s.dir, "*"+profileFileExt)); len(e) != 0 {
		t.Fatalf("expired archive not deleted: %v", e)
	}
}

func TestProfileScrapeRepacksState(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "linkedin.com", "v1")
	l, _ := s.Acquire(context.Background(), subjA, "linkedin.com")
	if err := os.WriteFile(filepath.Join(l.Dir, "Default", "Cookies"), []byte("v2-refreshed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	l, _ = s.Acquire(context.Background(), subjA, "linkedin.com")
	defer l.Discard()
	if got := readMarker(t, l); got != "v2-refreshed" {
		t.Fatalf("refreshed state lost: %q", got)
	}
	if l.Meta.Browser != "/usr/bin/chrome" {
		t.Fatalf("browser path not kept: %q", l.Meta.Browser)
	}
}

func TestProfileDeleteWhileLeasedIsNotResurrected(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "linkedin.com", "x")
	l, _ := s.Acquire(context.Background(), subjA, "linkedin.com")
	if ok, err := s.Delete(context.Background(), subjA, "linkedin.com"); err != nil || !ok {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if _, err := l.Commit(context.Background()); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("commit after delete: %v", err)
	}
	if _, err := s.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("profile came back: %v", err)
	}
	if ok, _ := s.Delete(context.Background(), subjA, "linkedin.com"); ok {
		t.Fatal("second Delete reported a profile")
	}
}

func TestProfileCorruptAndTruncatedArchivesStartFresh(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "linkedin.com", "x")
	_, key, _ := resolve(subjA, "linkedin.com")
	raw, _ := os.ReadFile(s.path(key))
	if err := os.WriteFile(s.path(key), raw[:len(raw)-9], 0o600); err != nil { // truncated tail
		t.Fatal(err)
	}
	l, err := s.Begin(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Discard()
	if !l.Fresh {
		t.Fatal("truncated archive was accepted")
	}
}

func TestProfileLimitPerSubject(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	for i := range MaxProfilesPerSubject {
		saveProfile(t, s, subjA, "site"+string(rune('a'+i))+".com", "x")
	}
	if _, err := s.Begin(context.Background(), subjA, "extra.com"); !errors.Is(err, ErrTooManyProfiles) {
		t.Fatalf("got %v, want ErrTooManyProfiles", err)
	}
	// Re-login on an existing site is still allowed.
	l, err := s.Begin(context.Background(), subjA, "sitea.com")
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	l.Discard()
	if n, _ := s.EraseAll(context.Background(), subjA); n != MaxProfilesPerSubject {
		t.Fatalf("EraseAll = %d", n)
	}
}

func TestProfileEraseAllInvalidatesFirstTimeLogin(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	l, err := s.Begin(context.Background(), subjA, "linkedin.com") // no archive yet
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EraseAll(context.Background(), subjA); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Commit(context.Background()); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("login committed after erasure: %v", err)
	}
	if _, err := s.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("profile came back after erasure: %v", err)
	}
}

func TestProfileDeleteReportsRemovalFailure(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	_, key, _ := resolve(subjA, "linkedin.com")
	// A non-empty directory where the archive should be makes os.Remove fail.
	if err := os.MkdirAll(filepath.Join(s.path(key), "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Delete(context.Background(), subjA, "linkedin.com"); ok || err == nil {
		t.Fatalf("Delete = %v, %v; want a removal error", ok, err)
	}
	if n, err := s.EraseAll(context.Background(), subjA); n != 0 || err == nil {
		t.Fatalf("EraseAll = %d, %v; want a removal error", n, err)
	}
}

func TestProfileAcquireRemovesUnreadableArchive(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "linkedin.com", "x")
	_, key, _ := resolve(subjA, "linkedin.com")
	raw, _ := os.ReadFile(s.path(key))
	if err := os.WriteFile(s.path(key), raw[:len(raw)-9], 0o600); err != nil { // truncated tail
		t.Fatal(err)
	}
	if _, err := s.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("Acquire = %v, want ErrNoProfile", err)
	}
	if _, err := os.Stat(s.path(key)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreadable archive still on disk: %v", err)
	}
}

// A second store on the same directory stands in for a second server process.
func TestProfileDeleteInOtherProcessBlocksCommit(t *testing.T) {
	t.Parallel()
	a := newTestProfileStore(t)
	saveProfile(t, a, subjA, "linkedin.com", "x")
	b, err := NewProfileStore(a.dir, profileTestKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	l, err := a.Acquire(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Delete(context.Background(), subjA, "linkedin.com"); err != nil || !ok {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if _, err := l.Commit(context.Background()); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("commit after other process deleted: %v", err)
	}
	if _, err := a.Acquire(context.Background(), subjA, "linkedin.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("profile came back: %v", err)
	}
	// A login that starts after the deletion saves normally.
	saveProfile(t, a, subjA, "linkedin.com", "again")
}

func TestProfileEraseInOtherProcessBlocksFirstLogin(t *testing.T) {
	t.Parallel()
	a := newTestProfileStore(t)
	b, _ := NewProfileStore(a.dir, profileTestKey, time.Hour)
	l, err := a.Begin(context.Background(), subjA, "linkedin.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.EraseAll(context.Background(), subjA); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Commit(context.Background()); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("login committed after erasure in another process: %v", err)
	}
	// Another subject is unaffected.
	saveProfile(t, a, subjB, "linkedin.com", "b")
}

func TestProfileCommitRechecksCap(t *testing.T) {
	t.Parallel()
	s := newTestProfileStore(t)
	l, err := s.Begin(context.Background(), subjA, "late.com")
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxProfilesPerSubject {
		saveProfile(t, s, subjA, "site"+string(rune('a'+i))+".com", "x")
	}
	if _, err := l.Commit(context.Background()); !errors.Is(err, ErrTooManyProfiles) {
		t.Fatalf("got %v, want ErrTooManyProfiles", err)
	}
}

func TestProfileSweepRemovesDeadOwnerWorkDirs(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("owner liveness is not checked on Windows")
	}
	s := newTestProfileStore(t)
	dead := filepath.Join(s.workDir, "p-2147483646-abc") // no such process
	mine := filepath.Join(s.workDir, "p-"+strconv.Itoa(os.Getpid())+"-abc")
	for _, d := range []string{dead, mine} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EraseAll(context.Background(), subjA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("work dir of a dead process survived erasure")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("work dir of a live process was removed")
	}
}

func archiveSize(t *testing.T, s *ProfileStore, sub Subject, host string) int64 {
	t.Helper()
	_, key, err := resolve(sub, host)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.path(key))
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestProfileStoreTotalQuota(t *testing.T) {
	ctx := context.Background()
	s := newTestProfileStore(t)
	saveProfile(t, s, subjA, "one.com", "x")
	size := archiveSize(t, s, subjA, "one.com")

	// Full: a login for a new site is refused before the browser opens.
	s.WithMaxTotalBytes(size)
	if _, err := s.Begin(ctx, subjA, "two.com"); !errors.Is(err, ErrProfileStoreFull) {
		t.Fatalf("Begin on a full store: got %v, want ErrProfileStoreFull", err)
	}
	// Renewing the sign-in for a saved site replaces its archive and still fits.
	s.WithMaxTotalBytes(size + 1024)
	saveProfile(t, s, subjA, "one.com", "y")

	// Room at Begin, none at Commit (another login filled it): nothing is saved.
	s.WithMaxTotalBytes(size * 3 / 2)
	l, err := s.Begin(ctx, subjA, "two.com")
	if err != nil {
		t.Fatalf("Begin with room: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, "Cookies"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Commit(ctx); !errors.Is(err, ErrProfileStoreFull) {
		t.Fatalf("Commit on a full store: got %v, want ErrProfileStoreFull", err)
	}
	if got := s.List(ctx, subjA); len(got) != 1 || got[0].Domain != "one.com" {
		t.Fatalf("saved profiles = %+v, want only one.com", got)
	}
	if left, _ := filepath.Glob(filepath.Join(s.dir, "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestProfileStoreQuotaPrunesExpired(t *testing.T) {
	ctx := context.Background()
	s := newTestProfileStore(t)
	now := time.Now()
	s.clock = func() time.Time { return now }
	saveProfile(t, s, subjA, "old.com", "x")
	size := archiveSize(t, s, subjA, "old.com")
	s.WithMaxTotalBytes(size + size/2)

	now = now.Add(2 * time.Hour) // past the one-hour TTL
	saveProfile(t, s, subjB, "new.com", "y")
	if _, err := s.Acquire(ctx, subjA, "old.com"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("expired archive survived: %v", err)
	}
}

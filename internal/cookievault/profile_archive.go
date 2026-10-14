package cookievault

import (
	"archive/tar"
	"compress/gzip"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

// Profile archive limits (see issue #775).
const (
	// MaxProfileArchiveBytes caps the encrypted archive on disk.
	MaxProfileArchiveBytes = 64 << 20
	// MaxProfileUnpackedBytes caps the profile size before compression and
	// after extraction, so a corrupt or hostile archive cannot fill the disk.
	MaxProfileUnpackedBytes = 256 << 20
	// MaxProfileFiles caps the number of files in one profile.
	MaxProfileFiles = 20000

	profileMetaName    = ".wrmcp-meta.json"
	profileMetaMaxSize = 4 << 10
	profileMetaVersion = 1
)

// Errors returned by the profile archive code.
var (
	ErrProfileTooLarge    = errors.New("cookievault: browser profile is too large to store")
	ErrProfileArchiveBad  = errors.New("cookievault: stored profile is unreadable")
	errProfileUnsafeEntry = errors.New("cookievault: unsafe entry in profile archive")
)

// TooLargeError is ErrProfileTooLarge plus a size breakdown of the profile, so
// the operator can see what filled it. It matches ErrProfileTooLarge with errors.Is.
type TooLargeError struct {
	// Detail names the largest directories, for example "IndexedDB 180 MB".
	Detail string
}

func (e *TooLargeError) Error() string {
	return ErrProfileTooLarge.Error() + " (largest: " + e.Detail + ")"
}

func (e *TooLargeError) Is(target error) bool { return target == ErrProfileTooLarge }

// profileSizeDetail lists the five largest directories (two levels deep) that
// would be archived from root. Only directory names and sizes are returned.
func profileSizeDetail(root string) string {
	sizes := map[string]int64{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if excludedProfileDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !d.Type().IsRegular() {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if dirs := parts[:len(parts)-1]; len(dirs) > 0 {
			parts = dirs[:min(len(dirs), 2)]
		}
		sizes["/"+strings.Join(parts, "/")] += info.Size()
		return nil
	})
	type kv struct {
		name string
		size int64
	}
	list := make([]kv, 0, len(sizes))
	for k, v := range sizes {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].size > list[j].size })
	if len(list) > 5 {
		list = list[:5]
	}
	parts := make([]string, 0, len(list))
	for _, e := range list {
		parts = append(parts, fmt.Sprintf("%s %d MB", e.name, (e.size+(1<<20)-1)>>20))
	}
	return strings.Join(parts, ", ")
}

// excludedProfileDirs are cache directories (any depth) left out of the archive.
// They are large, rebuilt by the browser, and hold no login state.
var excludedProfileDirs = map[string]bool{
	"Cache": true, "Code Cache": true, "GPUCache": true, "GrShaderCache": true,
	"ShaderCache": true, "DawnGraphiteCache": true, "DawnWebGPUCache": true,
	"GraphiteDawnCache": true, "Crashpad": true, "component_crx_cache": true,
	"extensions_crx_cache": true, "optimization_guide_model_store": true,
	"BrowserMetrics": true, "Safe Browsing": true, "CacheStorage": true,
	"ScriptCache": true,
	// Chrome component-updater downloads (translation, speech and suggestion
	// models, safety lists). Tens of MB of incompressible data, no login state,
	// fetched again by the browser. TranslateKit alone was 78 MB and broke the
	// archive cap on Facebook sign-ins (issue #775).
	"TranslateKit": true, "WasmTtsEngine": true, "OnDeviceHeadSuggestModel": true,
	"OptGuideOnDeviceModel": true, "screen_ai": true, "WidevineCdm": true,
	"hyphen-data": true, "ActorSafetyLists": true, "ZxcvbnData": true,
	"CertificateRevocation": true, "OptimizationHints": true, "Subresource Filter": true,
	"PKIMetadata": true, "MEIPreload": true, "Media Cache": true,
}

// excludedProfileFiles are runtime lock and port files that must not be restored.
var excludedProfileFiles = map[string]bool{
	"SingletonLock": true, "SingletonSocket": true, "SingletonCookie": true,
	"lockfile": true, "DevToolsActivePort": true, "BrowserMetrics-spare.pma": true,
}

// chromiumTempPrefix marks Chrome's atomic-write temp files, which come and go
// while the browser shuts down.
const chromiumTempPrefix = ".org.chromium."

// ProfileMeta is stored inside the encrypted archive (authenticated with it).
type ProfileMeta struct {
	Version   int       `json:"v"`
	Domain    string    `json:"domain"`
	Browser   string    `json:"browser"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// cappedWriter fails once more than limit bytes pass through.
type cappedWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	if c.n > c.limit {
		return 0, ErrProfileTooLarge
	}
	return c.w.Write(p)
}

// writeProfileArchive packs root (minus caches and lock files) and meta into
// dst as tar+gzip sealed with AES-GCM. aad binds the archive to its owner.
func writeProfileArchive(dst io.Writer, gcm cipher.AEAD, aad []byte, root string, meta ProfileMeta) error {
	enc, err := cache.NewStreamEncrypter(&cappedWriter{w: dst, limit: MaxProfileArchiveBytes}, gcm, aad)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)

	meta.Version = profileMetaVersion
	mb, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: profileMetaName, Mode: 0o600, Size: int64(len(mb)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(mb); err != nil {
		return err
	}

	// Files are opened through an os.Root, so a symlink swapped in during the walk
	// cannot lead the read outside the profile dir.
	rootDir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = rootDir.Close() }()

	var total int64
	var files int
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path != root && errors.Is(err, fs.ErrNotExist) {
				return nil // vanished mid-walk (browser temp file)
			}
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if d.IsDir() {
			if excludedProfileDirs[d.Name()] {
				return filepath.SkipDir
			}
			return tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel) + "/", Mode: 0o700, Typeflag: tar.TypeDir})
		}
		if !d.Type().IsRegular() || excludedProfileFiles[d.Name()] || strings.HasPrefix(d.Name(), chromiumTempPrefix) {
			return nil // symlinks, sockets, lock files and browser temp files are never archived
		}
		f, err := rootDir.Open(rel)
		if errors.Is(err, fs.ErrNotExist) {
			return nil // vanished since the walk listed it
		}
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		files++
		total += info.Size()
		if files > MaxProfileFiles || total > MaxProfileUnpackedBytes {
			return ErrProfileTooLarge
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = io.Copy(tw, io.LimitReader(f, info.Size()))
		return err
	})
	if walkErr != nil {
		return tooLargeWithDetail(walkErr, root)
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return tooLargeWithDetail(err, root)
	}
	return tooLargeWithDetail(enc.Close(), root)
}

// tooLargeWithDetail turns a bare ErrProfileTooLarge into a TooLargeError for root.
func tooLargeWithDetail(err error, root string) error {
	if errors.Is(err, ErrProfileTooLarge) {
		return &TooLargeError{Detail: profileSizeDetail(root)}
	}
	return err
}

// openProfileArchive returns a tar reader positioned after the meta entry, the
// gzip reader under it, and the meta. The caller drains gz to authenticate the
// whole stream (a truncated archive fails only at the end).
func openProfileArchive(src io.Reader, gcm cipher.AEAD, aad []byte) (*tar.Reader, *gzip.Reader, ProfileMeta, error) {
	gz, err := gzip.NewReader(cache.NewStreamDecrypter(src, gcm, aad))
	if err != nil {
		return nil, nil, ProfileMeta{}, ErrProfileArchiveBad
	}
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != profileMetaName || h.Size > profileMetaMaxSize {
		return nil, nil, ProfileMeta{}, ErrProfileArchiveBad
	}
	var meta ProfileMeta
	if err := json.NewDecoder(io.LimitReader(tr, profileMetaMaxSize)).Decode(&meta); err != nil || meta.Version != profileMetaVersion {
		return nil, nil, ProfileMeta{}, ErrProfileArchiveBad
	}
	return tr, gz, meta, nil
}

// readProfileMeta reads only the meta entry; the rest of the stream is not opened.
func readProfileMeta(src io.Reader, gcm cipher.AEAD, aad []byte) (ProfileMeta, error) {
	_, _, meta, err := openProfileArchive(src, gcm, aad)
	return meta, err
}

// extractProfileArchive unpacks src into the existing empty directory dest and
// returns the meta. It refuses any entry that is not a plain file or directory
// inside dest, and enforces the size and file-count caps while writing.
func extractProfileArchive(src io.Reader, gcm cipher.AEAD, aad []byte, dest string) (ProfileMeta, error) {
	tr, gz, meta, err := openProfileArchive(src, gcm, aad)
	if err != nil {
		return ProfileMeta{}, err
	}
	var total int64
	var files int
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			if _, err := io.Copy(io.Discard, gz); err != nil {
				return ProfileMeta{}, ErrProfileArchiveBad
			}
			return meta, nil
		}
		if err != nil {
			return ProfileMeta{}, ErrProfileArchiveBad
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return ProfileMeta{}, fmt.Errorf("%w: %q", errProfileUnsafeEntry, h.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return ProfileMeta{}, err
			}
		case tar.TypeReg:
			files++
			if files > MaxProfileFiles || h.Size < 0 || h.Size > MaxProfileUnpackedBytes-total {
				return ProfileMeta{}, ErrProfileTooLarge
			}
			total += h.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return ProfileMeta{}, err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- target is validated to stay inside dest
			if err != nil {
				return ProfileMeta{}, err
			}
			_, cerr := io.Copy(f, io.LimitReader(tr, h.Size))
			if err := errors.Join(cerr, f.Close()); err != nil {
				return ProfileMeta{}, err
			}
		default:
			return ProfileMeta{}, fmt.Errorf("%w: %q", errProfileUnsafeEntry, h.Name)
		}
	}
}

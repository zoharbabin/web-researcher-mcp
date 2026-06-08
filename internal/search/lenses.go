package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Lens struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Domains     []string `json:"domains"`
	CX          string   `json:"cx"`
	Routing     string   `json:"routing,omitempty"`
}

type LensRegistry struct {
	mu     sync.RWMutex
	lenses map[string]*Lens
}

var defaultRegistry *LensRegistry
var registryOnce sync.Once

func GetLensRegistry() *LensRegistry {
	registryOnce.Do(func() {
		defaultRegistry = &LensRegistry{
			lenses: make(map[string]*Lens),
		}
	})
	return defaultRegistry
}

// ValidateLens checks a lens definition for the invariants the search path
// relies on: a non-empty name and at least one routing target (domains for
// site: injection, or a dedicated cx engine). A lens that satisfies neither is
// a silent no-op at query time, so it's rejected loudly here instead. Returns a
// descriptive error naming the offending lens (the source label, e.g. the
// filename) so a typo is actionable.
func ValidateLens(lens *Lens, source string) error {
	if lens == nil {
		return fmt.Errorf("lens %s: nil definition", source)
	}
	if strings.TrimSpace(lens.Name) == "" {
		return fmt.Errorf("lens %s: missing required \"name\"", source)
	}
	if len(lens.Domains) == 0 && strings.TrimSpace(lens.CX) == "" {
		return fmt.Errorf("lens %q (%s): must define at least one of \"domains\" or \"cx\" (otherwise it never restricts a search)", lens.Name, source)
	}
	for i, d := range lens.Domains {
		if strings.TrimSpace(d) == "" {
			return fmt.Errorf("lens %q (%s): domains[%d] is empty", lens.Name, source, i)
		}
		// A site: operator value: a host, optionally with a path prefix
		// (e.g. "github.com/advisories" is a valid path-scoped site: filter).
		// Reject a scheme or whitespace — those break the injected `site:` operator.
		if strings.Contains(d, "://") || strings.ContainsAny(d, " \t") {
			return fmt.Errorf("lens %q (%s): domain %q must be a bare host or host/path (e.g. \"example.com\" or \"example.com/section\"), not a URL with a scheme", lens.Name, source, d)
		}
	}
	return nil
}

// LoadFromDir loads every *.json lens in dir into the registry, validating each.
// Lenses already registered under the same name are OVERRIDDEN, so loading a
// custom directory after the bundled one lets operators extend or replace
// bundled lenses (last write wins). A malformed or invalid lens fails the whole
// load loudly (returns an error) rather than being silently skipped — a typo'd
// lens must not become an invisible no-op.
func (lr *LensRegistry) LoadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read lenses directory %s: %w", dir, err)
	}

	lr.mu.Lock()
	defer lr.mu.Unlock()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		// #nosec G304 -- reads lens definition files from the operator-configured lenses directory enumerated via os.ReadDir, not request input
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read lens file %s: %w", path, err)
		}

		var lens Lens
		if err := json.Unmarshal(data, &lens); err != nil {
			return fmt.Errorf("invalid lens JSON in %s: %w", path, err)
		}

		if lens.Name == "" {
			lens.Name = strings.TrimSuffix(entry.Name(), ".json")
		}

		if err := ValidateLens(&lens, entry.Name()); err != nil {
			return err
		}

		lr.lenses[lens.Name] = &lens
	}

	return nil
}

func (lr *LensRegistry) Get(name string) (*Lens, bool) {
	lr.mu.RLock()
	defer lr.mu.RUnlock()
	lens, ok := lr.lenses[name]
	return lens, ok
}

func (lr *LensRegistry) List() []string {
	lr.mu.RLock()
	defer lr.mu.RUnlock()

	names := make([]string, 0, len(lr.lenses))
	for name := range lr.lenses {
		names = append(names, name)
	}
	return names
}

func (lr *LensRegistry) BuildSiteQuery(query string, lens *Lens) string {
	if len(lens.Domains) == 0 {
		return query
	}

	// Use up to 10 domains in site: operators
	maxDomains := 10
	if len(lens.Domains) < maxDomains {
		maxDomains = len(lens.Domains)
	}

	siteOps := make([]string, maxDomains)
	for i := 0; i < maxDomains; i++ {
		siteOps[i] = "site:" + lens.Domains[i]
	}

	return query + " (" + strings.Join(siteOps, " OR ") + ")"
}

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
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var lens Lens
		if err := json.Unmarshal(data, &lens); err != nil {
			continue
		}

		if lens.Name == "" {
			lens.Name = strings.TrimSuffix(entry.Name(), ".json")
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

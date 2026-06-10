package search

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedLensesMatchRoot is the drift guard: the embedded copy in
// lenses_embed/ MUST be byte-identical to the canonical root lenses/ dir. The
// embedded set is generated (`make sync-lenses`); this fails CI if someone edits
// a root lens without regenerating, so the binary never ships stale lenses.
func TestEmbeddedLensesMatchRoot(t *testing.T) {
	t.Parallel()
	rootDir := filepath.Join("..", "..", "lenses")
	rootEntries, err := os.ReadDir(rootDir)
	if err != nil {
		t.Fatalf("read root lenses: %v", err)
	}
	rootJSON := map[string][]byte{}
	for _, e := range rootEntries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(rootDir, e.Name())) // #nosec G304 -- test fixture path
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		rootJSON[e.Name()] = b
	}

	embEntries, err := embeddedLenses.ReadDir("lenses_embed")
	if err != nil {
		t.Fatalf("read embedded lenses: %v", err)
	}
	embJSON := map[string][]byte{}
	for _, e := range embEntries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := embeddedLenses.ReadFile("lenses_embed/" + e.Name())
		if err != nil {
			t.Fatalf("read embedded %s: %v", e.Name(), err)
		}
		embJSON[e.Name()] = b
	}

	if len(rootJSON) != len(embJSON) {
		t.Fatalf("lens count drift: root has %d, embedded has %d — run `make sync-lenses`", len(rootJSON), len(embJSON))
	}
	for name, want := range rootJSON {
		got, ok := embJSON[name]
		if !ok {
			t.Errorf("embedded lenses missing %s — run `make sync-lenses`", name)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("embedded %s differs from root lenses/%s — run `make sync-lenses`", name, name)
		}
	}
}

// TestLoadEmbeddedRegistersLenses confirms the embedded set loads + validates,
// independent of any working directory (the uvx/go-install case).
func TestLoadEmbeddedRegistersLenses(t *testing.T) {
	lr := &LensRegistry{lenses: make(map[string]*Lens)}
	if err := lr.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if len(lr.List()) == 0 {
		t.Fatal("expected embedded lenses to register at least one lens")
	}
	// A known bundled lens must be present and valid.
	if _, ok := lr.Get("legal"); !ok {
		t.Error("expected the bundled 'legal' lens to load from the embed")
	}
}

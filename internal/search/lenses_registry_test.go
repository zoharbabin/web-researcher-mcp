package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateLens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		lens    Lens
		wantErr bool
	}{
		{"valid with domains", Lens{Name: "x", Domains: []string{"example.com"}}, false},
		{"valid with cx only", Lens{Name: "x", CX: "abc123"}, false},
		{"missing name", Lens{Domains: []string{"example.com"}}, true},
		{"no routing target", Lens{Name: "x"}, true},
		{"empty domain entry", Lens{Name: "x", Domains: []string{""}}, true},
		{"domain with path is OK (site: supports it)", Lens{Name: "x", Domains: []string{"github.com/advisories"}}, false},
		{"domain with scheme is rejected", Lens{Name: "x", Domains: []string{"https://example.com/path"}}, true},
		{"domain has space", Lens{Name: "x", Domains: []string{"exa mple.com"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateLens(&tt.lens, "test.json")
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateLens() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
	if err := ValidateLens(nil, "nil.json"); err == nil {
		t.Error("nil lens should error")
	}
}

func TestLoadFromDir_CustomMergeAndOverride(t *testing.T) {
	// A fresh registry (not the singleton) to avoid cross-test contamination.
	lr := &LensRegistry{lenses: make(map[string]*Lens)}

	bundled := t.TempDir()
	custom := t.TempDir()
	write := func(dir, file, body string) {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Bundled: one lens.
	write(bundled, "academic.json", `{"name":"academic","domains":["arxiv.org"]}`)
	// Custom: a new lens + an override of "academic".
	write(custom, "myfield.json", `{"name":"myfield","domains":["my.example.org"]}`)
	write(custom, "academic.json", `{"name":"academic","domains":["scholar.example.edu"]}`)

	if err := lr.LoadFromDir(bundled); err != nil {
		t.Fatalf("load bundled: %v", err)
	}
	if err := lr.LoadFromDir(custom); err != nil {
		t.Fatalf("load custom: %v", err)
	}

	// Custom adds myfield.
	if _, ok := lr.Get("myfield"); !ok {
		t.Error("custom lens 'myfield' not loaded")
	}
	// Custom overrides academic (last write wins).
	a, ok := lr.Get("academic")
	if !ok || len(a.Domains) != 1 || a.Domains[0] != "scholar.example.edu" {
		t.Errorf("custom override of 'academic' failed: %+v", a)
	}
}

func TestLoadFromDir_InvalidFailsLoudly(t *testing.T) {
	lr := &LensRegistry{lenses: make(map[string]*Lens)}
	dir := t.TempDir()
	// A lens with neither domains nor cx — a silent no-op at query time, must be rejected.
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"name":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lr.LoadFromDir(dir); err == nil {
		t.Fatal("expected LoadFromDir to fail loudly on an invalid lens, got nil")
	}
}

func TestLoadFromDir_MalformedJSONFailsLoudly(t *testing.T) {
	lr := &LensRegistry{lenses: make(map[string]*Lens)}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lr.LoadFromDir(dir); err == nil {
		t.Fatal("expected error on malformed lens JSON")
	}
}

// TestBundledLensesValid guards every shipped lens against the validator — this
// is what `make validate-lenses` exercises in CI so a malformed bundled lens
// can never ship.
func TestBundledLensesValid(t *testing.T) {
	lr := &LensRegistry{lenses: make(map[string]*Lens)}
	// repoRoot: this file is internal/search/, so ../../lenses.
	dir := filepath.Join("..", "..", "lenses")
	if err := lr.LoadFromDir(dir); err != nil {
		t.Fatalf("bundled lenses failed validation: %v", err)
	}
	if len(lr.List()) == 0 {
		t.Fatal("expected bundled lenses to load")
	}
}

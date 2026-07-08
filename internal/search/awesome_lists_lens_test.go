package search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestMultiInstanceLensRegistryIsolation proves lens registry state is
// per-instance, not global: loading a lens into one registry must not leak
// into a sibling registry (rule 1.1/1.2 isolation, see issue #354).
//
// It loads the real bundled set (including "awesome-lists") into lr1, then
// proves a custom lens loaded only into lr2 stays invisible to lr1 and leaves
// lr1's unrelated "legal" and "awesome-lists" lenses byte-for-byte unchanged.
func TestMultiInstanceLensRegistryIsolation(t *testing.T) {
	lr1 := &LensRegistry{lenses: make(map[string]*Lens)}
	lr2 := &LensRegistry{lenses: make(map[string]*Lens)}

	bundledDir := filepath.Join("..", "..", "lenses")
	if err := lr1.LoadFromDir(bundledDir); err != nil {
		t.Fatalf("lr1.LoadFromDir(bundled): %v", err)
	}

	// Snapshot lr1's "legal" and "awesome-lists" domains before lr2 does
	// anything, so we can later confirm lr2's load had zero effect on lr1.
	legalBefore, ok := lr1.Get("legal")
	if !ok {
		t.Fatal("expected bundled 'legal' lens to load into lr1")
	}
	legalDomainsSnapshot := append([]string{}, legalBefore.Domains...)

	awesomeBefore, ok := lr1.Get("awesome-lists")
	if !ok {
		t.Fatal("expected bundled 'awesome-lists' lens to load into lr1")
	}
	awesomeDomainsSnapshot := append([]string{}, awesomeBefore.Domains...)

	// A lens name that isn't bundled — confirm that on lr1.
	if _, ok := lr1.Get("totally-custom-test-lens"); ok {
		t.Fatal("did not expect 'totally-custom-test-lens' to exist in the bundled set")
	}

	// Load a custom lens into lr2 ONLY.
	customDir := t.TempDir()
	customLens := `{"name":"totally-custom-test-lens","domains":["example.com"]}`
	if err := os.WriteFile(filepath.Join(customDir, "custom.json"), []byte(customLens), 0o600); err != nil {
		t.Fatalf("write custom lens fixture: %v", err)
	}
	if err := lr2.LoadFromDir(customDir); err != nil {
		t.Fatalf("lr2.LoadFromDir(custom): %v", err)
	}

	// lr2 has it.
	got2, ok := lr2.Get("totally-custom-test-lens")
	if !ok {
		t.Fatal("expected lr2 to have loaded the custom lens")
	}
	if len(got2.Domains) != 1 || got2.Domains[0] != "example.com" {
		t.Errorf("lr2 custom lens domains = %+v, want [example.com]", got2.Domains)
	}

	// lr1 must NOT have it — registry state is per-instance, not global.
	if _, ok := lr1.Get("totally-custom-test-lens"); ok {
		t.Error("lr1 unexpectedly has the custom lens after loading it only into lr2 — registry state leaked across instances")
	}

	// lr1's unrelated "legal" and "awesome-lists" lenses must be byte-for-byte
	// unchanged by lr2's load.
	legalAfter, ok := lr1.Get("legal")
	if !ok {
		t.Fatal("lr1 lost its 'legal' lens after lr2's load")
	}
	if !reflect.DeepEqual(legalDomainsSnapshot, legalAfter.Domains) {
		t.Errorf("lr1 'legal' domains changed after lr2's load: before=%v after=%v", legalDomainsSnapshot, legalAfter.Domains)
	}

	awesomeAfter, ok := lr1.Get("awesome-lists")
	if !ok {
		t.Fatal("lr1 lost its 'awesome-lists' lens after lr2's load")
	}
	if !reflect.DeepEqual(awesomeDomainsSnapshot, awesomeAfter.Domains) {
		t.Errorf("lr1 'awesome-lists' domains changed after lr2's load: before=%v after=%v", awesomeDomainsSnapshot, awesomeAfter.Domains)
	}

	// And lr2 must not have picked up lr1's bundled lenses either (isolation
	// cuts both ways — lr2 was never loaded from bundledDir).
	if _, ok := lr2.Get("legal"); ok {
		t.Error("lr2 unexpectedly has 'legal' lens — it was never loaded from the bundled dir")
	}
}

// TestAwesomeListsLens_ExactlyTenDomains reads lenses/awesome-lists.json
// directly from disk and asserts it defines exactly 10 domains — the cap
// BuildSiteQuery uses (rule 4.1, see issue #354).
func TestAwesomeListsLens_ExactlyTenDomains(t *testing.T) {
	path := filepath.Join("..", "..", "lenses", "awesome-lists.json")
	data, err := os.ReadFile(path) // #nosec G304 -- fixed repo-relative test path, not request input
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var lens Lens
	if err := json.Unmarshal(data, &lens); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}

	if len(lens.Domains) != 10 {
		t.Errorf("awesome-lists lens has %d domains, want exactly 10: %v", len(lens.Domains), lens.Domains)
	}
}

// TestBundledLensesValid_UniqueNames proves no bundled lens silently shadows
// another via a same-name collision (rule 2.4): the number of lenses actually
// registered after loading the whole bundled lenses/ dir must equal the
// number of *.json files present — if two files declared (or defaulted to)
// the same name, LoadFromDir's last-write-wins merge would silently drop one
// and this count would mismatch.
func TestBundledLensesValid_UniqueNames(t *testing.T) {
	dir := filepath.Join("..", "..", "lenses")

	lr := &LensRegistry{lenses: make(map[string]*Lens)}
	if err := lr.LoadFromDir(dir); err != nil {
		t.Fatalf("LoadFromDir(%s): %v", dir, err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	fileCount := len(matches)
	if fileCount == 0 {
		t.Fatalf("expected at least one bundled lens *.json file in %s", dir)
	}

	registered := lr.List()
	if len(registered) != fileCount {
		t.Errorf("registered lens count = %d, want %d (one per *.json file) — a name collision is silently shadowing a bundled lens: files=%v registered=%v",
			len(registered), fileCount, matches, registered)
	}
}

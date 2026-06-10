package search

import "embed"

// Bundled lenses are embedded into the binary so they are ALWAYS available —
// regardless of the current working directory or install method. This matters
// for CWD-independent installs (uvx/pip wheels, `go install`, a bare binary run
// from anywhere): those have no adjacent on-disk lenses/ dir, and lenses are the
// project's core differentiator, so losing them would be a silent half-feature.
//
// The embedded copy in lenses_embed/ is GENERATED from the canonical root
// lenses/ dir — never edit it by hand. Run `make sync-lenses` (or
// `go generate ./internal/search/`) after changing a root lens; a CI drift test
// (TestEmbeddedLensesMatchRoot) fails if the two diverge.
//
//go:generate sh -c "cp ../../lenses/*.json lenses_embed/"
//go:embed lenses_embed/*.json
var embeddedLenses embed.FS

// LoadEmbedded loads the lenses baked into the binary. It is the always-present
// baseline: main.go loads these first, then overlays an on-disk lenses/ dir (for
// packaged installs that ship + allow editing them) and finally CUSTOM_LENSES_PATH,
// each overriding by name (last write wins).
func (lr *LensRegistry) LoadEmbedded() error {
	entries, err := embeddedLenses.ReadDir("lenses_embed")
	if err != nil {
		return err
	}
	return lr.loadEntries("embedded", entries, func(name string) ([]byte, error) {
		return embeddedLenses.ReadFile("lenses_embed/" + name)
	})
}

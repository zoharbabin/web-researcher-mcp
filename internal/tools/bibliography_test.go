package tools

import (
	"strings"
	"testing"
)

func TestFormatBibliographyExplicitAPA(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "format_bibliography", map[string]any{
		"style": "apa",
		"sources": []any{
			map[string]any{"url": "https://example.com/a", "title": "Attention Is All You Need", "author": "Vaswani, A.", "date": "2017"},
			map[string]any{"url": "https://example.com/b", "title": "BERT", "author": "Devlin, J.", "date": "2019"},
		},
	})
	if res.IsError {
		t.Fatalf("format_bibliography failed")
	}
	if out["style"] != "apa" {
		t.Errorf("style = %v", out["style"])
	}
	if c, _ := out["entryCount"].(float64); c != 2 {
		t.Errorf("entryCount = %v, want 2", out["entryCount"])
	}
	b, _ := out["bibliography"].(string)
	if !strings.Contains(b, "Attention Is All You Need") || !strings.Contains(b, "BERT") {
		t.Errorf("bibliography missing entries:\n%s", b)
	}
	if out["trust"] != "untrusted-external-content" {
		t.Error("missing trust marker")
	}
}

func TestFormatBibliographyBibTeX(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "format_bibliography", map[string]any{
		"style": "bibtex",
		"sources": []any{
			map[string]any{"url": "https://example.com/a", "title": "Attention Is All You Need", "author": "Vaswani, A.", "date": "2017"},
		},
	})
	if res.IsError {
		t.Fatalf("bibtex failed")
	}
	b, _ := out["bibliography"].(string)
	if !strings.HasPrefix(b, "@misc{vaswani2017") {
		t.Errorf("bibtex cite key wrong:\n%s", b)
	}
	if !strings.Contains(b, "year = {2017}") {
		t.Error("bibtex missing year")
	}
}

func TestFormatBibliographyDedupByURL(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "format_bibliography", map[string]any{
		"sources": []any{
			map[string]any{"url": "https://example.com/a", "title": "One"},
			map[string]any{"url": "https://example.com/a", "title": "Duplicate"},
		},
	})
	if res.IsError {
		t.Fatalf("dedup failed")
	}
	if c, _ := out["entryCount"].(float64); c != 1 {
		t.Errorf("entryCount = %v, want 1 after dedup", out["entryCount"])
	}
}

func TestFormatBibliographyInvalidStyle(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "format_bibliography", map[string]any{
		"style":   "chicago",
		"sources": []any{map[string]any{"url": "https://example.com/a"}},
	})
	if !res.IsError {
		t.Error("invalid style should error")
	}
}

func TestFormatBibliographyRequiresInput(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "format_bibliography", map[string]any{})
	if !res.IsError {
		t.Error("no session and no sources should error")
	}
}

// TestFormatBibliographyURLValidation: a malformed or dangerous-scheme URL is
// rejected before it lands verbatim in a citation; a real http(s) URL and a bare
// DOI are both accepted.
func TestFormatBibliographyURLValidation(t *testing.T) {
	bad := []string{"not a valid url at all", "javascript:alert(1)", "ftp://x/y", ""}
	for _, u := range bad {
		_, res := callTool(t, setupTestDeps(), "format_bibliography", map[string]any{
			"sources": []any{map[string]any{"url": u, "title": "X"}},
		})
		if !res.IsError {
			t.Errorf("url %q should be rejected", u)
		}
	}
	good := []string{"https://example.com/a", "10.1038/nature12373"}
	for _, u := range good {
		_, res := callTool(t, setupTestDeps(), "format_bibliography", map[string]any{
			"sources": []any{map[string]any{"url": u, "title": "X"}},
		})
		if res.IsError {
			t.Errorf("url %q should be accepted", u)
		}
	}
}

func TestFormatBibliographyFromSession(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithSources(t, deps)
	out, res := callTool(t, deps, "format_bibliography", map[string]any{"sessionId": sid, "style": "mla"})
	if res.IsError {
		t.Fatalf("session bibliography failed")
	}
	if out["sessionId"] != sid {
		t.Errorf("sessionId echo = %v", out["sessionId"])
	}
	if c, _ := out["entryCount"].(float64); c < 1 {
		t.Errorf("expected at least one source from session, got %v", out["entryCount"])
	}
}

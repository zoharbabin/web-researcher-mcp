package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type outlineEntry struct {
	Heading string `json:"heading"`
	Level   int    `json:"level"`
	Offset  int    `json:"offset"`
}

func sectionedPage(sections, bodyBytes int) string {
	var b strings.Builder
	for i := 0; i < sections; i++ {
		fmt.Fprintf(&b, "## Section %d\n\n", i)
		b.WriteString(strings.Repeat("Plain sentence about the topic. ", bodyBytes/32))
		b.WriteString("\n\n")
	}
	return b.String()
}

// #843: a multi-part page returns an outline, and each offset starts a section.
func TestFitScrapePage_OutlineJumpsToSection(t *testing.T) {
	t.Parallel()
	full := sectionedPage(30, 5_000) // about 150 KB, 30 sections

	b, err := fitScrapePage(budgetEnvelope(full, 0), 0, 30_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > maxInlineResultBytes {
		t.Fatalf("result %d bytes passes the budget %d", len(b), maxInlineResultBytes)
	}
	var out struct {
		Outline []outlineEntry `json:"outline"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Outline) != 30 {
		t.Fatalf("want 30 outline entries, got %d", len(out.Outline))
	}

	// Jump to section 20: the part must start at its heading.
	target := out.Outline[20]
	b, err = fitScrapePage(budgetEnvelope(full, 0), target.Offset, 30_000, "")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(page.Content, "## "+target.Heading) {
		t.Errorf("part at offset %d starts %q, want heading %q", target.Offset, page.Content[:20], target.Heading)
	}
}

func TestFitScrapePage_NoOutlineForSinglePart(t *testing.T) {
	t.Parallel()
	b, err := fitScrapePage(budgetEnvelope(sectionedPage(5, 1_000), 0), 0, 30_000, "")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, has := out["outline"]; has {
		t.Error("a page that fits in one part must not carry an outline")
	}
}

// Parts of one page share the same outline: it maps the whole page, not the
// part that was asked for.
func TestFitScrapePage_OutlineSameForEveryPart(t *testing.T) {
	t.Parallel()
	full := sectionedPage(30, 5_000)
	var first []outlineEntry
	for _, offset := range []int{0, 60_000} {
		b, err := fitScrapePage(budgetEnvelope(full, 0), offset, 30_000, "")
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Outline []outlineEntry `json:"outline"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Outline) != 30 {
			t.Fatalf("offset %d: want 30 entries, got %d", offset, len(out.Outline))
		}
		if first == nil {
			first = out.Outline
		} else if fmt.Sprint(first) != fmt.Sprint(out.Outline) {
			t.Error("outline differs between parts")
		}
	}
}

// #845: with a query the result lists the best passages of the whole page, not
// only of the returned part, and each offset starts the passage.
func TestFitScrapePage_QueryMatches(t *testing.T) {
	t.Parallel()
	full := sectionedPage(30, 5_000)
	needle := "The retention period for audit logs is 400 days under policy."
	at := len(full) * 3 / 4
	full = full[:at] + "\n\n" + needle + "\n\n" + full[at:]

	b, err := fitScrapePage(budgetEnvelope(full, 0), 0, 30_000, "audit logs retention period")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > maxInlineResultBytes {
		t.Fatalf("result %d bytes passes the budget %d", len(b), maxInlineResultBytes)
	}
	var out struct {
		Matches []struct {
			Offset int    `json:"offset"`
			Text   string `json:"text"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) == 0 || !strings.Contains(out.Matches[0].Text, "400 days") {
		t.Fatalf("answer passage not first: %+v", out.Matches)
	}
	if !strings.HasPrefix(full[out.Matches[0].Offset:], out.Matches[0].Text) {
		t.Error("match offset does not start the passage")
	}
}

func TestFitScrapePage_NoQueryNoMatches(t *testing.T) {
	t.Parallel()
	b, err := fitScrapePage(budgetEnvelope(sectionedPage(30, 5_000), 0), 0, 30_000, "")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, has := out["matches"]; has {
		t.Error("matches must be absent without a query")
	}
}

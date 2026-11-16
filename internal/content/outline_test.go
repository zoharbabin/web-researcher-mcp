package content

import (
	"strings"
	"testing"
)

func TestOutlineOffsetsPointAtHeadings(t *testing.T) {
	t.Parallel()
	text := "# Title\n\nintro\n\n## One\n\ntext\n\n### Deep\n\nmore\n\n## Two\n\nend"
	got := Outline(text)
	if len(got) != 4 {
		t.Fatalf("want 4 entries, got %+v", got)
	}
	for _, e := range got {
		if !strings.HasPrefix(text[e.Offset:], strings.Repeat("#", e.Level)+" "+e.Heading) {
			t.Errorf("offset %d does not start %q", e.Offset, e.Heading)
		}
	}
}

func TestOutlineTooFewHeadings(t *testing.T) {
	t.Parallel()
	if got := Outline("# A\n\ntext\n\n## B\n\ntext"); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestOutlineSkipsCodeFences(t *testing.T) {
	t.Parallel()
	text := "# A\n\n```\n# not a heading\n## nor this\n```\n\n## B\n\n## C\n"
	got := Outline(text)
	if len(got) != 3 {
		t.Fatalf("want 3 headings, got %+v", got)
	}
}

func TestOutlineRejectsNonHeadings(t *testing.T) {
	t.Parallel()
	text := "#hashtag\n####### seven\n# \n# A\n## B\n## C\n"
	got := Outline(text)
	if len(got) != 3 {
		t.Fatalf("want 3 headings, got %+v", got)
	}
}

func TestOutlineCapsByDroppingDeepLevels(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("## Section\n\n")
		for j := 0; j < 20; j++ {
			b.WriteString("### Sub\n\n")
		}
	}
	got := Outline(b.String())
	if len(got) != 10 {
		t.Fatalf("want the 10 level-2 entries, got %d", len(got))
	}
	for _, e := range got {
		if e.Level != 2 {
			t.Fatalf("unexpected level %d", e.Level)
		}
	}
}

func TestOutlineTruncatesLongHeading(t *testing.T) {
	t.Parallel()
	text := "# " + strings.Repeat("é", 200) + "\n## B\n## C\n"
	got := Outline(text)
	if len(got) != 3 || len(got[0].Heading) > maxOutlineHeadingLen+3 {
		t.Fatalf("heading not capped: %d", len(got[0].Heading))
	}
}

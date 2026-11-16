package content

import (
	"strings"
	"testing"
)

func filler(n int) string {
	return strings.Repeat("Unrelated filler sentence about weather and travel plans. ", n)
}

func TestRankPassagesFindsTheAnswerParagraph(t *testing.T) {
	t.Parallel()
	text := filler(10) + "\n\nThe retention period for audit logs is 400 days under policy.\n\n" + filler(10) + "\n\n" + filler(8)
	got := RankPassages(text, "audit logs retention period", 3)
	if len(got) == 0 || !strings.Contains(got[0].Text, "400 days") {
		t.Fatalf("answer paragraph not first: %+v", got)
	}
}

func TestRankPassagesOffsetsPointAtText(t *testing.T) {
	t.Parallel()
	text := "## One\n\n" + filler(12) + "\n\n## Two\n\nThe encryption algorithm used is AES-256-GCM everywhere.\n\n" + filler(12)
	for _, p := range RankPassages(text, "encryption algorithm AES", 5) {
		if !strings.HasPrefix(text[p.Offset:], p.Text) {
			t.Errorf("offset %d does not start passage %q", p.Offset, p.Text)
		}
	}
}

func TestRankPassagesHeadingJoinsItsParagraph(t *testing.T) {
	t.Parallel()
	text := filler(8) + "\n\n## Key rotation\n\nKeys are rotated every ninety days by the operator.\n\n" + filler(8)
	got := RankPassages(text, "keys rotated ninety days", 1)
	if len(got) != 1 || !strings.HasPrefix(got[0].Text, "## Key rotation") {
		t.Fatalf("heading should lead the passage: %+v", got)
	}
}

func TestRankPassagesCutsLongParagraphs(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("Filler words go here and there. ", 100) + "The quarterly revenue figure was 4.2 billion dollars. " + strings.Repeat("More filler words go here. ", 100)
	got := RankPassages(text, "quarterly revenue billion", 2)
	if len(got) == 0 {
		t.Fatal("no match")
	}
	for _, p := range got {
		if len(p.Text) > MaxPassageBytes {
			t.Errorf("passage %d bytes, cap %d", len(p.Text), MaxPassageBytes)
		}
	}
	if !strings.Contains(got[0].Text, "4.2 billion") && !strings.Contains(got[0].Text, "revenue") {
		t.Errorf("wrong passage: %q", got[0].Text)
	}
}

func TestRankPassagesRareTermOutweighsCommon(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("The system handles requests and the system returns responses to users.\n\n")
	}
	b.WriteString("The system uses a zebracorn scheduler for batch jobs and retries.\n\n")
	got := RankPassages(b.String(), "system zebracorn", 1)
	if len(got) != 1 || !strings.Contains(got[0].Text, "zebracorn") {
		t.Fatalf("rare term should win: %+v", got)
	}
}

func TestRankPassagesBestFirstAndCap(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 8; i++ {
		b.WriteString("The cache setting controls entry lifetime in minutes here.\n\n" + filler(3) + "\n\n")
	}
	b.WriteString("The cache expiry setting controls entry lifetime in minutes here.\n\n" + filler(3))
	got := RankPassages(b.String(), "cache expiry setting", 3)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "cache expiry setting") {
		t.Errorf("best match not first: %q", got[0].Text)
	}
	if got[1].Offset >= got[2].Offset {
		t.Error("equal scores should keep page order")
	}
}

func TestRankPassagesNoMatch(t *testing.T) {
	t.Parallel()
	if got := RankPassages(filler(20), "zzyzx quuxlet", 3); got != nil {
		t.Errorf("want nil, got %+v", got)
	}
	if got := RankPassages(filler(20), "the and of", 3); got != nil {
		t.Errorf("stop words only: want nil, got %+v", got)
	}
	if got := RankPassages("", "anything useful", 3); got != nil {
		t.Errorf("empty text: want nil, got %+v", got)
	}
}

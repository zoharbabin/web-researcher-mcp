package scraper

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func parseDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func paras(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("<p>" + prefix + " paragraph number " + strings.Repeat("x", i%7) + " with enough words to be unique and long: " + strings.Repeat("word ", 20) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + "</p>")
	}
	return b.String()
}

// #838: a short #content wrapper must not hide the rest of a long page.
func TestExtractArticleContent_TinyWrapperFallsBackToBody(t *testing.T) {
	t.Parallel()
	html := `<html><body>
<div id="content">` + paras("front", 3) + `</div>
<div class="doc">` + paras("body", 200) + `</div></body></html>`
	got := extractArticleContent(parseDoc(t, html), nil)
	if !strings.Contains(got, "body paragraph") || !strings.Contains(got, "front paragraph") {
		t.Fatalf("expected body and wrapper text, got %d chars", len(got))
	}
	if len(got) < 20000 {
		t.Fatalf("expected full body (>20000 chars), got %d", len(got))
	}
}

// A normal article page still selects <article> and drops outside text.
func TestExtractArticleContent_ArticleStillSelected(t *testing.T) {
	t.Parallel()
	html := `<html><body>
<article>` + paras("story", 20) + `</article>
<div class="related">` + paras("related", 3) + `</div></body></html>`
	got := extractArticleContent(parseDoc(t, html), nil)
	if !strings.Contains(got, "story paragraph") {
		t.Fatal("article text missing")
	}
	if strings.Contains(got, "related paragraph") {
		t.Fatal("text outside <article> should not be included")
	}
}

// A later selector with a real share wins over an earlier tiny one.
func TestExtractArticleContent_NextSelectorWhenFirstTiny(t *testing.T) {
	t.Parallel()
	html := `<html><body>
<main>` + paras("tiny", 3) + `</main>
<div class="post-content">` + paras("post", 60) + `</div>
<div class="misc">` + paras("misc", 5) + `</div></body></html>`
	got := extractArticleContent(parseDoc(t, html), nil)
	if !strings.Contains(got, "post paragraph") {
		t.Fatal("post-content text missing")
	}
}

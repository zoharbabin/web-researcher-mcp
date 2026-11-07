package scraper

import (
	"strings"
	"testing"
)

// =============================================================================
// #826 - shared author / publish date / site name extractor
// =============================================================================

// pciBylineSnippet mirrors the real blog.pcisecuritystandards.org post: an
// author meta tag plus a visible byline, and no date meta, <time> or JSON-LD.
const pciBylineSnippet = `<!doctype html><html><head>
<meta name="author" content="Alicia Malone">
<title>Just Published: PCI Key Management and Operations (KMO) Standard v1.0</title>
</head><body>
<div class="post-details-header">
<h1>Just Published: PCI Key Management and Operations (KMO) Standard v1.0</h1>
<div class="post-details-meta">
    Posted by
    <a class="author-link" href="https://blog.pcisecuritystandards.org/author/alicia-malone">Alicia Malone</a> on 14 Sep, 2026
    in <a class="topic-link" href="/topic/pin">PIN Security Standard</a> and
</div></div></body></html>`

func TestExtractHTMLMetadata_Sources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		html         string
		wantAuthor   string
		wantDate     string
		wantSiteName string
	}{
		{"PCI byline", pciBylineSnippet, "Alicia Malone", "2026-09-14", ""},
		{"article:published_time", `<meta property="article:published_time" content="2025-03-04T10:00:00Z">`, "", "2025-03-04", ""},
		{"og:article:published_time", `<meta property="og:article:published_time" content="2025-03-05">`, "", "2025-03-05", ""},
		{"meta name=date", `<meta name="date" content="2024-12-31">`, "", "2024-12-31", ""},
		{"DC.date", `<meta name="DC.date" content="2023/07/01">`, "", "2023-07-01", ""},
		{"dcterms.created", `<meta name="dcterms.created" content="2022-02-03T04:05:06+02:00">`, "", "2022-02-03", ""},
		{"citation_publication_date", `<meta name="citation_publication_date" content="2021-11-09">`, "", "2021-11-09", ""},
		{"year only kept", `<meta name="date" content="2019">`, "", "2019", ""},
		{"year-month kept", `<meta name="date" content="2019-05">`, "", "2019-05", ""},
		{"JSON-LD datePublished", `<script type="application/ld+json">{"@type":"Article","datePublished":"2020-01-15T08:00:00-05:00","author":{"@type":"Person","name":"Dana Lee"}}</script>`, "Dana Lee", "2020-01-15", ""},
		{"JSON-LD @graph", `<script type="application/ld+json">{"@graph":[{"@type":"WebSite"},{"@type":"NewsArticle","datePublished":"2018-06-07","author":[{"name":"A One"},{"name":"B Two"}]}]}</script>`, "A One, B Two", "2018-06-07", ""},
		{"JSON-LD array top level", `<script type="application/ld+json">[{"@type":"Article","datePublished":"2017-08-09","author":"Solo Writer"}]</script>`, "Solo Writer", "2017-08-09", ""},
		{"invalid JSON-LD skipped", `<script type="application/ld+json">{not json</script><meta name="date" content="2016-04-05">`, "", "2016-04-05", ""},
		{"time in article", `<article><time datetime="2015-09-10T12:00:00Z">Sep 10</time><p>x</p></article>`, "", "2015-09-10", ""},
		{"time in header", `<header><time datetime="2014-10-11">x</time></header>`, "", "2014-10-11", ""},
		{"time outside article ignored", `<div><time datetime="2013-01-02">x</time></div>`, "", "", ""},
		{"itemprop datePublished content", `<meta itemprop="datePublished" content="2012-03-04">`, "", "2012-03-04", ""},
		{"itemprop datePublished text", `<span itemprop="datePublished">March 4, 2011</span>`, "", "2011-03-04", ""},
		{"meta author", `<meta name="author" content="  Meta   Person ">`, "Meta Person", "", ""},
		{"article:author", `<meta property="article:author" content="Prop Person">`, "Prop Person", "", ""},
		{"article:author URL ignored", `<meta property="article:author" content="https://facebook.com/someone">`, "", "", ""},
		{"itemprop author nested name", `<div itemprop="author"><span itemprop="name">Item Person</span></div>`, "Item Person", "", ""},
		{"itemprop author text", `<span itemprop="author">Plain Item</span>`, "Plain Item", "", ""},
		{"rel=author text", `<a rel="author" href="/a">Rel Person</a>`, "Rel Person", "", ""},
		{"site name", `<meta property="og:site_name" content="Example News">`, "", "", "Example News"},
		{"byline Month D, YYYY", `<div class="post-byline">By <a rel="author" href="/a">Jo Smith</a> | September 3, 2024</div>`, "Jo Smith", "2024-09-03", ""},
		{"byline full month", `<p><a class="author-link" href="/a">Ann</a> on 5 March 2021</p>`, "", "2021-03-05", ""},
		{"no signal", `<html><body><p>Just text.</p></body></html>`, "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := extractHTMLMetadata(docFrom(t, tc.html))
			if m.author != tc.wantAuthor {
				t.Errorf("author = %q, want %q", m.author, tc.wantAuthor)
			}
			if m.publishDate != tc.wantDate {
				t.Errorf("publishDate = %q, want %q", m.publishDate, tc.wantDate)
			}
			if m.siteName != tc.wantSiteName {
				t.Errorf("siteName = %q, want %q", m.siteName, tc.wantSiteName)
			}
		})
	}
}

func TestExtractHTMLMetadata_Precedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		html       string
		wantAuthor string
		wantDate   string
	}{
		{
			"meta date beats JSON-LD, time and byline",
			`<meta property="article:published_time" content="2025-01-01">
			 <script type="application/ld+json">{"datePublished":"2024-02-02"}</script>
			 <article><time datetime="2023-03-03"></time></article>
			 <p><a class="author-link">X</a> on 4 Apr, 2022</p>`,
			"", "2025-01-01",
		},
		{
			"JSON-LD beats time and byline",
			`<script type="application/ld+json">{"datePublished":"2024-02-02"}</script>
			 <article><time datetime="2023-03-03"></time></article>`,
			"", "2024-02-02",
		},
		{
			"time beats byline",
			`<article><time datetime="2023-03-03"></time><p><a class="author-link">X</a> on 4 Apr, 2022</p></article>`,
			"", "2023-03-03",
		},
		{
			"unparsable earlier source falls through to next",
			`<meta property="article:published_time" content="soon">
			 <meta name="date" content="2020-05-06">`,
			"", "2020-05-06",
		},
		{
			"meta author beats JSON-LD and rel=author",
			`<meta name="author" content="Meta A">
			 <script type="application/ld+json">{"author":{"name":"LD B"}}</script>
			 <a rel="author">Rel C</a>`,
			"Meta A", "",
		},
		{
			"JSON-LD author beats itemprop and rel=author",
			`<script type="application/ld+json">{"author":{"name":"LD B"}}</script>
			 <span itemprop="author">Item D</span><a rel="author">Rel C</a>`,
			"LD B", "",
		},
		{
			"itemprop author beats rel=author",
			`<span itemprop="author">Item D</span><a rel="author">Rel C</a>`,
			"Item D", "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := extractHTMLMetadata(docFrom(t, tc.html))
			if m.author != tc.wantAuthor {
				t.Errorf("author = %q, want %q", m.author, tc.wantAuthor)
			}
			if m.publishDate != tc.wantDate {
				t.Errorf("publishDate = %q, want %q", m.publishDate, tc.wantDate)
			}
		})
	}
}

// A byline date that does not parse yields no date: never a guessed day.
func TestExtractHTMLMetadata_BylineStrict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		html string
	}{
		{"impossible day", `<p><a class="author-link">X</a> on 31 Feb, 2026</p>`},
		{"unknown month", `<p><a class="author-link">X</a> on 14 Foo, 2026</p>`},
		{"month and year only", `<p><a class="author-link">X</a> on March 2026</p>`},
		{"date outside any byline container", `<p>Posted on 14 Sep, 2026 by staff</p>`},
		{"date far from the byline", `<div><a class="author-link">X</a></div><section><p>` + strings.Repeat("filler ", 100) + `on 14 Sep, 2026</p></section>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractHTMLMetadata(docFrom(t, tc.html)).publishDate; got != "" {
				t.Errorf("publishDate = %q, want empty", got)
			}
		})
	}
}

func TestNormalizeDate(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"2026-09-14T23:30:00-05:00", "2026-09-14"},
		{"2026-09-14T10:00:00.123Z", "2026-09-14"},
		{"2026-09-14 10:00:00", "2026-09-14"},
		{"Mon, 14 Sep 2026 10:00:00 GMT", "2026-09-14"},
		{"14 Sep, 2026", "2026-09-14"},
		{"Sep 14, 2026", "2026-09-14"},
		{"September 14, 2026", "2026-09-14"},
		{"2026", "2026"},
		{"2026-09", "2026-09"},
		{"", ""},
		{"yesterday", ""},
		{"2026-13-40", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := normalizeDate(tc.in); got != tc.want {
				t.Errorf("normalizeDate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

package content

import "testing"

// Fuzz targets for issue #476: SanitizeHTML/SanitizeText run over arbitrary
// HTML scraped from the open web, so a crafted fragment must never panic or
// hang. Seeds are drawn from the existing table-driven fixtures in
// content_test.go.

func FuzzSanitizeHTML(f *testing.F) {
	seeds := []string{
		`<p>Hello</p><script>alert('xss')</script>`,
		`<a href="https://example.com" onclick="evil()">link</a>`,
		`<a href="javascript:alert(1)">click</a>`,
		`<p>Safe</p><iframe src="evil.com"></iframe>`,
		`<img src="x.png" alt="pic" onerror="alert(1)">`,
		`<h1>Title</h1><p>Paragraph with <strong>bold</strong> and <em>italic</em></p>`,
		`<style>body{display:none}</style><p>visible</p>`,
		`<object data="evil.swf"></object><embed src="evil.swf"><p>ok</p>`,
		``,
		`<`,
		`<<<<<<<<<<`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	sanitizer := NewSanitizer()
	f.Fuzz(func(t *testing.T, html string) {
		sanitizer.SanitizeHTML(html)
	})
}

func FuzzSanitizeText(f *testing.F) {
	seeds := []string{
		"hello​world",
		"\uFEFFhello",
		"visible text\ndisplay: none; some hidden content\nmore visible",
		"para1\n\n\n\n\npara2",
		"hello    world",
		"   hello world   ",
		"",
		"   \n\n\n   ",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	sanitizer := NewSanitizer()
	f.Fuzz(func(t *testing.T, text string) {
		sanitizer.SanitizeText(text)
	})
}

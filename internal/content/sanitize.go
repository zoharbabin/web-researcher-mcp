package content

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

var (
	zeroWidthChars = regexp.MustCompile(`[\x{200B}\x{200C}\x{200D}\x{FEFF}\x{2060}]`)
	multiNewlines  = regexp.MustCompile(`\n{3,}`)
	multiSpaces    = regexp.MustCompile(`[ \t]{2,}`)
	hiddenCSS      = regexp.MustCompile(`(?i)display\s*:\s*none|visibility\s*:\s*hidden|font-size\s*:\s*0`)
)

type Sanitizer struct {
	policy *bluemonday.Policy
}

func NewSanitizer() *Sanitizer {
	p := bluemonday.UGCPolicy()
	p.AllowElements("p", "br", "h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "blockquote", "pre", "code",
		"table", "thead", "tbody", "tr", "th", "td",
		"strong", "em", "a", "img")
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("src", "alt").OnElements("img")
	p.RequireNoFollowOnLinks(true)
	return &Sanitizer{policy: p}
}

func (s *Sanitizer) SanitizeHTML(html string) string {
	return s.policy.Sanitize(html)
}

func (s *Sanitizer) SanitizeText(text string) string {
	result := zeroWidthChars.ReplaceAllString(text, "")
	result = removeHiddenContent(result)
	result = multiNewlines.ReplaceAllString(result, "\n\n")
	result = multiSpaces.ReplaceAllString(result, " ")
	result = strings.TrimSpace(result)
	return result
}

func removeHiddenContent(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		if hiddenCSS.MatchString(line) {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

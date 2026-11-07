package content

type Processor struct {
	sanitizer *Sanitizer
}

func NewProcessor() *Processor {
	return &Processor{
		sanitizer: NewSanitizer(),
	}
}

// Clean sanitizes and dedups raw text without cutting it to a length.
func (p *Processor) Clean(raw string) string {
	return DedupContent(p.sanitizer.SanitizeText(raw))
}

func (p *Processor) Process(raw string, maxLength int) (string, bool) {
	return Truncate(p.Clean(raw), maxLength)
}

func (p *Processor) SanitizeHTML(html string) string {
	return p.sanitizer.SanitizeHTML(html)
}

func (p *Processor) SanitizeText(text string) string {
	return p.sanitizer.SanitizeText(text)
}

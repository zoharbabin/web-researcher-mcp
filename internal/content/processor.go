package content

type Processor struct {
	sanitizer *Sanitizer
}

func NewProcessor() *Processor {
	return &Processor{
		sanitizer: NewSanitizer(),
	}
}

func (p *Processor) Process(raw string, maxLength int) (string, bool) {
	cleaned := p.sanitizer.SanitizeText(raw)
	cleaned = DedupContent(cleaned)
	result, truncated := Truncate(cleaned, maxLength)
	return result, truncated
}

func (p *Processor) SanitizeHTML(html string) string {
	return p.sanitizer.SanitizeHTML(html)
}

func (p *Processor) SanitizeText(text string) string {
	return p.sanitizer.SanitizeText(text)
}

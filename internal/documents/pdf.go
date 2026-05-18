package documents

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var pdfTextRegex = regexp.MustCompile(`\(((?:[^\\)]|\\.)*)\)`)

func parsePDF(reader io.ReaderAt, size int64) (string, Metadata, error) {
	meta := Metadata{
		FileSize: size,
	}

	// Read entire content
	buf := make([]byte, size)
	_, err := reader.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return "", meta, fmt.Errorf("failed to read PDF: %w", err)
	}

	// Basic PDF text extraction - extract text between stream/endstream
	// and parenthesized strings in text objects
	content := string(buf)

	if !bytes.HasPrefix(buf, []byte("%PDF")) {
		return "", meta, fmt.Errorf("not a valid PDF file")
	}

	// Count pages (rough estimate)
	meta.PageCount = strings.Count(content, "/Type /Page")
	if meta.PageCount == 0 {
		meta.PageCount = strings.Count(content, "/Type/Page")
	}

	// Extract text from PDF text objects
	var sb strings.Builder
	matches := pdfTextRegex.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			text := unescapePDFString(m[1])
			if isReadableText(text) {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
	}

	result := strings.TrimSpace(sb.String())
	if result == "" {
		return "", meta, fmt.Errorf("no extractable text in PDF (may be image-based)")
	}

	return result, meta, nil
}

func unescapePDFString(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\r", "\r")
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\(", "(")
	s = strings.ReplaceAll(s, "\\)", ")")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

func isReadableText(s string) bool {
	if len(s) < 2 {
		return false
	}
	readable := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == ' ' || r == '.' || r == ',' {
			readable++
		}
	}
	return float64(readable)/float64(len(s)) > 0.5
}

package documents

import (
	"fmt"
	"io"

	"github.com/razvandimescu/gopdf/pdf"
)

func parsePDF(_ io.ReaderAt, size int64, data []byte) (string, Metadata, error) {
	meta := Metadata{
		FileSize: size,
	}

	if len(data) < 4 || string(data[:4]) != "%PDF" {
		return "", meta, fmt.Errorf("not a valid PDF file")
	}

	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return "", meta, fmt.Errorf("failed to parse PDF: %w", err)
	}

	meta.PageCount = doc.NumPages()

	text, err := doc.Text()
	if err != nil {
		return "", meta, fmt.Errorf("no extractable text in PDF: %w", err)
	}

	if text == "" {
		return "", meta, fmt.Errorf("no extractable text in PDF (may be image-based)")
	}

	return text, meta, nil
}

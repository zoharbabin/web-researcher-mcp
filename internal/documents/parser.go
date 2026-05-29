package documents

import (
	"bytes"
	"fmt"
)

type Metadata struct {
	Title     string `json:"title,omitempty"`
	Author    string `json:"author,omitempty"`
	PageCount int    `json:"pageCount,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	FileSize  int64  `json:"fileSize,omitempty"`
}

func Parse(data []byte, docType string) (string, Metadata, error) {
	switch docType {
	case "pdf":
		return parsePDF(bytes.NewReader(data), int64(len(data)), data)
	case "docx":
		return parseDOCX(data)
	case "pptx":
		return parsePPTX(data)
	default:
		return "", Metadata{}, fmt.Errorf("unsupported document type: %s", docType)
	}
}

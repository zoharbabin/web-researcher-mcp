package documents

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

func parsePPTX(data []byte) (string, Metadata, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", Metadata{}, err
	}

	meta := Metadata{FileSize: int64(len(data))}

	// Find slide files
	var slideFiles []*zip.File
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slideFiles = append(slideFiles, f)
		}
	}

	sort.Slice(slideFiles, func(i, j int) bool {
		return slideFiles[i].Name < slideFiles[j].Name
	})

	meta.PageCount = len(slideFiles)

	var sb strings.Builder
	for i, f := range slideFiles {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		text := extractSlideText(rc)
		_ = rc.Close()
		if text != "" {
			sb.WriteString(fmt.Sprintf("--- Slide %d ---\n", i+1))
			sb.WriteString(text)
			sb.WriteString("\n\n")
		}
	}

	return strings.TrimSpace(sb.String()), meta, nil
}

func extractSlideText(r io.Reader) string {
	decoder := xml.NewDecoder(r)
	var sb strings.Builder
	var inText bool

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
			if t.Name.Local == "p" && sb.Len() > 0 {
				sb.WriteString("\n")
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
	}

	return strings.TrimSpace(sb.String())
}

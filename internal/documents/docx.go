package documents

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

func parseDOCX(data []byte) (string, Metadata, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", Metadata{}, err
	}

	meta := Metadata{FileSize: int64(len(data))}

	// Extract core properties
	for _, f := range r.File {
		if f.Name == "docProps/core.xml" {
			if props, err := readCoreProps(f); err == nil {
				meta.Title = props.Title
				meta.Author = props.Creator
			}
		}
	}

	// Extract document text
	var text string
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			text = extractDOCXText(rc)
			rc.Close()
			break
		}
	}

	return text, meta, nil
}

type coreProps struct {
	Title   string `xml:"title"`
	Creator string `xml:"creator"`
}

func readCoreProps(f *zip.File) (coreProps, error) {
	rc, err := f.Open()
	if err != nil {
		return coreProps{}, err
	}
	defer rc.Close()

	var props coreProps
	if err := xml.NewDecoder(rc).Decode(&props); err != nil {
		return coreProps{}, err
	}
	return props, nil
}

func extractDOCXText(r io.Reader) string {
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
			switch t.Name.Local {
			case "t":
				inText = true
			case "p":
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
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

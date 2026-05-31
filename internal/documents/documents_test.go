package documents

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// =============================================================================
// PDF Parsing Tests
// =============================================================================

func TestParsePDF_InvalidHeader(t *testing.T) {
	data := []byte("This is not a PDF file at all")
	_, _, err := Parse(data, "pdf")
	if err == nil {
		t.Fatal("expected error for non-PDF data")
	}
	if !strings.Contains(err.Error(), "not a valid PDF") {
		t.Errorf("expected 'not a valid PDF' error, got: %v", err)
	}
}

func TestParsePDF_MalformedPDF(t *testing.T) {
	data := []byte("%PDF-1.4\ngarbage data without structure\n%%EOF")
	_, _, err := Parse(data, "pdf")
	if err == nil {
		t.Fatal("expected error for malformed PDF")
	}
}

func TestParsePDF_EmptyBytes(t *testing.T) {
	_, _, err := Parse([]byte{}, "pdf")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParsePDF_FileSize(t *testing.T) {
	data := []byte("%PDF-1.4\nsome content\n%%EOF")
	_, meta, _ := Parse(data, "pdf")
	if meta.FileSize != int64(len(data)) {
		t.Errorf("expected FileSize %d, got %d", len(data), meta.FileSize)
	}
}

// =============================================================================
// DOCX Parsing Tests
// =============================================================================

func TestParseDOCX_ValidDocument(t *testing.T) {
	docx := buildMinimalDOCX("Hello from DOCX", "Test Title", "Test Author")

	text, meta, err := Parse(docx, "docx")
	if err != nil {
		t.Fatalf("parseDOCX error: %v", err)
	}

	if !strings.Contains(text, "Hello from DOCX") {
		t.Errorf("expected text to contain 'Hello from DOCX', got %q", text)
	}
	if meta.Title != "Test Title" {
		t.Errorf("expected title 'Test Title', got %q", meta.Title)
	}
	if meta.Author != "Test Author" {
		t.Errorf("expected author 'Test Author', got %q", meta.Author)
	}
	if meta.FileSize == 0 {
		t.Error("expected non-zero FileSize")
	}
}

func TestParseDOCX_MultiParagraph(t *testing.T) {
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>First paragraph</w:t></w:r></w:p>
<w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
<w:p><w:r><w:t>Third paragraph</w:t></w:r></w:p>
</w:body>
</w:document>`

	docx := buildDOCXWithContent(documentXML, "", "")

	text, _, err := Parse(docx, "docx")
	if err != nil {
		t.Fatalf("parseDOCX error: %v", err)
	}

	if !strings.Contains(text, "First paragraph") {
		t.Error("expected 'First paragraph' in text")
	}
	if !strings.Contains(text, "Second paragraph") {
		t.Error("expected 'Second paragraph' in text")
	}
	if !strings.Contains(text, "Third paragraph") {
		t.Error("expected 'Third paragraph' in text")
	}

	// Paragraphs should be separated by newlines
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines, got %d", len(lines))
	}
}

func TestParseDOCX_InvalidZip(t *testing.T) {
	_, _, err := Parse([]byte("not a zip file"), "docx")
	if err == nil {
		t.Fatal("expected error for invalid ZIP data")
	}
}

func TestParseDOCX_EmptyDocument(t *testing.T) {
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body></w:body>
</w:document>`

	docx := buildDOCXWithContent(documentXML, "", "")
	text, _, err := Parse(docx, "docx")
	if err != nil {
		t.Fatalf("parseDOCX error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text for empty document, got %q", text)
	}
}

// =============================================================================
// PPTX Parsing Tests
// =============================================================================

func TestParsePPTX_ValidPresentation(t *testing.T) {
	pptx := buildMinimalPPTX([]string{"Slide One Title", "Slide Two Content"})

	text, meta, err := Parse(pptx, "pptx")
	if err != nil {
		t.Fatalf("parsePPTX error: %v", err)
	}

	if !strings.Contains(text, "Slide One Title") {
		t.Errorf("expected text to contain 'Slide One Title', got %q", text)
	}
	if !strings.Contains(text, "Slide Two Content") {
		t.Errorf("expected text to contain 'Slide Two Content', got %q", text)
	}
	if meta.PageCount != 2 {
		t.Errorf("expected page count 2, got %d", meta.PageCount)
	}
	if !strings.Contains(text, "--- Slide 1 ---") {
		t.Error("expected slide separator")
	}
}

func TestParsePPTX_InvalidZip(t *testing.T) {
	_, _, err := Parse([]byte("not a zip file"), "pptx")
	if err == nil {
		t.Fatal("expected error for invalid ZIP data")
	}
}

func TestParsePPTX_EmptyPresentation(t *testing.T) {
	// Create a PPTX with no slide files
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Add a non-slide file
	f, _ := w.Create("ppt/presentation.xml")
	f.Write([]byte(`<?xml version="1.0"?><p:presentation/>`))
	w.Close()

	text, meta, err := Parse(buf.Bytes(), "pptx")
	if err != nil {
		t.Fatalf("parsePPTX error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text for empty presentation, got %q", text)
	}
	if meta.PageCount != 0 {
		t.Errorf("expected 0 pages, got %d", meta.PageCount)
	}
}

func TestParsePPTX_SlideOrdering(t *testing.T) {
	// Slides should be sorted by filename (slide1, slide2, slide10 etc.)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Write slides out of order
	slide2XML := `<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Second slide</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`

	slide1XML := `<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>First slide</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`

	f2, _ := w.Create("ppt/slides/slide2.xml")
	f2.Write([]byte(slide2XML))
	f1, _ := w.Create("ppt/slides/slide1.xml")
	f1.Write([]byte(slide1XML))
	w.Close()

	text, _, err := Parse(buf.Bytes(), "pptx")
	if err != nil {
		t.Fatalf("parsePPTX error: %v", err)
	}

	// First slide content should appear before second
	idx1 := strings.Index(text, "First slide")
	idx2 := strings.Index(text, "Second slide")
	if idx1 == -1 || idx2 == -1 {
		t.Fatalf("expected both slides in text, got %q", text)
	}
	if idx1 > idx2 {
		t.Error("expected slide1 content before slide2 content")
	}
}

// =============================================================================
// Parse Dispatcher Tests
// =============================================================================

func TestParse_UnsupportedType(t *testing.T) {
	_, _, err := Parse([]byte("data"), "xlsx")
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported document type") {
		t.Errorf("expected 'unsupported document type' error, got: %v", err)
	}
}

func TestParse_RoutesToPDF(t *testing.T) {
	// Should fail with invalid PDF but prove the routing works
	_, _, err := Parse([]byte("not pdf"), "pdf")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a valid PDF") {
		t.Errorf("expected PDF validation error, got: %v", err)
	}
}

func TestParse_RoutesToDOCX(t *testing.T) {
	// Should fail with invalid ZIP but prove the routing works
	_, _, err := Parse([]byte("not docx"), "docx")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParse_RoutesToPPTX(t *testing.T) {
	// Should fail with invalid ZIP but prove the routing works
	_, _, err := Parse([]byte("not pptx"), "pptx")
	if err == nil {
		t.Fatal("expected error")
	}
}

// =============================================================================
// Test Helpers — Build Minimal Documents
// =============================================================================

func buildMinimalDOCX(text, title, author string) []byte {
	documentXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>%s</w:t></w:r></w:p>
</w:body>
</w:document>`, text)

	return buildDOCXWithContent(documentXML, title, author)
}

func buildDOCXWithContent(documentXML, title, author string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Add document.xml
	f, _ := w.Create("word/document.xml")
	f.Write([]byte(documentXML))

	// Add core properties if title or author provided
	if title != "" || author != "" {
		coreXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
                   xmlns:dc="http://purl.org/dc/elements/1.1/">
  <dc:title>%s</dc:title>
  <dc:creator>%s</dc:creator>
</cp:coreProperties>`, title, author)
		f2, _ := w.Create("docProps/core.xml")
		f2.Write([]byte(coreXML))
	}

	w.Close()
	return buf.Bytes()
}

func buildMinimalPPTX(slideTexts []string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	for i, text := range slideTexts {
		slideXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
        xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld>
<p:spTree>
<p:sp>
<p:txBody>
<a:p><a:r><a:t>%s</a:t></a:r></a:p>
</p:txBody>
</p:sp>
</p:spTree>
</p:cSld>
</p:sld>`, text)

		name := fmt.Sprintf("ppt/slides/slide%d.xml", i+1)
		f, _ := w.Create(name)
		f.Write([]byte(slideXML))
	}

	w.Close()
	return buf.Bytes()
}

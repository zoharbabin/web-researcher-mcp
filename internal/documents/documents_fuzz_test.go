package documents

import "testing"

// Fuzz targets for issue #476: PDF/DOCX/PPTX extraction parses fully
// untrusted, internet-sourced bytes, so a crafted input must return an error,
// never panic or hang. Seeds are drawn from the existing table-driven fixtures
// in documents_test.go and gopdf_eval_test.go.

func FuzzParsePDF(f *testing.F) {
	f.Add([]byte("%PDF-1.4\nsome content\n%%EOF"))
	f.Add([]byte(""))
	f.Add([]byte("This is not a PDF file at all"))
	f.Add([]byte("%PDF-1.4\ngarbage data without structure\n%%EOF"))
	f.Add([]byte("%PDF-1.4\n"))
	f.Add([]byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\nxref\n"))
	f.Add(make([]byte, 1024))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = Parse(data, "pdf")
	})
}

func FuzzParseDOCX(f *testing.F) {
	f.Add(buildMinimalDOCX("Hello from DOCX", "Test Title", "Test Author"))
	f.Add([]byte("not a zip file"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = Parse(data, "docx")
	})
}

func FuzzParsePPTX(f *testing.F) {
	f.Add(buildMinimalPPTX([]string{"Slide One Title", "Slide Two Content"}))
	f.Add([]byte("not a zip file"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = Parse(data, "pptx")
	})
}

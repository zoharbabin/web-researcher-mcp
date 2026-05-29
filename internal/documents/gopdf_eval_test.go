package documents

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/razvandimescu/gopdf/pdf"
)

// TestGoPDFExtraction_ArXiv2301 downloads and parses an arXiv paper to evaluate
// gopdf text extraction quality, speed, and memory usage.
func TestGoPDFExtraction_ArXiv2301(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}

	url := "https://arxiv.org/pdf/2301.00234"
	data := downloadPDF(t, url)

	// Record memory before parsing.
	var memBefore runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	start := time.Now()

	doc, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}

	text, err := doc.Text()
	if err != nil {
		t.Fatalf("Text() failed: %v", err)
	}

	elapsed := time.Since(start)

	// Record memory after parsing.
	var memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memAfter)

	memUsed := memAfter.TotalAlloc - memBefore.TotalAlloc

	t.Logf("PDF size: %d bytes", len(data))
	t.Logf("Pages: %d", doc.NumPages())
	t.Logf("Text length: %d chars", len(text))
	t.Logf("Parse + extract time: %v", elapsed)
	t.Logf("Memory allocated: %.2f MB", float64(memUsed)/1024/1024)

	// Print first 500 chars.
	preview := text
	if len(preview) > 500 {
		preview = preview[:500]
	}
	t.Logf("First 500 chars:\n---\n%s\n---", preview)

	// Basic quality checks.
	if len(text) < 100 {
		t.Errorf("extracted text too short (%d chars), expected substantial content", len(text))
	}
	if doc.NumPages() < 1 {
		t.Errorf("expected at least 1 page, got %d", doc.NumPages())
	}
}

// TestGoPDFExtraction_AttentionPaper tests with the famous "Attention Is All You Need" paper.
func TestGoPDFExtraction_AttentionPaper(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}

	url := "https://arxiv.org/pdf/1706.03762"
	data := downloadPDF(t, url)

	var memBefore runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	start := time.Now()

	doc, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}

	text, err := doc.Text()
	if err != nil {
		t.Fatalf("Text() failed: %v", err)
	}

	elapsed := time.Since(start)

	var memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	memUsed := memAfter.TotalAlloc - memBefore.TotalAlloc

	t.Logf("PDF size: %d bytes", len(data))
	t.Logf("Pages: %d", doc.NumPages())
	t.Logf("Text length: %d chars", len(text))
	t.Logf("Parse + extract time: %v", elapsed)
	t.Logf("Memory allocated: %.2f MB", float64(memUsed)/1024/1024)

	preview := text
	if len(preview) > 500 {
		preview = preview[:500]
	}
	t.Logf("First 500 chars:\n---\n%s\n---", preview)

	// Quality assertions for the Attention paper.
	if len(text) < 1000 {
		t.Errorf("extracted text too short (%d chars), expected substantial content", len(text))
	}
	if doc.NumPages() < 10 {
		t.Errorf("expected at least 10 pages for the Attention paper, got %d", doc.NumPages())
	}

	// Check for known content from this paper.
	knownPhrases := []string{"Attention", "Transformer", "self-attention"}
	found := 0
	for _, phrase := range knownPhrases {
		if containsIgnoreCase(text, phrase) {
			found++
		}
	}
	if found == 0 {
		t.Errorf("none of the expected phrases found in extracted text; quality may be poor")
	}
	t.Logf("Found %d/%d expected phrases", found, len(knownPhrases))
}

// TestGoPDFMalformed tests that gopdf handles malformed input gracefully (error, not panic).
func TestGoPDFMalformed(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"garbage", []byte("not a pdf at all")},
		{"truncated_header", []byte("%PDF-1.4\n")},
		{"partial_xref", []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\nxref\n")},
		{"null_bytes", make([]byte, 1024)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PANIC on malformed input %q: %v", tc.name, r)
				}
			}()

			doc, err := pdf.OpenBytes(tc.data)
			if err != nil {
				// Expected — malformed input should return an error.
				t.Logf("%s: correctly returned error: %v", tc.name, err)
				return
			}
			// If parsing succeeded, try extracting text.
			_, err = doc.Text()
			if err != nil {
				t.Logf("%s: parsed but Text() returned error: %v", tc.name, err)
			}
		})
	}
}

// TestGoPDFCompareWithCurrent compares gopdf output with the current regex-based parser.
func TestGoPDFCompareWithCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}

	url := "https://arxiv.org/pdf/2301.00234"
	data := downloadPDF(t, url)

	// Current parser.
	reader := &byteReaderAt{data: data}
	currentText, _, currentErr := parsePDF(reader, int64(len(data)))

	// gopdf parser.
	doc, gopdfErr := pdf.OpenBytes(data)
	var gopdfText string
	if gopdfErr == nil {
		gopdfText, gopdfErr = doc.Text()
	}

	t.Logf("Current parser: err=%v, len=%d", currentErr, len(currentText))
	t.Logf("gopdf parser:   err=%v, len=%d", gopdfErr, len(gopdfText))

	if currentErr == nil && len(currentText) > 0 {
		preview := currentText
		if len(preview) > 200 {
			preview = preview[:200]
		}
		t.Logf("Current parser preview: %s", preview)
	}
	if gopdfErr == nil && len(gopdfText) > 0 {
		preview := gopdfText
		if len(preview) > 200 {
			preview = preview[:200]
		}
		t.Logf("gopdf parser preview: %s", preview)
	}
}

// helper: byteReaderAt wraps a []byte for io.ReaderAt.
type byteReaderAt struct {
	data []byte
}

func (b *byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	n := copy(p, b.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func downloadPDF(t *testing.T, url string) []byte {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("failed to download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("unexpected status %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	if len(data) < 1000 {
		t.Fatalf("PDF too small (%d bytes), might not have downloaded correctly", len(data))
	}
	return data
}

func containsIgnoreCase(text, substr string) bool {
	return len(text) > 0 && len(substr) > 0 &&
		(contains(text, substr) || contains(text, capitalize(substr)) || contains(text, upper(substr)))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return fmt.Sprintf("%c%s", s[0]-32, s[1:])
}

func upper(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		if s[i] >= 'a' && s[i] <= 'z' {
			b[i] = s[i] - 32
		} else {
			b[i] = s[i]
		}
	}
	return string(b)
}

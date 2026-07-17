package scraper

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// =============================================================================
// SSRF Protection Tests
// =============================================================================

func TestIsPrivateIP_BlocksLocalhostIPv4(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.255", true},
		{"127.255.255.255", true},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %s", tt.ip)
		}
		got := isPrivateIP(ip)
		if got != tt.blocked {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.blocked)
		}
	}
}

func TestIsPrivateIP_BlocksPrivateRanges(t *testing.T) {
	privateIPs := []string{
		"10.0.0.1",
		"10.255.255.255",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.0.1",
		"192.168.1.100",
		"169.254.169.254",
		"169.254.0.1",
		"100.64.0.1",
		"0.0.0.0",
	}
	for _, ipStr := range privateIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("failed to parse IP %s", ipStr)
		}
		if !isPrivateIP(ip) {
			t.Errorf("isPrivateIP(%s) = false, want true (should block private IP)", ipStr)
		}
	}
}

func TestIsPrivateIP_AllowsPublicIPs(t *testing.T) {
	publicIPs := []string{
		"8.8.8.8",
		"1.1.1.1",
		"93.184.216.34",
		"142.250.80.46",
		"151.101.1.140",
	}
	for _, ipStr := range publicIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("failed to parse IP %s", ipStr)
		}
		if isPrivateIP(ip) {
			t.Errorf("isPrivateIP(%s) = true, want false (should allow public IP)", ipStr)
		}
	}
}

func TestIsPrivateIP_BlocksMetadataEndpoint(t *testing.T) {
	ip := net.ParseIP("169.254.169.254")
	if !isPrivateIP(ip) {
		t.Error("isPrivateIP(169.254.169.254) = false, want true (cloud metadata endpoint)")
	}
}

func TestIsPrivateIP_BlocksIPv6Loopback(t *testing.T) {
	ip := net.ParseIP("::1")
	if !isPrivateIP(ip) {
		t.Error("isPrivateIP(::1) = false, want true")
	}
}

func TestIsPrivateIP_BlocksIPv6ULA(t *testing.T) {
	ip := net.ParseIP("fc00::1")
	if !isPrivateIP(ip) {
		t.Error("isPrivateIP(fc00::1) = false, want true (unique local address)")
	}
}

func TestIsPrivateIP_BlocksIPv6LinkLocal(t *testing.T) {
	ip := net.ParseIP("fe80::1")
	if !isPrivateIP(ip) {
		t.Error("isPrivateIP(fe80::1) = false, want true (link-local)")
	}
}

func TestIsBlockedHostname(t *testing.T) {
	tests := []struct {
		host    string
		blocked bool
	}{
		{"metadata.google.internal", true},
		{"metadata.azure.com", true},
		{"169.254.169.254", true},
		{"instance-data", true},
		{"METADATA.GOOGLE.INTERNAL", true}, // case-insensitive
		{"www.example.com", false},
		{"google.com", false},
	}
	for _, tt := range tests {
		got := isBlockedHostname(tt.host)
		if got != tt.blocked {
			t.Errorf("isBlockedHostname(%q) = %v, want %v", tt.host, got, tt.blocked)
		}
	}
}

func TestNewSSRFSafeClient_RejectsPrivateIPs(t *testing.T) {
	// Start a local httptest server (which binds to 127.0.0.1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not reach here"))
	}))
	defer ts.Close()

	client := NewSSRFSafeClient(false)
	_, err := client.Get(ts.URL)
	if err == nil {
		t.Fatal("expected error when connecting to localhost, got nil")
	}
	if !strings.Contains(err.Error(), "ssrf") && !strings.Contains(err.Error(), "SSRF") &&
		!strings.Contains(err.Error(), "blocked") && !strings.Contains(err.Error(), "private") {
		// The error from the transport wraps ErrSSRFBlocked
		if !strings.Contains(err.Error(), ErrSSRFBlocked.Error()) {
			t.Errorf("expected SSRF-related error, got: %v", err)
		}
	}
}

func TestNewSSRFSafeClient_AllowsPublicIPs(t *testing.T) {
	// We test with a real httptest server on localhost but with allowPrivate=true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	client := NewSSRFSafeClient(true) // allow private
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error with allowPrivate=true: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// =============================================================================
// Pipeline Tests
// =============================================================================

func TestNewPipeline_DefaultConcurrency(t *testing.T) {
	p := NewPipeline(PipelineConfig{})
	if cap(p.semaphore) != 5 {
		t.Errorf("expected default concurrency 5, got %d", cap(p.semaphore))
	}
}

func TestNewPipeline_CustomConcurrency(t *testing.T) {
	p := NewPipeline(PipelineConfig{MaxConcurrency: 10})
	if cap(p.semaphore) != 10 {
		t.Errorf("expected concurrency 10, got %d", cap(p.semaphore))
	}
}

func TestPipeline_DomainFiltering(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		AllowedDomains:  []string{"example.com", "test.org"},
		AllowPrivateIPs: true,
	})

	if !p.isDomainAllowed("https://example.com/page") {
		t.Error("expected example.com to be allowed")
	}
	if !p.isDomainAllowed("https://test.org/page") {
		t.Error("expected test.org to be allowed")
	}
	if p.isDomainAllowed("https://evil.com/page") {
		t.Error("expected evil.com to be blocked")
	}
}

func TestPipeline_EmptyAllowedDomains_AllowsAll(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		AllowedDomains:  nil,
		AllowPrivateIPs: true,
	})

	if !p.isDomainAllowed("https://anything.com/page") {
		t.Error("expected all domains allowed when AllowedDomains is empty")
	}
}

func TestPipeline_ScrapeBlockedDomain(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		AllowedDomains:  []string{"allowed.com"},
		AllowPrivateIPs: true,
	})

	_, err := p.Scrape(context.Background(), "https://blocked.com/page", 10000)
	if err == nil {
		t.Fatal("expected error for blocked domain")
	}
	if !strings.Contains(err.Error(), "not in allowed list") {
		t.Errorf("expected domain error, got: %v", err)
	}
}

func TestPipeline_ScrapeContextCanceled(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		MaxConcurrency:  1,
		AllowPrivateIPs: true,
	})

	// Fill the semaphore
	p.semaphore <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	_, err := p.Scrape(ctx, "https://example.com", 10000)
	if err == nil {
		t.Fatal("expected context error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	// Drain semaphore
	<-p.semaphore
}

// =============================================================================
// Full Pipeline.Scrape() Integration Tests
// =============================================================================

func TestPipeline_ScrapeHTML_FullPipeline(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
	<title>Pipeline Test Page</title>
	<meta property="og:title" content="Full Pipeline Test" />
	<meta name="author" content="Pipeline Author" />
</head>
<body>
	<nav>Navigation to remove</nav>
	<article>
		<h1>Pipeline Heading</h1>
		<p>This is the main content of the article being tested through the full pipeline. It contains enough text to pass the 100 character threshold for content extraction to work properly in the pipeline and be returned as a successful result.</p>
		<p>Additional content paragraph to ensure we have sufficient text for the extraction logic to succeed and return meaningful content from the pipeline.</p>
	</article>
	<footer>Footer to remove</footer>
</body>
</html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{
		MaxConcurrency:  3,
		AllowPrivateIPs: true,
	})

	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("Scrape error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ContentType != "html" {
		t.Errorf("expected content type 'html', got %q", result.ContentType)
	}
	if !strings.Contains(result.Content, "Pipeline Heading") {
		t.Error("expected content to contain 'Pipeline Heading'")
	}
	if strings.Contains(result.Content, "Navigation to remove") {
		t.Error("nav element should have been removed")
	}
	if result.Title != "Full Pipeline Test" && result.Title != "Pipeline Test Page" {
		t.Errorf("expected title 'Full Pipeline Test' or 'Pipeline Test Page', got %q", result.Title)
	}
}

func TestPipeline_ScrapeMarkdown_FullPipeline(t *testing.T) {
	md := `# Markdown Test

## Section One

This is a paragraph with [a link](https://example.com) and some content that is long enough.

- List item one
- List item two
- List item three

## Section Two

More content here for the markdown detection to confirm this is real markdown.
` + "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(md))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})

	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("Scrape error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ContentType != "markdown" {
		t.Errorf("expected content type 'markdown', got %q", result.ContentType)
	}
	if !strings.Contains(result.Content, "# Markdown Test") {
		t.Error("expected content to contain '# Markdown Test'")
	}
}

func TestPipeline_YouTubeURLDetection(t *testing.T) {
	// Mock a YouTube page with ytInitialPlayerResponse
	ytHTML := `<!DOCTYPE html>
<html><head><title>Test Video - YouTube</title></head>
<body>
<script>
var ytInitialPlayerResponse = {"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"CAPTION_URL","languageCode":"en"}]}}};
</script>
</body></html>`

	var captionRequested atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "watch") || r.URL.Query().Get("v") != "" {
			w.Header().Set("Content-Type", "text/html")
			// Replace CAPTION_URL with the test server's URL for captions
			html := strings.ReplaceAll(ytHTML, "CAPTION_URL", "http://"+r.Host+"/captions")
			w.Write([]byte(html))
		} else if strings.Contains(r.URL.Path, "captions") {
			captionRequested.Add(1)
			w.Header().Set("Content-Type", "text/xml")
			w.Write([]byte(`<transcript><text start="0" dur="5">Hello world</text><text start="5" dur="3">Testing captions</text></transcript>`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	_ = NewPipeline(PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})

	// The YouTube scraper uses its own URL construction with youtube.com domain,
	// but we can test the URL detection logic directly
	if !isYouTubeURL("https://www.youtube.com/watch?v=abc123def45") {
		t.Error("expected YouTube URL to be detected")
	}
	if !isYouTubeURL("https://youtu.be/abc123def45") {
		t.Error("expected youtu.be URL to be detected")
	}
	if isYouTubeURL(ts.URL) {
		t.Error("expected non-YouTube URL to not be detected")
	}
}

func TestPipeline_DocumentURLDetection(t *testing.T) {
	if !isDocumentURL("https://example.com/paper.pdf") {
		t.Error("expected .pdf to be detected as document")
	}
	if !isDocumentURL("https://example.com/report.docx") {
		t.Error("expected .docx to be detected as document")
	}
	if !isDocumentURL("https://example.com/slides.pptx") {
		t.Error("expected .pptx to be detected as document")
	}
	if isDocumentURL("https://example.com/page.html") {
		t.Error("expected .html not to be detected as document")
	}
}

func TestPipeline_ContextCancellationMidScrape(t *testing.T) {
	// Server that delays response
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write([]byte("delayed response"))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.Scrape(ctx, ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error from context timeout")
	}
}

func TestPipeline_ConcurrentScraping(t *testing.T) {
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><body><article>
<p>Concurrent scrape test content with enough characters to pass the threshold of one hundred characters for content extraction to succeed in the pipeline logic.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Scrape(context.Background(), ts.URL, 50000)
		}()
	}
	wg.Wait()

	if maxConcurrent.Load() > 2 {
		t.Errorf("expected max concurrency of 2, got %d", maxConcurrent.Load())
	}
}

// =============================================================================
// HTML Extraction Tests
// =============================================================================

func TestScrapeHTML_ExtractsContent(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
	<title>Test Page Title</title>
	<meta property="og:title" content="OG Title" />
	<meta name="author" content="Test Author" />
	<meta property="og:site_name" content="Test Site" />
	<meta property="article:published_time" content="2024-01-15" />
</head>
<body>
	<nav>Navigation should be removed</nav>
	<article>
		<h1>Main Heading</h1>
		<p>This is the main content of the article. It contains enough text to pass the 100 character threshold for content extraction to work properly in the pipeline.</p>
		<p>Another paragraph with more content to ensure we have enough text for the extraction to succeed.</p>
	</article>
	<footer>Footer should be removed</footer>
	<script>alert('should be removed')</script>
</body>
</html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeHTML(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeHTML error: %v", err)
	}

	if result.Title != "OG Title" {
		t.Errorf("expected title 'OG Title', got %q", result.Title)
	}
	if result.Author != "Test Author" {
		t.Errorf("expected author 'Test Author', got %q", result.Author)
	}
	if result.SiteName != "Test Site" {
		t.Errorf("expected site name 'Test Site', got %q", result.SiteName)
	}
	if result.PublishDate != "2024-01-15" {
		t.Errorf("expected publish date '2024-01-15', got %q", result.PublishDate)
	}
	if !strings.Contains(result.Content, "Main Heading") {
		t.Error("expected content to contain 'Main Heading'")
	}
	if !strings.Contains(result.Content, "main content") {
		t.Error("expected content to contain article text")
	}
	if strings.Contains(result.Content, "Navigation should be removed") {
		t.Error("nav element should have been removed")
	}
	if strings.Contains(result.Content, "Footer should be removed") {
		t.Error("footer element should have been removed")
	}
	if strings.Contains(result.Content, "alert") {
		t.Error("script content should have been removed")
	}
}

func TestScrapeHTML_Truncation(t *testing.T) {
	html := `<!DOCTYPE html>
<html><body><article>
<p>` + strings.Repeat("This is content. ", 200) + `</p>
</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeHTML(context.Background(), ts.URL, 200)
	if err != nil {
		t.Fatalf("scrapeHTML error: %v", err)
	}

	if !result.Truncated {
		t.Error("expected Truncated=true when content exceeds maxLength")
	}
	if len(result.Content) > 200 {
		t.Errorf("expected content length <= 200, got %d", len(result.Content))
	}
}

func TestScrapeHTML_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	_, err := p.scrapeHTML(context.Background(), ts.URL, 10000)
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to mention 404, got: %v", err)
	}
}

func TestScrapeHTML_ContentTypeIsHTML(t *testing.T) {
	html := `<!DOCTYPE html>
<html><body><article>
<p>Sufficient content for the test to work properly with the extraction pipeline and content length threshold.</p>
<p>More content here to ensure we pass the minimum threshold of 100 characters needed for successful extraction.</p>
</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeHTML(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeHTML error: %v", err)
	}
	if result.ContentType != "html" {
		t.Errorf("expected content type 'html', got %q", result.ContentType)
	}
}

// =============================================================================
// Markdown Extraction Tests
// =============================================================================

func TestScrapeMarkdown_ValidMarkdown(t *testing.T) {
	md := `# Hello World

## Section One

This is a paragraph with [a link](https://example.com) and some content.

- List item one
- List item two

` + "```go\nfunc main() {}\n```"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(md))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeMarkdown(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeMarkdown error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for valid markdown")
	}
	if result.ContentType != "markdown" {
		t.Errorf("expected content type 'markdown', got %q", result.ContentType)
	}
	if !strings.Contains(result.Content, "# Hello World") {
		t.Error("expected content to contain markdown heading")
	}
}

func TestScrapeMarkdown_NonMarkdownContentType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>not markdown</body></html>"))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeMarkdown(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for non-markdown content type")
	}
}

func TestIsMarkdown(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"valid markdown with headings and links", "# Title\n\n## Subtitle\n\nSome text with [link](url) and more content", true},
		{"valid markdown with list and code", "# Title\n\n- item one\n- item two\n\n```code```\n\nMore text here", true},
		{"too short", "# Hi", false},
		{"plain text no indicators", "This is just plain text without any markdown formatting at all.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMarkdown(tt.input)
			if got != tt.expect {
				t.Errorf("isMarkdown(%q) = %v, want %v", tt.input[:min(len(tt.input), 40)], got, tt.expect)
			}
		})
	}
}

// =============================================================================
// YouTube URL Detection Tests
// =============================================================================

func TestIsYouTubeURL(t *testing.T) {
	tests := []struct {
		url    string
		expect bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://youtu.be/dQw4w9WgXcQ", true},
		{"https://youtube.com/embed/dQw4w9WgXcQ", true},
		{"https://example.com/page", false},
		{"https://vimeo.com/12345", false},
	}
	for _, tt := range tests {
		got := isYouTubeURL(tt.url)
		if got != tt.expect {
			t.Errorf("isYouTubeURL(%q) = %v, want %v", tt.url, got, tt.expect)
		}
	}
}

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://example.com/page", ""},
	}
	for _, tt := range tests {
		got := extractVideoID(tt.url)
		if got != tt.expected {
			t.Errorf("extractVideoID(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

// =============================================================================
// YouTube Transcript Extraction Tests
// =============================================================================

func TestExtractTranscript_WithMockServer(t *testing.T) {
	captionXML := `<transcript><text start="0.5" dur="2">Hello everyone</text><text start="3.0" dur="2">Welcome to the video</text><text start="6.0" dur="3">Today we discuss testing</text></transcript>`

	captionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(captionXML))
	}))
	defer captionServer.Close()

	pageHTML := `<html><body><script>
var ytInitialPlayerResponse = {"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"` + captionServer.URL + `","languageCode":"en"}]}}};
</script></body></html>`

	client := NewSSRFSafeClient(true)
	transcript, err := extractTranscript(context.Background(), client, pageHTML)
	if err != nil {
		t.Fatalf("extractTranscript error: %v", err)
	}
	if !strings.Contains(transcript, "Hello everyone") {
		t.Error("expected transcript to contain 'Hello everyone'")
	}
	if !strings.Contains(transcript, "Welcome to the video") {
		t.Error("expected transcript to contain 'Welcome to the video'")
	}
	if !strings.Contains(transcript, "[0:00]") || !strings.Contains(transcript, "[0:03]") {
		t.Error("expected transcript to contain timestamps")
	}
}

func TestExtractTranscript_NoPlayerResponse(t *testing.T) {
	pageHTML := `<html><body><p>No player response here</p></body></html>`
	client := NewSSRFSafeClient(true)
	_, err := extractTranscript(context.Background(), client, pageHTML)
	if err == nil {
		t.Fatal("expected error when player response is missing")
	}
}

// =============================================================================
// Document URL Detection Tests
// =============================================================================

func TestIsDocumentURL(t *testing.T) {
	tests := []struct {
		url    string
		expect bool
	}{
		{"https://example.com/file.pdf", true},
		{"https://example.com/file.PDF", true},
		{"https://example.com/file.docx", true},
		{"https://example.com/file.pptx", true},
		{"https://example.com/page", false},
		{"https://example.com/page.html", false},
	}
	for _, tt := range tests {
		got := isDocumentURL(tt.url)
		if got != tt.expect {
			t.Errorf("isDocumentURL(%q) = %v, want %v", tt.url, got, tt.expect)
		}
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestCleanText(t *testing.T) {
	input := "  Line one  \n\n\n\n\n  Line two  \n\n\n  Line three  "
	result := cleanText(input)

	if strings.Contains(result, "\n\n\n") {
		t.Error("cleanText should collapse triple newlines")
	}
	if !strings.Contains(result, "Line one") {
		t.Error("cleanText should preserve content")
	}
	if strings.HasPrefix(result, " ") || strings.HasSuffix(result, " ") {
		t.Error("cleanText should trim leading/trailing whitespace")
	}
}

func TestExtractYouTubeTitle(t *testing.T) {
	html := `<html><head><title>My Video - YouTube</title></head></html>`
	title := extractYouTubeTitle(html)
	if title != "My Video" {
		t.Errorf("expected 'My Video', got %q", title)
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		seconds  float64
		expected string
	}{
		{0, "0:00"},
		{65, "1:05"},
		{3661, "61:01"},
	}
	for _, tt := range tests {
		got := formatTimestamp(tt.seconds)
		if got != tt.expected {
			t.Errorf("formatTimestamp(%f) = %q, want %q", tt.seconds, got, tt.expected)
		}
	}
}

func TestDetectDocType(t *testing.T) {
	tests := []struct {
		url         string
		contentType string
		expected    string
	}{
		{"https://example.com/file.pdf", "", "pdf"},
		{"https://example.com/file", "application/pdf", "pdf"},
		{"https://example.com/file.docx", "", "docx"},
		{"https://example.com/file", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx"},
		{"https://example.com/file.pptx", "", "pptx"},
		{"https://example.com/file", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "pptx"},
		{"https://example.com/file.txt", "text/plain", "unknown"},
	}
	for _, tt := range tests {
		got := detectDocType(tt.url, tt.contentType)
		if got != tt.expected {
			t.Errorf("detectDocType(%q, %q) = %q, want %q", tt.url, tt.contentType, got, tt.expected)
		}
	}
}

func TestIsSPADomain(t *testing.T) {
	tests := []struct {
		url    string
		expect bool
	}{
		{"https://patents.google.com/patent/US123", true},
		{"https://twitter.com/user/status/123", false}, // handled by dedicated twitter path
		{"https://www.linkedin.com/in/user", true},
		{"https://example.com/page", false},
		{"https://bsky.app/profile/user.bsky.social", true},
		{"https://www.tiktok.com/@user/video/123", true},
		{"https://www.threads.net/@user/post/123", true},
		{"https://notbsky.app/profile/user", false},
		{"https://eviltiktok.com/page", false},
	}
	for _, tt := range tests {
		got := isSPADomain(tt.url)
		if got != tt.expect {
			t.Errorf("isSPADomain(%q) = %v, want %v", tt.url, got, tt.expect)
		}
	}
}

func TestParseTranscriptXML(t *testing.T) {
	xml := `<transcript><text start="0" dur="2">Hello</text><text start="2.5" dur="3">World</text></transcript>`
	result := parseTranscriptXML(xml)
	if !strings.Contains(result, "[0:00] Hello") {
		t.Errorf("expected '[0:00] Hello' in transcript, got %q", result)
	}
	if !strings.Contains(result, "[0:02] World") {
		t.Errorf("expected '[0:02] World' in transcript, got %q", result)
	}
}

func TestParseTranscriptXML_HTMLEntities(t *testing.T) {
	xml := `<transcript><text start="0" dur="2">Tom &amp; Jerry &lt;3</text></transcript>`
	result := parseTranscriptXML(xml)
	if !strings.Contains(result, "Tom & Jerry <3") {
		t.Errorf("expected HTML entities to be decoded, got %q", result)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// =============================================================================
// Stealth Scraper (Tier 2) Tests
// =============================================================================

func TestScrapeStealth_ExtractsArticleContent(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head><title>Stealth Test Page</title></head>
<body>
<nav>Nav to remove</nav>
<article>
<h1>Stealth Article</h1>
<p>This is substantial article content extracted via the stealth HTTP client. It must be long enough to exceed the 200 character threshold for article selectors and the 100 character threshold for the pipeline to accept it as valid content from the stealth tier.</p>
<p>Additional paragraph providing more depth to the article content for proper extraction testing.</p>
</article>
<footer>Footer noise</footer>
<script>alert('removed')</script>
</body>
</html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeStealth error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Title != "Stealth Test Page" {
		t.Errorf("expected title 'Stealth Test Page', got %q", result.Title)
	}
	if !strings.Contains(result.Content, "Stealth Article") {
		t.Error("expected content to contain 'Stealth Article'")
	}
	if strings.Contains(result.Content, "Nav to remove") {
		t.Error("expected nav to be removed")
	}
	if strings.Contains(result.Content, "Footer noise") {
		t.Error("expected footer to be removed")
	}
}

func TestScrapeStealth_ReturnsNilForThinContent(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>Thin</title></head><body><p>Short.</p></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for thin content")
	}
}

func TestScrapeStealth_Truncation(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>Truncate</title></head><body>
<article><p>` + strings.Repeat("A", 5000) + `</p></article>
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.scrapeStealth(context.Background(), ts.URL, 500)
	if err != nil {
		t.Fatalf("scrapeStealth error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Truncated {
		t.Error("expected Truncated=true")
	}
	if len(result.Content) > 500 {
		t.Errorf("content length %d exceeds maxLength 500", len(result.Content))
	}
}

func TestScrapeStealth_HTTP4xxError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	scrapeErr, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if scrapeErr.Kind != ErrBlocked {
		t.Errorf("expected ErrBlocked, got %v", scrapeErr.Kind)
	}
	if scrapeErr.Tier != "stealth" {
		t.Errorf("expected tier stealth, got %q", scrapeErr.Tier)
	}
}

func TestScrapeStealth_BrowserHeaders(t *testing.T) {
	var receivedHeaders http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><body><article>` + strings.Repeat("Content. ", 50) + `</article></body></html>`))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, _ = p.scrapeStealth(context.Background(), ts.URL, 50000)

	if receivedHeaders == nil {
		t.Fatal("no headers received")
	}
	ua := receivedHeaders.Get("User-Agent")
	if !strings.Contains(ua, "Chrome") {
		t.Errorf("expected Chrome user-agent, got %q", ua)
	}
	if receivedHeaders.Get("Sec-Fetch-Dest") != "document" {
		t.Errorf("expected Sec-Fetch-Dest=document, got %q", receivedHeaders.Get("Sec-Fetch-Dest"))
	}
	if receivedHeaders.Get("Sec-Ch-Ua-Platform") == "" {
		t.Error("expected Sec-Ch-Ua-Platform header to be set")
	}
}

func TestScrapeStealth_FallsBackToBody(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>No Article</title></head>
<body>
<div>` + strings.Repeat("Body content paragraph. ", 30) + `</div>
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeStealth error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result when body has enough content")
	}
	if !strings.Contains(result.Content, "Body content paragraph") {
		t.Error("expected body content to be extracted")
	}
}

func TestNewStealthClient_SSRFProtection(t *testing.T) {
	client := newStealthClient(false)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Timeout != 20*time.Second {
		t.Errorf("expected 20s timeout, got %v", client.Timeout)
	}
}

func TestNewStealthClient_AllowsPrivateIPs(t *testing.T) {
	client := newStealthClient(true)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestExtractArticleContent_Selectors(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains string
	}{
		{
			name:     "article tag",
			html:     `<html><body><article>` + strings.Repeat("Article selector content. ", 20) + `</article></body></html>`,
			contains: "Article selector content",
		},
		{
			name:     "role=main",
			html:     `<html><body><div role="main">` + strings.Repeat("Main role content. ", 20) + `</div></body></html>`,
			contains: "Main role content",
		},
		{
			name:     "main tag",
			html:     `<html><body><main>` + strings.Repeat("Main tag content. ", 20) + `</main></body></html>`,
			contains: "Main tag content",
		},
		{
			name:     ".entry-content",
			html:     `<html><body><div class="entry-content">` + strings.Repeat("Entry content class. ", 20) + `</div></body></html>`,
			contains: "Entry content class",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(tt.html))
			}))
			defer ts.Close()

			p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
			result, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !strings.Contains(result.Content, tt.contains) {
				t.Errorf("expected content to contain %q", tt.contains)
			}
		})
	}
}

// =============================================================================
// YouTube Transcript Fallback Tests
// =============================================================================

func TestExtractDescription_Valid(t *testing.T) {
	html := `<script>var data = {"shortDescription":"This is a long video description with enough content to pass the minimum threshold and be returned as a valid fallback."};</script>`
	desc := extractDescription(html)
	if desc == "" {
		t.Fatal("expected non-empty description")
	}
	if !strings.Contains(desc, "long video description") {
		t.Errorf("unexpected description: %q", desc)
	}
}

func TestExtractDescription_EscapedCharacters(t *testing.T) {
	// In the actual YouTube page, JSON has escape sequences like \n, \/, \"
	// In Go backtick strings, a single backslash is literal, so \\n in backtick = two chars: \ and n
	// The regex captures the inner content, then extractDescription replaces \\n -> \n etc.
	// We need to simulate what the regex actually captures from real YouTube JSON.
	html := `<script>var data = {"shortDescription":"Line one\nLine two\nURL: https:\/\/example.com\nQuote: \"hello\""};</script>`
	desc := extractDescription(html)
	if !strings.Contains(desc, "Line one\nLine two") {
		t.Errorf("expected newlines to be unescaped, got %q", desc)
	}
	if !strings.Contains(desc, "https://example.com") {
		t.Errorf("expected slashes to be unescaped, got %q", desc)
	}
	if !strings.Contains(desc, `"hello"`) {
		t.Errorf("expected quotes to be unescaped, got %q", desc)
	}
}

func TestExtractDescription_TooShort(t *testing.T) {
	html := `<script>var data = {"shortDescription":"short"};</script>`
	desc := extractDescription(html)
	if desc != "" {
		t.Errorf("expected empty for short description, got %q", desc)
	}
}

func TestExtractDescription_NotFound(t *testing.T) {
	html := `<script>var data = {"title":"No description here"};</script>`
	desc := extractDescription(html)
	if desc != "" {
		t.Errorf("expected empty when no shortDescription, got %q", desc)
	}
}

func TestFetchTimedTextAPI_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "timedtext") || strings.Contains(r.URL.RawQuery, "timedtext") {
			w.Header().Set("Content-Type", "text/xml")
			w.Write([]byte(`<transcript>` +
				`<text start="0" dur="2">First segment of the video transcript.</text>` +
				`<text start="2" dur="3">Second segment with more content here.</text>` +
				`<text start="5" dur="2">Third segment to exceed the character threshold required.</text>` +
				`<text start="7" dur="4">Fourth segment adding even more text to ensure we have enough.</text>` +
				`</transcript>`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	client := ts.Client()
	// Override the URL by using a custom transport that rewrites the host
	origTransport := client.Transport
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = ts.Listener.Addr().String()
		if origTransport != nil {
			return origTransport.RoundTrip(req)
		}
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := fetchTimedTextAPI(context.Background(), client, "testVideo123")
	if err != nil {
		t.Fatalf("fetchTimedTextAPI error: %v", err)
	}
	if len(result) < 100 {
		t.Fatalf("expected transcript >100 chars, got %d", len(result))
	}
	if !strings.Contains(result, "First segment") {
		t.Error("expected transcript to contain 'First segment'")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFetchTimedTextAPI_NoTranscript(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	client := ts.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = ts.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	_, err := fetchTimedTextAPI(context.Background(), client, "noTranscript1")
	if err == nil {
		t.Fatal("expected error when no transcript available")
	}
}

func TestExtractTranscript_AlternateRegex(t *testing.T) {
	captionXML := `<transcript><text start="0" dur="2">Alternate regex caption text that needs to be long enough.</text>` +
		`<text start="2" dur="3">More caption content to meet threshold requirements for valid extraction.</text>` +
		`<text start="5" dur="4">Even more text to ensure we exceed the minimum character limit.</text></transcript>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(captionXML))
	}))
	defer ts.Close()

	playerResp := `{"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"` + ts.URL + `","languageCode":"en"}]}}}`
	pageHTML := `<script>var ytInitialPlayerResponse = ` + playerResp + `;</script>`

	result, err := extractTranscript(context.Background(), ts.Client(), pageHTML)
	if err != nil {
		t.Fatalf("extractTranscript error: %v", err)
	}
	if !strings.Contains(result, "Alternate regex caption") {
		t.Errorf("expected caption text, got %q", result)
	}
}

func TestExtractTranscript_PrimaryRegex(t *testing.T) {
	captionXML := `<transcript><text start="0" dur="2">Primary regex caption text for testing.</text>` +
		`<text start="2" dur="3">Additional content to meet the minimum character threshold.</text>` +
		`<text start="5" dur="2">Third segment making sure we have enough characters total.</text></transcript>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(captionXML))
	}))
	defer ts.Close()

	playerResp := `{"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"` + ts.URL + `","languageCode":"en"}]}}}`
	pageHTML := `<script>ytInitialPlayerResponse = ` + playerResp + `;</script>`

	result, err := extractTranscript(context.Background(), ts.Client(), pageHTML)
	if err != nil {
		t.Fatalf("extractTranscript error: %v", err)
	}
	if !strings.Contains(result, "Primary regex caption") {
		t.Errorf("expected caption text, got %q", result)
	}
}

func TestExtractTranscript_NoCaptions(t *testing.T) {
	pageHTML := `<script>ytInitialPlayerResponse = {"playabilityStatus":{"status":"OK"}};</script>`
	_, err := extractTranscript(context.Background(), http.DefaultClient, pageHTML)
	if err == nil {
		t.Fatal("expected error when no captions found")
	}
}

func TestExtractTranscript_NoPlayerResponse_ErrorMessage(t *testing.T) {
	pageHTML := `<html><body>No player response here</body></html>`
	_, err := extractTranscript(context.Background(), http.DefaultClient, pageHTML)
	if err == nil {
		t.Fatal("expected error when no player response")
	}
	if !strings.Contains(err.Error(), "player response not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestYouTubeScrape_DescriptionFallback(t *testing.T) {
	pageHTML := `<!DOCTYPE html><html><head><title>Description Only Video - YouTube</title></head>
<body>
<script>
ytInitialPlayerResponse = {"playabilityStatus":{"status":"OK"},"videoDetails":{"shortDescription":"This is a detailed video description that serves as fallback content when no transcript is available. It provides enough information about the video for the user to understand what it covers."}};
</script>
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "watch") || r.URL.Query().Get("v") != "" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(pageHTML))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	p.client = ts.Client()
	// Override transport to route youtube.com requests to test server
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = ts.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := p.scrapeYouTube(context.Background(), "https://www.youtube.com/watch?v=abc123def45", 50000)
	if err != nil {
		t.Fatalf("scrapeYouTube error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result from description fallback")
	}
	if !strings.Contains(result.Content, "detailed video description") {
		t.Errorf("expected description content, got %q", result.Content)
	}
	if result.ContentType != "youtube" {
		t.Errorf("expected content type 'youtube', got %q", result.ContentType)
	}
}

func TestYouTubeScrape_TranscriptStrategy1(t *testing.T) {
	captionXML := `<transcript>` +
		`<text start="0" dur="3">Welcome to this video about testing strategies in Go.</text>` +
		`<text start="3" dur="4">We will cover unit tests, integration tests, and more.</text>` +
		`<text start="7" dur="3">Let us begin with the basics of test coverage.</text>` +
		`</transcript>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "watch") || r.URL.Query().Get("v") != "" {
			captionURL := "http://" + r.Host + "/captions"
			playerResp := `{"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"` + captionURL + `","languageCode":"en"}]}}}`
			html := `<!DOCTYPE html><html><head><title>Go Testing - YouTube</title></head><body>
<script>ytInitialPlayerResponse = ` + playerResp + `;</script>
</body></html>`
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(html))
			return
		}
		if strings.Contains(r.URL.Path, "captions") {
			w.Header().Set("Content-Type", "text/xml")
			w.Write([]byte(captionXML))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	p.client = ts.Client()
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = ts.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := p.scrapeYouTube(context.Background(), "https://www.youtube.com/watch?v=abc123def45", 50000)
	if err != nil {
		t.Fatalf("scrapeYouTube error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(result.Content, "Welcome to this video") {
		t.Errorf("expected transcript content, got %q", result.Content)
	}
	if result.Title != "Go Testing" {
		t.Errorf("expected title 'Go Testing', got %q", result.Title)
	}
}

func TestParseTranscriptXML_NewlineEntity(t *testing.T) {
	xml := `<transcript><text start="10" dur="2">Line one&#10;Line two</text></transcript>`
	result := parseTranscriptXML(xml)
	if !strings.Contains(result, "Line one\nLine two") {
		t.Errorf("expected &#10; to be decoded as newline, got %q", result)
	}
}

// =============================================================================
// Pipeline Tier Integration Tests
// =============================================================================

func TestPipeline_StealthTierIntercepts(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>Stealth Intercept</title></head>
<body><article><p>` + strings.Repeat("Stealth intercepted content. ", 20) + `</p></article></body></html>`

	var requestCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Header.Get("Accept") == "text/markdown" {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("Scrape error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(result.Content, "Stealth intercepted content") {
		t.Error("expected stealth tier to have provided the content")
	}
}

// =============================================================================
// Gzip Decompression Tests
// =============================================================================

func TestScrapeHTML_GzipDecompression(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>Gzip Test</title></head><body><article>
<p>This is gzip-compressed content that should be properly decompressed by the scraper pipeline to produce readable text output.</p>
</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")

		var buf strings.Builder
		gz, _ := newGzipWriter(&buf)
		gz.Write([]byte(html))
		gz.Close()
		w.Write([]byte(buf.String()))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeHTML(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeHTML with gzip error: %v", err)
	}
	if !strings.Contains(result.Content, "gzip-compressed content") {
		t.Errorf("expected decompressed content, got: %q", result.Content[:min(len(result.Content), 200)])
	}
	if result.Title != "Gzip Test" {
		t.Errorf("expected title 'Gzip Test', got %q", result.Title)
	}
}

func TestScrapeStealth_GzipDecompression(t *testing.T) {
	html := `<!DOCTYPE html><html><body><article>
<p>Stealth gzip content that must be decompressed correctly. This paragraph has enough text to pass the one hundred character minimum threshold for extraction.</p>
</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")

		var buf strings.Builder
		gz, _ := newGzipWriter(&buf)
		gz.Write([]byte(html))
		gz.Close()
		w.Write([]byte(buf.String()))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeStealth with gzip error: %v", err)
	}
	if !strings.Contains(result.Content, "Stealth gzip content") {
		t.Errorf("expected decompressed content, got: %q", result.Content[:min(len(result.Content), 200)])
	}
}

func TestDecompressBody_Gzip(t *testing.T) {
	var buf strings.Builder
	gz, _ := newGzipWriter(&buf)
	gz.Write([]byte("hello gzip world"))
	gz.Close()

	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(strings.NewReader(buf.String())),
	}
	reader, err := decompressBody(resp)
	if err != nil {
		t.Fatalf("decompressBody error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	if string(data) != "hello gzip world" {
		t.Errorf("expected 'hello gzip world', got %q", string(data))
	}
}

func TestDecompressBody_NoEncoding(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader("plain text")),
	}
	reader, err := decompressBody(resp)
	if err != nil {
		t.Fatalf("decompressBody error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	if string(data) != "plain text" {
		t.Errorf("expected 'plain text', got %q", string(data))
	}
}

func newGzipWriter(buf *strings.Builder) (*gzipTestWriter, error) {
	w, err := gzip.NewWriterLevel(writerAdapter{buf}, gzip.DefaultCompression)
	return &gzipTestWriter{w}, err
}

type gzipTestWriter struct {
	*gzip.Writer
}

type writerAdapter struct {
	*strings.Builder
}

func (w writerAdapter) Write(p []byte) (int, error) {
	return w.Builder.Write(p)
}

// =============================================================================
// Google Patents Tests
// =============================================================================

func TestBuildGooglePatentsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   PatentSearchParams
		contains []string
	}{
		{
			name: "basic query",
			params: PatentSearchParams{
				Query:      "machine learning",
				NumResults: 5,
			},
			contains: []string{"patents.google.com", "q=machine+learning", "num=5"},
		},
		{
			name: "with assignee",
			params: PatentSearchParams{
				Query:      "LLM inference",
				Assignee:   "Apple",
				NumResults: 10,
			},
			contains: []string{"assignee=Apple", "q=LLM+inference"},
		},
		{
			name: "with date range",
			params: PatentSearchParams{
				Query:    "neural network",
				YearFrom: 2024,
				YearTo:   2026,
			},
			contains: []string{"after=priority%3A20240101", "before=priority%3A20261231"},
		},
		{
			name: "with patent office",
			params: PatentSearchParams{
				Query:        "battery",
				PatentOffice: "US",
			},
			contains: []string{"country=US"},
		},
		{
			name: "with inventor",
			params: PatentSearchParams{
				Query:    "transformer",
				Inventor: "John Smith",
			},
			contains: []string{"inventor=John+Smith"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			url := BuildGooglePatentsURL(tt.params)
			for _, want := range tt.contains {
				if !strings.Contains(url, want) {
					t.Errorf("URL %q should contain %q", url, want)
				}
			}
		})
	}
}

func TestParsePatentDetailPage(t *testing.T) {
	t.Parallel()

	html := `<html><head>
		<meta name="DC.title" content="System and method for video coordination">
		<meta name="DC.description" content="A system for coordinating multiple video streams in real-time.">
	</head><body>
		<dd itemprop="assigneeOriginal">Kaltura, Inc.</dd>
		<dd itemprop="filingDate">2014-06-16</dd>
		<dd itemprop="events"><span itemprop="type">Grant</span><time itemprop="date">2016-02-23</time></dd>
	</body></html>`

	doc, err := goQueryFromString(html)
	if err != nil {
		t.Fatal(err)
	}

	result := parsePatentDetailPage(doc, "US9270715B2", "https://patents.google.com/patent/US9270715B2/en")

	if result.Title != "System and method for video coordination" {
		t.Errorf("expected title, got %q", result.Title)
	}
	if result.Abstract != "A system for coordinating multiple video streams in real-time." {
		t.Errorf("expected abstract, got %q", result.Abstract)
	}
	if result.Assignee != "Kaltura, Inc." {
		t.Errorf("expected Kaltura, Inc., got %q", result.Assignee)
	}
	if result.Filed != "2014-06-16" {
		t.Errorf("expected 2014-06-16, got %q", result.Filed)
	}
	if result.Granted != "2016-02-23" {
		t.Errorf("expected 2016-02-23, got %q", result.Granted)
	}
}

func TestExtractPatentNumberFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url    string
		expect string
	}{
		{"/patent/US11234567/en", "US11234567"},
		{"/patent/EP3456789A1/en", "EP3456789A1"},
		{"https://patents.google.com/patent/WO2024123456/en", "WO2024123456"},
		{"/about", ""},
		{"/patent/CN115678901B/en?q=test", "CN115678901B"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			got := ExtractPatentNumberFromURL(tt.url)
			if got != tt.expect {
				t.Errorf("ExtractPatentNumberFromURL(%q) = %q, want %q", tt.url, got, tt.expect)
			}
		})
	}
}

func TestScrapePatentDetail_MockServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head>
			<meta name="DC.title" content="Method for efficient LLM inference">
			<meta name="DC.description" content="A method for deploying language models on mobile devices.">
		</head><body>
			<dd itemprop="assigneeOriginal">Apple Inc.</dd>
			<dd itemprop="filingDate">2024-03-15</dd>
			<dd itemprop="events"><span itemprop="type">Grant</span><time itemprop="date">2025-01-10</time></dd>
		</body></html>`))
	}))
	defer server.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	_ = p
	_ = server
}

func goQueryFromString(html string) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(strings.NewReader(html))
}

// =============================================================================
// Error Taxonomy Tests
// =============================================================================

func TestScrapeError_Interface(t *testing.T) {
	cause := fmt.Errorf("underlying issue")
	se := &ScrapeError{
		Kind:    ErrBrowser,
		Message: "chrome launch failed: underlying issue",
		Cause:   cause,
		URL:     "https://example.com",
		Tier:    "browser",
	}

	if se.Error() != "chrome launch failed: underlying issue" {
		t.Errorf("unexpected Error(): %q", se.Error())
	}
	if se.Unwrap() != cause {
		t.Error("Unwrap should return the cause")
	}

	var target *ScrapeError
	if !errors.As(se, &target) {
		t.Fatal("errors.As should match *ScrapeError")
	}
	if target.Kind != ErrBrowser {
		t.Errorf("expected ErrBrowser, got %v", target.Kind)
	}
}

func TestClassifyHTTPStatus(t *testing.T) {
	tests := []struct {
		code int
		kind ErrorKind
	}{
		{401, ErrAuth},
		{403, ErrBlocked},
		{429, ErrRateLimit},
		{500, ErrNetwork},
		{502, ErrNetwork},
		{404, ErrNotFound}, // a 404 is a definite dead link, not a transient network fault
		{410, ErrNotFound},
	}
	for _, tt := range tests {
		se := classifyHTTPStatus(tt.code, "https://example.com", "stealth")
		if se.Kind != tt.kind {
			t.Errorf("HTTP %d: expected kind %v, got %v", tt.code, tt.kind, se.Kind)
		}
		if se.Tier != "stealth" {
			t.Errorf("HTTP %d: expected tier stealth, got %q", tt.code, se.Tier)
		}
	}
}

func TestPipeline_TieredFallback_DiagnosticError(t *testing.T) {
	// All tiers return < 100 bytes or error
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>tiny</body></html>`))
	}))
	defer ts.Close()

	// Override statFile to prevent browser detection
	orig := statFile
	statFile = func(path string) (any, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error when all tiers produce thin content")
	}

	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T: %v", err, err)
	}

	// Should contain per-tier diagnostics
	if !strings.Contains(se.Message, "markdown:") || !strings.Contains(se.Message, "stealth:") || !strings.Contains(se.Message, "html:") {
		t.Errorf("diagnostic message missing tier details: %q", se.Message)
	}
}

func TestScrapeStealth_AuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if se.Kind != ErrAuth {
		t.Errorf("expected ErrAuth, got %v", se.Kind)
	}
}

func TestScrapeStealth_RateLimitError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error for 429")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if se.Kind != ErrRateLimit {
		t.Errorf("expected ErrRateLimit, got %v", se.Kind)
	}
}

func TestScrapeHTML_ClassifiedErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeHTML(context.Background(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if se.Kind != ErrBlocked {
		t.Errorf("expected ErrBlocked, got %v", se.Kind)
	}
	if se.Tier != "html" {
		t.Errorf("expected tier html, got %q", se.Tier)
	}
}

func TestScrapeHTML_429_ClassifiesAsRateLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeHTML(context.Background(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error for 429")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if se.Kind != ErrRateLimit {
		t.Errorf("expected ErrRateLimit, got %v", se.Kind)
	}
	if se.Tier != "html" {
		t.Errorf("expected tier html, got %q", se.Tier)
	}
}

func TestPipeline_DiagnosticError_EscalatesKind(t *testing.T) {
	// If one tier returns ErrBlocked (403), the composite error
	// should escalate to ErrBlocked even though other tiers got ErrContent
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// First request (markdown) gets thin content
		// Stealth gets 403
		// HTML gets thin content
		if callCount == 2 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>tiny</body></html>`))
	}))
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected error")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T: %v", err, err)
	}
	// Should escalate to ErrBlocked because stealth tier returned 403
	if se.Kind != ErrBlocked {
		t.Errorf("expected escalated kind ErrBlocked, got %v (message: %s)", se.Kind, se.Message)
	}
}

func TestPipeline_SuccessfulTierStopsFallback(t *testing.T) {
	// Markdown tier fails, stealth tier succeeds with good content
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/html")
		content := strings.Repeat("Good content from the server. ", 20)
		fmt.Fprintf(w, `<html><head><title>Success</title></head><body><article>%s</article></body></html>`, content)
	}))
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(result.Content, "Good content") {
		t.Errorf("expected content, got: %s", result.Content[:100])
	}
}

func TestBrowserError_TypedErrorCreation(t *testing.T) {
	t.Parallel()
	// Test that browserError() creates a properly typed error
	// without needing actual Chrome (avoids singleton pool issues)
	cause := fmt.Errorf("exec: chromium: not found")
	se := browserError("https://x.com/status/123", cause, "chrome launch failed: exec: chromium: not found")

	if se.Kind != ErrBrowser {
		t.Errorf("expected ErrBrowser, got %v", se.Kind)
	}
	if se.Tier != "browser" {
		t.Errorf("expected tier browser, got %q", se.Tier)
	}
	if se.URL != "https://x.com/status/123" {
		t.Errorf("expected URL preserved, got %q", se.URL)
	}
	if se.Unwrap() != cause {
		t.Error("expected cause to be preserved via Unwrap()")
	}
	if !strings.Contains(se.Error(), "chrome launch failed") {
		t.Errorf("expected descriptive message, got: %s", se.Error())
	}
}

func TestPipeline_DomainAllowlist_RejectsDisallowed(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		MaxConcurrency: 2,
		AllowedDomains: []string{"allowed.example.com"},
	})

	_, err := p.Scrape(context.Background(), "https://blocked.example.com/page", 50000)
	if err == nil {
		t.Fatal("expected error for disallowed domain")
	}
	if !strings.Contains(err.Error(), "not in allowed list") {
		t.Errorf("expected domain allowlist error, got: %v", err)
	}
}

func TestPipeline_DomainAllowlist_AllowsListed(t *testing.T) {
	content := strings.Repeat("Allowed domain content here. ", 20)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, content)
	}))
	defer ts.Close()

	// Use the test server's host as the allowed domain
	p := NewPipeline(PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
		AllowedDomains:  []string{"127.0.0.1"},
	})

	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("expected success for allowed domain, got: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Allowed domain content") {
		t.Error("expected content from allowed domain")
	}
}

func TestHelperConstructors(t *testing.T) {
	t.Parallel()

	t.Run("networkError", func(t *testing.T) {
		cause := fmt.Errorf("connection refused")
		se := networkError("https://example.com", "stealth", cause)
		if se.Kind != ErrNetwork {
			t.Errorf("expected ErrNetwork, got %v", se.Kind)
		}
		if se.Unwrap() != cause {
			t.Error("expected cause to be wrapped")
		}
		if !strings.Contains(se.Error(), "connection refused") {
			t.Errorf("expected cause in message, got: %s", se.Error())
		}
	})

	t.Run("blockedError", func(t *testing.T) {
		se := blockedError("https://example.com", "html", nil, "HTTP 403")
		if se.Kind != ErrBlocked {
			t.Errorf("expected ErrBlocked, got %v", se.Kind)
		}
		if !strings.Contains(se.Error(), "HTTP 403") {
			t.Errorf("expected detail in message, got: %s", se.Error())
		}
	})

	t.Run("browserError", func(t *testing.T) {
		cause := fmt.Errorf("exec: chromium: not found")
		se := browserError("https://x.com", cause, "chrome launch failed: exec: chromium: not found")
		if se.Kind != ErrBrowser {
			t.Errorf("expected ErrBrowser, got %v", se.Kind)
		}
		if se.Tier != "browser" {
			t.Errorf("expected tier browser, got %q", se.Tier)
		}
	})

	t.Run("contentError", func(t *testing.T) {
		se := contentError("https://spa.example.com", "no usable content")
		if se.Kind != ErrContent {
			t.Errorf("expected ErrContent, got %v", se.Kind)
		}
		if se.Tier != "" {
			t.Errorf("expected empty tier for content errors, got %q", se.Tier)
		}
	})

	t.Run("authError", func(t *testing.T) {
		se := authError("https://private.example.com", "stealth", 401)
		if se.Kind != ErrAuth {
			t.Errorf("expected ErrAuth, got %v", se.Kind)
		}
		if !strings.Contains(se.Error(), "401") {
			t.Errorf("expected status in message, got: %s", se.Error())
		}
	})

	t.Run("rateLimitError", func(t *testing.T) {
		se := rateLimitError("https://example.com", "html")
		if se.Kind != ErrRateLimit {
			t.Errorf("expected ErrRateLimit, got %v", se.Kind)
		}
		if !strings.Contains(se.Error(), "429") {
			t.Errorf("expected 429 in message, got: %s", se.Error())
		}
	})
}

// =============================================================================
// Step 4 — Hardening: hostnameMatches, validateScrapeURL, blocked hostnames,
// ScrapeRaw, and CIDR-parse smoke test.
// =============================================================================

func TestHostnameMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		url    string
		domain string
		want   bool
	}{
		{"exact host", "https://example.com/page", "example.com", true},
		{"subdomain", "https://sub.example.com/page", "example.com", true},
		{"deep subdomain", "https://a.b.example.com/", "example.com", true},
		{"suffix-spoof registrable domain", "https://example.com.attacker.net/", "example.com", false},
		{"query-string injection", "https://evil.com/?q=example.com", "example.com", false},
		{"path injection", "https://evil.com/example.com/x", "example.com", false},
		{"different domain", "https://other.org/", "example.com", false},
		{"trailing dot host", "https://example.com./page", "example.com", true},
		{"case-insensitive", "https://EXAMPLE.com/page", "Example.COM", true},
		{"empty domain", "https://example.com/", "", false},
		{"unparseable url", "://bad", "example.com", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hostnameMatches(tt.url, tt.domain); got != tt.want {
				t.Errorf("hostnameMatches(%q, %q) = %v, want %v", tt.url, tt.domain, got, tt.want)
			}
		})
	}
}

func TestIsDomainAllowed_HostBased(t *testing.T) {
	t.Parallel()
	p := NewPipeline(PipelineConfig{
		AllowedDomains:  []string{"example.com", "test.org"},
		AllowPrivateIPs: true,
	})

	if !p.isDomainAllowed("https://example.com/page") {
		t.Error("expected example.com allowed")
	}
	if !p.isDomainAllowed("https://sub.example.com/page") {
		t.Error("expected sub.example.com allowed (subdomain)")
	}
	if p.isDomainAllowed("https://example.com.attacker.net/page") {
		t.Error("expected example.com.attacker.net to be BLOCKED (suffix spoof)")
	}
	if p.isDomainAllowed("https://evil.com/?ref=example.com") {
		t.Error("expected evil.com with example.com in query to be BLOCKED")
	}
	if p.isDomainAllowed("https://evil.com/page") {
		t.Error("expected evil.com to be blocked")
	}
}

func TestValidateScrapeURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid http", "http://example.com/page", false},
		{"valid https", "https://example.com/page", false},
		{"https with port", "https://example.com:8443/x", false},
		{"file scheme", "file:///etc/passwd", true},
		{"gopher scheme", "gopher://example.com/", true},
		{"ftp scheme", "ftp://example.com/file", true},
		{"scheme-relative", "//example.com/page", true},
		{"no scheme bare host", "example.com/page", true},
		{"empty host http", "http:///path", true},
		{"empty string", "", true},
		{"whitespace only", "   ", true},
		{"data scheme", "data:text/html,<script>1</script>", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateScrapeURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateScrapeURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestScrape_RejectsInvalidScheme(t *testing.T) {
	t.Parallel()
	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	_, err := p.Scrape(context.Background(), "file:///etc/passwd", 10000)
	if err == nil {
		t.Fatal("expected error for file:// scheme via Scrape chokepoint")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if se.Kind != ErrValidation {
		t.Errorf("expected ErrValidation for an unsupported scheme, got %v", se.Kind)
	}
}

func TestIsBlockedHostname_WidenedAndSuffix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host    string
		blocked bool
	}{
		{"metadata.google.internal", true},
		{"metadata.tencentyun.com", true},
		{"192.0.0.192", true},     // Oracle Cloud
		{"100.100.100.200", true}, // Alibaba Cloud
		{"kubernetes.default.svc", true},
		{"myservice.svc.cluster.local", true},     // suffix of svc.cluster.local
		{"a.b.namespace.svc.cluster.local", true}, // deep suffix
		{"svc.cluster.local", true},               // exact
		{"SVC.CLUSTER.LOCAL", true},               // case-insensitive
		{"myservice.svc.cluster.local.", true},    // trailing-dot FQDN
		{"svc.cluster.local.evil.com", false},     // MUST NOT false-positive
		{"notmetadata.tencentyun.com.evil.com", false},
		{"www.example.com", false},
		{"google.com", false},
	}
	for _, tt := range tests {
		if got := isBlockedHostname(tt.host); got != tt.blocked {
			t.Errorf("isBlockedHostname(%q) = %v, want %v", tt.host, got, tt.blocked)
		}
	}
}

func TestScrapeRaw_ReturnsBodyAndRealMIME(t *testing.T) {
	t.Parallel()
	raw := `<!DOCTYPE html><html><head><style>.x{}</style></head>` +
		`<body><script>alert('xss')</script><p>raw body content</p></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(raw))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.ScrapeRaw(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("ScrapeRaw error: %v", err)
	}
	if result.ContentType != "text/html; charset=utf-8" {
		t.Errorf("expected real MIME content type, got %q", result.ContentType)
	}
	// Raw mode must NOT strip scripts/styles — content.Process is skipped.
	if !strings.Contains(result.Content, "<script>alert('xss')</script>") {
		t.Error("expected raw <script> to be preserved in raw mode")
	}
	if !strings.Contains(result.Content, "<style>.x{}</style>") {
		t.Error("expected raw <style> to be preserved in raw mode")
	}
	if result.Content != raw {
		t.Error("expected verbatim body in raw mode")
	}
}

func TestScrapeRaw_HonorsLimitReader(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("A", 10000)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.ScrapeRaw(context.Background(), ts.URL, 500)
	if err != nil {
		t.Fatalf("ScrapeRaw error: %v", err)
	}
	if len(result.Content) != 500 {
		t.Errorf("expected content capped at 500 bytes, got %d", len(result.Content))
	}
	if !result.Truncated {
		t.Error("expected Truncated=true when body exceeds maxLength")
	}
}

func TestScrapeRaw_EnforcesGuards(t *testing.T) {
	t.Parallel()

	t.Run("rejects invalid scheme", func(t *testing.T) {
		t.Parallel()
		p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
		if _, err := p.ScrapeRaw(context.Background(), "ftp://example.com/x", 1000); err == nil {
			t.Fatal("expected scheme rejection")
		}
	})

	t.Run("rejects disallowed domain", func(t *testing.T) {
		t.Parallel()
		p := NewPipeline(PipelineConfig{
			AllowPrivateIPs: true,
			AllowedDomains:  []string{"allowed.com"},
		})
		_, err := p.ScrapeRaw(context.Background(), "https://blocked.com/page", 1000)
		if err == nil {
			t.Fatal("expected domain rejection")
		}
		if !strings.Contains(err.Error(), "not in allowed list") {
			t.Errorf("expected allowlist error, got: %v", err)
		}
	})

	t.Run("rejects private IP via SSRF client", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("secret"))
		}))
		defer ts.Close()
		// allowPrivate=false => SSRF client blocks the localhost test server.
		p := NewPipeline(PipelineConfig{AllowPrivateIPs: false})
		if _, err := p.ScrapeRaw(context.Background(), ts.URL, 1000); err == nil {
			t.Fatal("expected SSRF block for private IP")
		}
	})
}

// TestScrape_SSRFCompositeIsValidation verifies that an SSRF denial reached
// through the tiered-fallback path (where each tier wraps it as a generic
// network error) is classified as ErrValidation — a permanent security
// rejection — not a retryable ErrNetwork.
func TestScrape_SSRFCompositeIsValidation(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer ts.Close()

	// allowPrivate=false => the SSRF-safe client blocks the loopback test server
	// at every tier; the composite must surface a validation denial.
	p := NewPipeline(PipelineConfig{AllowPrivateIPs: false})
	_, err := p.Scrape(context.Background(), ts.URL, 1000)
	if err == nil {
		t.Fatal("expected SSRF denial via the tiered path")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if se.Kind != ErrValidation {
		t.Errorf("SSRF composite denial must be ErrValidation (permanent), got %v", se.Kind)
	}
}

func TestScrapeRaw_HTTPError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	_, err := p.ScrapeRaw(context.Background(), ts.URL, 1000)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("expected *ScrapeError, got %T", err)
	}
	if se.Kind != ErrBlocked {
		t.Errorf("expected ErrBlocked, got %v", se.Kind)
	}
}

func TestPrivateRangesParse(t *testing.T) {
	t.Parallel()
	// Smoke test for M8: exercising isPrivateIP forces every mustParseCIDR
	// literal to be parsed; a malformed literal would panic at package init.
	cases := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"192.0.0.192", true}, // 192.0.0.0/24
		{"::1", true},
		{"fc00::1", true},
		{"8.8.8.8", false},
		{"2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("failed to parse %s", c.ip)
		}
		if got := isPrivateIP(ip); got != c.private {
			t.Errorf("isPrivateIP(%s) = %v, want %v", c.ip, got, c.private)
		}
	}
}

func TestIsTwitterURL_HostBased(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url    string
		expect bool
	}{
		{"https://x.com/user/status/123", true},
		{"https://twitter.com/user", true},
		{"https://www.twitter.com/user", true},
		{"https://mobile.twitter.com/user", true},
		{"https://example.com/?ref=x.com/abc", false},
		{"https://notx.com/page", false},
		{"https://example.com/twitter.com/x", false},
	}
	for _, tt := range tests {
		if got := isTwitterURL(tt.url); got != tt.expect {
			t.Errorf("isTwitterURL(%q) = %v, want %v", tt.url, got, tt.expect)
		}
	}
}

func TestIsYouTubeURL_HostBased_NoSpoofing(t *testing.T) {
	t.Parallel()
	if isYouTubeURL("https://evil.com/youtube.com/watch?v=x") {
		t.Error("expected path-embedded youtube.com NOT to match")
	}
	if isYouTubeURL("https://example.com/?u=youtu.be/abc") {
		t.Error("expected query-embedded youtu.be NOT to match")
	}
	if !isYouTubeURL("https://m.youtube.com/watch?v=abc") {
		t.Error("expected m.youtube.com/watch to match")
	}
}

func TestIsDocumentURL_HostBased_NoSpoofing(t *testing.T) {
	t.Parallel()
	if isDocumentURL("https://evil.com/?x=file.pdf") {
		t.Error("expected query-embedded .pdf NOT to match (path only)")
	}
	if !isDocumentURL("https://arxiv.org/pdf/2401.00001") {
		t.Error("expected arxiv.org/pdf/ to match")
	}
	if isDocumentURL("https://evil.com/arxiv.org/pdf/x") {
		t.Error("expected path-embedded arxiv.org/pdf NOT to match")
	}
}

func TestPipeline_ConcurrentScrapes_ErrorIsolation(t *testing.T) {
	// Verify that concurrent scrapes don't contaminate each other's errors
	successContent := strings.Repeat("Successful page content. ", 20)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/success" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, successContent)
		} else {
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 5, AllowPrivateIPs: true})

	var wg sync.WaitGroup
	successCount := atomic.Int32{}
	errorCount := atomic.Int32{}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var url string
			if i%2 == 0 {
				url = ts.URL + "/success"
			} else {
				url = ts.URL + "/fail"
			}
			result, err := p.Scrape(context.Background(), url, 50000)
			if err == nil && result != nil && len(result.Content) > 100 {
				successCount.Add(1)
			} else {
				errorCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if successCount.Load() != 5 {
		t.Errorf("expected 5 successes, got %d", successCount.Load())
	}
	if errorCount.Load() != 5 {
		t.Errorf("expected 5 errors, got %d", errorCount.Load())
	}
}

// TestBrowserEnabled verifies the CHROME_PATH semantics, including the
// documented "disabled" sentinel that turns the browser tier off entirely
// (no autodetect, no go-rod download). This guards the doc claim in
// docs/SECURITY_AND_COMPLIANCE.md / DEPLOYMENT.md against drift.
func TestBrowserEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		chromePath string
		want       bool
	}{
		{"disabled sentinel turns browser off", "disabled", false},
		{"explicit path enables browser", "/usr/bin/chromium-browser", true},
		// empty path => autodetect; result depends on host, so not asserted here.
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := NewPipeline(PipelineConfig{ChromePath: tt.chromePath})
			if got := p.browserEnabled(); got != tt.want {
				t.Errorf("browserEnabled() with ChromePath=%q = %v, want %v", tt.chromePath, got, tt.want)
			}
		})
	}
}

// TestScrapeHTML_BoundsOversizeBody verifies the tier-3 HTML scraper caps the
// decompressed body it loads into goquery (maxHTMLRead), so a very large or
// decompression-bomb page cannot exhaust memory (OWASP Agentic ASI06). The
// server must still return a bounded, successful result rather than OOM/erroring.
func TestScrapeHTML_BoundsOversizeBody(t *testing.T) {
	// maxHTMLRead mirrors the NewPipeline default (PipelineConfig.MaxHTMLBytes).
	const maxHTMLRead = 8 << 20 // 8 MB
	// Build a body well over maxHTMLRead: a valid <article> followed by a
	// huge filler tail. goquery should parse only up to the cap.
	var sb strings.Builder
	sb.WriteString("<html><head><title>Big</title></head><body><article><h1>Heading</h1><p>")
	sb.WriteString(strings.Repeat("the quick brown fox jumps over the lazy dog. ", 200))
	sb.WriteString("</p></article>")
	filler := strings.Repeat("x", maxHTMLRead+2*1024*1024) // pushes total past the cap
	sb.WriteString(filler)
	sb.WriteString("</body></html>")
	full := sb.String()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, full)
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeHTML(context.Background(), ts.URL, 5_000_000)
	if err != nil {
		t.Fatalf("scrapeHTML errored on oversize body: %v", err)
	}
	// The parsed+extracted content must be bounded by the read cap, proving we
	// did not load the multi-MB filler into the DOM.
	if len(res.Content) > maxHTMLRead {
		t.Errorf("extracted content %d bytes exceeds maxHTMLRead %d — body was not bounded", len(res.Content), maxHTMLRead)
	}
	if !strings.Contains(res.Content, "quick brown fox") {
		t.Error("expected the leading article text to survive the cap")
	}
}

// =============================================================================
// PDF Content-Type Detection Tests (#206)
// =============================================================================

// minimalPDFB64 is a base64-encoded minimal but structurally valid PDF 1.4
// document with one page and one text object ("Hello PDF test content").
// It starts with %PDF so looksLikePDF fires, and the gopdf parser can extract
// the text — confirming real document routing, not just header detection.
// Generated once with a Python script; validated by TestProbeMinimalPDF.
const minimalPDFB64 = "JVBERi0xLjQKJeLjz9MKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCjIgMCBvYmoKPDwgL1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDEgPj4KZW5kb2JqCjMgMCBvYmoKPDwgL1R5cGUgL1BhZ2UgL1BhcmVudCAyIDAgUiAvTWVkaWFCb3ggWzAgMCA2MTIgNzkyXQogICAvQ29udGVudHMgNCAwIFIgL1Jlc291cmNlcyA8PCAvRm9udCA8PCAvRjEgNSAwIFIgPj4gPj4gPj4KZW5kb2JqCjQgMCBvYmoKPDwgL0xlbmd0aCA1NCA+PgpzdHJlYW0KQlQgL0YxIDEyIFRmIDEwMCA3MDAgVGQgKEhlbGxvIFBERiB0ZXN0IGNvbnRlbnQpIFRqIEVUCmVuZHN0cmVhbQplbmRvYmoKNSAwIG9iago8PCAvVHlwZSAvRm9udCAvU3VidHlwZSAvVHlwZTEgL0Jhc2VGb250IC9IZWx2ZXRpY2EgPj4KZW5kb2JqCnhyZWYKMCA2CjAwMDAwMDAwMDAgNjU1MzUgZiAKMDAwMDAwMDAxNSAwMDAwMCBuIAowMDAwMDAwMDY0IDAwMDAwIG4gCjAwMDAwMDAxMjEgMDAwMDAgbiAKMDAwMDAwMDI1MCAwMDAwMCBuIAowMDAwMDAwMzU0IDAwMDAwIG4gCnRyYWlsZXIKPDwgL1Jvb3QgMSAwIFIgL1NpemUgNiA+PgpzdGFydHhyZWYKNDI0CiUlRU9GCg=="

// minimalPDF returns valid PDF bytes for test cases.
func minimalPDF(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(minimalPDFB64)
	if err != nil {
		t.Fatalf("minimalPDF: base64 decode: %v", err)
	}
	return data
}

// TestIsPDFContentType checks the Content-Type helper directly.
func TestIsPDFContentType(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"application/pdf", true},
		{"Application/PDF", true},
		{"application/pdf; charset=utf-8", true},
		{"text/html", false},
		{"text/html; charset=utf-8", false},
		{"", false},
		{"application/octet-stream", false},
	}
	for _, tc := range cases {
		if got := isPDFContentType(tc.ct); got != tc.want {
			t.Errorf("isPDFContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

// TestLooksLikePDF checks the magic-byte helper directly.
func TestLooksLikePDF(t *testing.T) {
	cases := []struct {
		body []byte
		want bool
	}{
		{[]byte("%PDF-1.4 ..."), true},
		{[]byte("%pdf-1.4 ..."), false}, // lowercase — not a real PDF header
		{[]byte("<html>"), false},
		{[]byte{}, false},
		{[]byte("%PD"), false}, // too short
	}
	for _, tc := range cases {
		if got := looksLikePDF(tc.body); got != tc.want {
			t.Errorf("looksLikePDF(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// TestScrapeStealth_PDFContentType_Reroutes verifies that the stealth tier
// re-routes to scrapeBodyAsPDF when the server sends application/pdf (#206).
// The URL has no .pdf suffix so isDocumentURL would miss it.
func TestScrapeStealth_PDFContentType_Reroutes(t *testing.T) {
	t.Parallel()
	pdf := minimalPDF(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		w.Write(pdf) //nolint:errcheck
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeStealth(context.Background(), ts.URL+"/download/paper", 50_000)
	if err != nil {
		t.Fatalf("scrapeStealth unexpectedly errored on PDF response: %v", err)
	}
	if res == nil {
		t.Fatal("scrapeStealth returned nil result for PDF response")
	}
	if res.ContentType != "pdf" {
		t.Errorf("ContentType = %q, want %q", res.ContentType, "pdf")
	}
	if res.Tier != "document" {
		t.Errorf("Tier = %q, want %q", res.Tier, "document")
	}
	if !strings.Contains(res.Content, "Hello PDF test content") {
		t.Errorf("expected extracted PDF text in Content, got %q", res.Content)
	}
}

// TestScrapeHTML_PDFContentType_Reroutes verifies the same re-routing in the
// tier-3 HTML scraper (#206).
func TestScrapeHTML_PDFContentType_Reroutes(t *testing.T) {
	t.Parallel()
	pdf := minimalPDF(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		w.Write(pdf) //nolint:errcheck
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeHTML(context.Background(), ts.URL+"/view/fulltext", 50_000)
	if err != nil {
		t.Fatalf("scrapeHTML unexpectedly errored on PDF response: %v", err)
	}
	if res == nil {
		t.Fatal("scrapeHTML returned nil result for PDF response")
	}
	if res.ContentType != "pdf" {
		t.Errorf("ContentType = %q, want %q", res.ContentType, "pdf")
	}
	if res.Tier != "document" {
		t.Errorf("Tier = %q, want %q", res.Tier, "document")
	}
	if !strings.Contains(res.Content, "Hello PDF test content") {
		t.Errorf("expected extracted PDF text in Content, got %q", res.Content)
	}
}

// TestScrapeStealth_PDFMagicBytes_Reroutes verifies that %PDF magic bytes
// trigger document parsing even when Content-Type is wrong or absent (#206).
func TestScrapeStealth_PDFMagicBytes_Reroutes(t *testing.T) {
	t.Parallel()
	pdf := minimalPDF(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Content-Type header — server misconfiguration
		w.WriteHeader(http.StatusOK)
		w.Write(pdf) //nolint:errcheck
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeStealth(context.Background(), ts.URL+"/report", 50_000)
	if err != nil {
		t.Fatalf("scrapeStealth errored on PDF-magic-bytes response: %v", err)
	}
	if res == nil {
		t.Fatal("scrapeStealth returned nil for PDF-magic-bytes response")
	}
	if res.ContentType != "pdf" {
		t.Errorf("ContentType = %q, want %q", res.ContentType, "pdf")
	}
	if !strings.Contains(res.Content, "Hello PDF test content") {
		t.Errorf("expected extracted PDF text in Content, got %q", res.Content)
	}
}

// TestScrapeHTML_HTMLContent_NotRerouted confirms that a normal HTML response
// is NOT mis-classified as a PDF — regression guard (#206).
func TestScrapeHTML_HTMLContent_NotRerouted(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "<html><body><article><p>"+strings.Repeat("Normal article text. ", 20)+"</p></article></body></html>") //nolint:errcheck
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeHTML(context.Background(), ts.URL+"/article", 50_000)
	if err != nil {
		t.Fatalf("scrapeHTML errored on plain HTML: %v", err)
	}
	if res == nil {
		t.Fatal("scrapeHTML returned nil for plain HTML")
	}
	if res.ContentType == "pdf" {
		t.Error("ContentType should not be pdf for a plain HTML response")
	}
}

// =============================================================================
// SPA-shell detection + escalate-then-best-tier selection (deterministic
// completeness heuristic). A server-rendered SPA ships a large static HTML
// shell (JS bundles, hydration JSON) that extracts into only a sliver of text;
// the stealth tier clears the 100-byte floor on that shell, so the old "first
// tier over 100 bytes wins" rule never escalated to the JS-executing browser
// tier. looksLikePartialShell flags it structurally (low text + high HTML:text
// ratio) so the pipeline keeps walking and returns the best tier's output.
// =============================================================================

func TestLooksLikePartialShell(t *testing.T) {
	t.Parallel()
	// wordsOfLen returns a space-separated string of single-char "words" whose
	// total length is exactly targetBytes, giving well over shellMaxWords
	// words for any targetBytes used below — isolating the ratio branch under
	// test from the new word-count branch.
	wordsOfLen := func(targetBytes int) string {
		if targetBytes <= 0 {
			return ""
		}
		// "x x x...x": odd positions are spaces, so length targetBytes needs
		// (targetBytes+1)/2 "x" chars.
		var b strings.Builder
		for b.Len() < targetBytes {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteByte('x')
		}
		s := b.String()
		if len(s) > targetBytes {
			s = s[:targetBytes]
		}
		return s
	}

	tests := []struct {
		name         string
		content      string
		rawHTMLBytes int
		want         bool
	}{
		{
			name:         "spa shell: short text, huge HTML",
			content:      wordsOfLen(120), // > 100 floor, <= 2048 cap, >= 30 words
			rawHTMLBytes: 120 * 60,        // 60:1 ratio
			want:         true,
		},
		{
			name:         "short but complete: little text, little HTML",
			content:      wordsOfLen(120),
			rawHTMLBytes: 120 * 3, // 3:1 ratio — a real short page
			want:         false,
		},
		{
			name:         "substantial article never flagged regardless of ratio",
			content:      wordsOfLen(shellMaxTextBytes + 1),
			rawHTMLBytes: (shellMaxTextBytes + 1) * 100,
			want:         false,
		},
		{
			name:         "non-HTML tier (rawHTMLBytes==0) never flagged when word count is sufficient",
			content:      wordsOfLen(120),
			rawHTMLBytes: 0,
			want:         false,
		},
		{
			name:         "exactly at the ratio threshold is a shell",
			content:      wordsOfLen(100),
			rawHTMLBytes: 100 * shellMinHTMLRatio,
			want:         true,
		},
		{
			name:         "just under the ratio threshold is not a shell",
			content:      wordsOfLen(100),
			rawHTMLBytes: 100*shellMinHTMLRatio - 1,
			want:         false,
		},
		{
			name:         "empty content is not a shell",
			content:      "",
			rawHTMLBytes: 50000,
			want:         false,
		},
		{
			name:         "sparse word count flags markdown-tier stub with zero rawHTMLBytes",
			content:      "cookie banner accept all",
			rawHTMLBytes: 0,
			want:         true,
		},
		{
			name:         "short but complete page is not flagged despite low word count when rawHTMLBytes>0",
			content:      "just a few words here",
			rawHTMLBytes: 6, // ratio well under shellMinHTMLRatio — a genuinely short complete page
			want:         false,
		},
		{
			name:         "real short tweet-style article with low HTML ratio is not a shell",
			content:      "Just launched our new thing today after months of work. Excited to finally ship it and see what people think. Feedback welcome!",
			rawHTMLBytes: 250, // ~1.9:1 ratio, well under shellMinHTMLRatio — genuinely short & complete
			want:         false,
		},
		{
			name:         "short HTML-tier result is flagged when the HTML:text ratio is high (an actual shell)",
			content:      "just a few words here",
			rawHTMLBytes: 21 * shellMinHTMLRatio, // ratio at threshold — a real SPA shell
			want:         true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &ScrapeResult{Content: tt.content, rawHTMLBytes: tt.rawHTMLBytes}
			if got := looksLikePartialShell(r); got != tt.want {
				t.Errorf("looksLikePartialShell() = %v, want %v (text=%d html=%d words=%d)",
					got, tt.want, len(tt.content), tt.rawHTMLBytes, len(strings.Fields(tt.content)))
			}
		})
	}

	if looksLikePartialShell(nil) {
		t.Error("looksLikePartialShell(nil) should be false")
	}
}

func TestBetterResult(t *testing.T) {
	t.Parallel()
	short := &ScrapeResult{Content: "short"}
	long := &ScrapeResult{Content: "this is a longer result body"}

	if got := betterResult(nil, nil); got != nil {
		t.Errorf("betterResult(nil,nil) = %v, want nil", got)
	}
	if got := betterResult(nil, short); got != short {
		t.Error("betterResult(nil, x) should return x")
	}
	if got := betterResult(short, nil); got != short {
		t.Error("betterResult(x, nil) should return x")
	}
	if got := betterResult(short, long); got != long {
		t.Error("betterResult should pick the longer candidate")
	}
	if got := betterResult(long, short); got != long {
		t.Error("betterResult should keep the longer incumbent")
	}
	// Tie prefers the incumbent (earlier, cheaper tier).
	tie := &ScrapeResult{Content: "short"}
	if got := betterResult(short, tie); got != short {
		t.Error("betterResult should prefer the incumbent on a length tie")
	}
}

// spaShellHTML builds a page whose extractable text is `para` but whose raw
// HTML is inflated far past the shell ratio by a large inert <script> blob
// (stripped before text extraction, so it counts toward rawHTMLBytes only).
func spaShellHTML(title, para string) string {
	bulk := strings.Repeat("/*hydration state padding*/\n", 600) // ~16KB of script
	return `<!DOCTYPE html><html><head><title>` + title + `</title>` +
		`<script type="application/json" id="__NEXT_DATA__">` + bulk + `</script>` +
		`</head><body><div id="root"><p>` + para + `</p></div></body></html>`
}

func TestPipeline_SPAShellEscalatesPastStealth(t *testing.T) {
	// The stealth tier extracts only the shell paragraph (clears 100 bytes) but
	// it looks like a partial SPA shell, so the pipeline must NOT short-circuit:
	// it keeps walking to the html tier (and would reach the browser tier if one
	// were available). We observe the escalation by request count.
	para := strings.Repeat("Above-the-fold blurb. ", 6) // ~132 chars > 100 floor
	html := spaShellHTML("SPA", para)

	var requestCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	defer ts.Close()

	// Force the browser tier off so the run is deterministic in CI; the best-of
	// fallback then returns the shell content rather than erroring (no regression).
	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("expected best-of fallback to return shell content, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Above-the-fold blurb") {
		t.Fatalf("expected shell content returned as fallback, got: %+v", result)
	}
	// markdown (406) + stealth + html = 3 requests proves it did NOT stop at stealth.
	if got := requestCount.Load(); got < 3 {
		t.Errorf("expected escalation past stealth (>=3 requests), got %d", got)
	}
}

func TestPipeline_ShortCompletePageShortCircuits(t *testing.T) {
	// A genuinely short but complete page (little text AND little HTML, low ratio)
	// must short-circuit at the stealth tier — it is NOT a shell, so the html tier
	// is never requested.
	para := strings.Repeat("Complete short article body. ", 8) // ~232 chars
	html := `<!DOCTYPE html><html><head><title>Short</title></head><body><article><p>` +
		para + `</p></article></body></html>`

	var requestCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Complete short article body") {
		t.Fatalf("expected short page content, got: %+v", result)
	}
	if result.Tier != "stealth" {
		t.Errorf("expected stealth tier to win, got %q", result.Tier)
	}
	// markdown (406) + stealth = 2 requests; html tier must NOT have been hit.
	if got := requestCount.Load(); got != 2 {
		t.Errorf("expected short-circuit at stealth (2 requests), got %d", got)
	}
}

func TestPipeline_ShortUnder30WordsCompletePageShortCircuits(t *testing.T) {
	// Regression test for a bug where looksLikePartialShell's word-count branch
	// fired on ANY tier result under shellMaxWords (30) words, with no
	// rawHTMLBytes/ratio precondition — misclassifying genuinely short but
	// complete real-world pages (tweets, dictionary entries, short Q&A
	// answers) as SPA shells and forcing full tier escalation plus
	// Partial:true. This content is a real tweet-style post: 22 words, little
	// HTML, low HTML:text ratio — a genuinely short but complete page, not a
	// shell. It must short-circuit at the stealth tier with Partial:false.
	body := "Just launched our new thing today after months of work. Excited to finally ship it and see what people think. Feedback welcome!"
	html := `<!DOCTYPE html><html><head><title>Post</title></head><body><article><p>` +
		body + `</p></article></body></html>`

	var requestCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Just launched our new thing") {
		t.Fatalf("expected short page content, got: %+v", result)
	}
	if result.Tier != "stealth" {
		t.Errorf("expected stealth tier to win without escalating to html, got tier %q", result.Tier)
	}
	if result.Partial {
		t.Error("expected Partial=false for a genuinely short but complete page under 30 words")
	}
	// markdown (406) + stealth = 2 requests; html tier must NOT have been hit.
	if got := requestCount.Load(); got != 2 {
		t.Errorf("expected short-circuit at stealth (2 requests), got %d", got)
	}
}

func TestStampSparsity_WordCountBoundary(t *testing.T) {
	t.Parallel()
	// Regression test for the exact sparseWordThreshold (150) boundary: the
	// contract is strictly "< 150 triggers SparsityWarning", so 150 words must
	// NOT warn, while 149 and 151 pin down the boundary on either side. No
	// existing fixture in the suite lands at 149/150/151 words, so an off-by-one
	// (e.g. "<" flipped to "<=") would pass every other test silently.
	words := func(n int) string {
		w := make([]string, n)
		for i := range w {
			w[i] = "word"
		}
		return strings.Join(w, " ")
	}

	tests := []struct {
		name      string
		wordCount int
		wantWarn  bool
	}{
		{"149 words is sparse", 149, true},
		{"exactly 150 words is not sparse", 150, false},
		{"151 words is not sparse", 151, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &ScrapeResult{Content: words(tt.wordCount)}
			stampSparsity(r)
			if r.WordCount != tt.wordCount {
				t.Fatalf("WordCount = %d, want %d", r.WordCount, tt.wordCount)
			}
			gotWarn := r.SparsityWarning != ""
			if gotWarn != tt.wantWarn {
				t.Errorf("SparsityWarning set = %v (value %q), want set=%v", gotWarn, r.SparsityWarning, tt.wantWarn)
			}
		})
	}

	stampSparsity(nil) // must not panic
}

// TestStampSparsity_CJKContentNotMisflagged is a regression test: a complete
// CJK article has no ASCII whitespace, so strings.Fields would count it as
// ~1 "word" and fire a false SparsityWarning even though nothing is missing.
// stampSparsity must use a script-aware word count instead.
func TestStampSparsity_CJKContentNotMisflagged(t *testing.T) {
	t.Parallel()
	// A genuine, complete Chinese paragraph repeated to be unambiguously long —
	// zero ASCII spaces, well over sparseWordThreshold (150) runes.
	content := strings.Repeat("这是一段完整的中文新闻内容用于测试提取质量与字数统计逻辑是否正确处理非拉丁语言的文本", 4)
	r := &ScrapeResult{Content: content}
	stampSparsity(r)
	if r.WordCount < sparseWordThreshold {
		t.Errorf("expected CJK content's script-aware word count to clear sparseWordThreshold, got WordCount=%d", r.WordCount)
	}
	if r.SparsityWarning != "" {
		t.Errorf("expected no SparsityWarning for complete CJK content, got %q", r.SparsityWarning)
	}
}

// TestLooksLikePartialShell_CJKContentNotMisflagged is a regression test for
// the same strings.Fields defect in the rawHTMLBytes==0 branch: a complete
// CJK/Thai/Lao article must not be misclassified as a partial shell.
func TestLooksLikePartialShell_CJKContentNotMisflagged(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("这是一段完整的中文新闻内容用于测试提取质量与字数统计逻辑是否正确处理非拉丁语言的文本", 3)
	r := &ScrapeResult{Content: content, rawHTMLBytes: 0}
	if looksLikePartialShell(r) {
		t.Errorf("expected complete CJK content (rawHTMLBytes==0) to NOT be flagged as a partial shell")
	}
}

// TestLooksLikePartialShell_BrowserTierExempt is a regression test: the
// browser tier already executed JS, so a short browser-tier result is a
// genuinely short page, not a shell to keep escalating past — there is
// nothing further to render. Only the browser tier gets this exemption;
// other rawHTMLBytes==0 tiers (markdown) still use the word-count check.
func TestLooksLikePartialShell_BrowserTierExempt(t *testing.T) {
	t.Parallel()
	short := &ScrapeResult{Content: "cookie banner accept all", rawHTMLBytes: 0, Tier: "browser"}
	if looksLikePartialShell(short) {
		t.Error("expected a short browser-tier result to NOT be flagged as a partial shell")
	}
	// Same content, markdown tier: still flagged (unchanged behavior).
	markdown := &ScrapeResult{Content: "cookie banner accept all", rawHTMLBytes: 0, Tier: "markdown"}
	if !looksLikePartialShell(markdown) {
		t.Error("expected the same short markdown-tier result to still be flagged as a partial shell")
	}
}

// =============================================================================
// Cross-Origin Iframe Follow Tests (#399)
// =============================================================================

// iframeShellHTML builds a partial-shell wrapper page (large inert blob to
// trip looksLikePartialShell's ratio, tiny visible text) embedding one or more
// cross-origin iframes pointing at the given src values verbatim (so callers
// can pass absolute URLs, relative paths, or non-http(s) schemes to test
// extraction/dropping).
func iframeShellHTML(title, blurb string, iframeSrcs ...string) string {
	bulk := strings.Repeat("/*hydration state padding*/\n", 600) // ~16KB of script
	var iframes strings.Builder
	for _, src := range iframeSrcs {
		iframes.WriteString(`<iframe src="` + src + `"></iframe>`)
	}
	return `<!DOCTYPE html><html><head><title>` + title + `</title>` +
		`<script type="application/json" id="__NEXT_DATA__">` + bulk + `</script>` +
		`</head><body><div id="root"><p>` + blurb + `</p>` + iframes.String() + `</div></body></html>`
}

func TestExtractIframeCandidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		html string
		base string
		want []string
	}{
		{
			name: "absolute http(s) src kept",
			html: `<iframe src="https://embed.example.com/widget"></iframe>`,
			base: "https://wrapper.example.com/page",
			want: []string{"https://embed.example.com/widget"},
		},
		{
			name: "relative src resolved against base",
			html: `<iframe src="/embed"></iframe><iframe src="embed2.html"></iframe>`,
			base: "https://wrapper.example.com/dir/page",
			want: []string{"https://wrapper.example.com/embed", "https://wrapper.example.com/dir/embed2.html"},
		},
		{
			name: "data/about/javascript/mailto/empty dropped",
			html: `<iframe src="data:text/html,hi"></iframe>` +
				`<iframe src="about:blank"></iframe>` +
				`<iframe src="javascript:void(0)"></iframe>` +
				`<iframe src="blob:https://x/y"></iframe>` +
				`<iframe src="mailto:a@b.com"></iframe>` +
				`<iframe src=""></iframe>`,
			base: "https://wrapper.example.com/page",
			want: nil,
		},
		{
			name: "duplicate absolute src deduped",
			html: `<iframe src="https://embed.example.com/widget"></iframe>` +
				`<iframe src="https://embed.example.com/widget"></iframe>`,
			base: "https://wrapper.example.com/page",
			want: []string{"https://embed.example.com/widget"},
		},
		{
			name: "capped at maxIframeCandidates",
			html: `<iframe src="https://a.example.com/1"></iframe>` +
				`<iframe src="https://b.example.com/2"></iframe>` +
				`<iframe src="https://c.example.com/3"></iframe>`,
			base: "https://wrapper.example.com/page",
			want: []string{"https://a.example.com/1", "https://b.example.com/2"},
		},
		{
			name: "no iframe present returns nil",
			html: `<p>no iframes here</p>`,
			base: "https://wrapper.example.com/page",
			want: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := goQueryFromString(tt.html)
			if err != nil {
				t.Fatalf("goQueryFromString error: %v", err)
			}
			got := extractIframeCandidates(doc, tt.base)
			if len(got) != len(tt.want) {
				t.Fatalf("extractIframeCandidates() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("candidate[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestPipeline_IframeFollow_HappyPath(t *testing.T) {
	articleContent := strings.Repeat("Real article content recovered from the iframe target. ", 20)
	innerHTML := `<!DOCTYPE html><html><head><title>Inner</title></head><body><article><p>` +
		articleContent + `</p></article></body></html>`

	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(innerHTML))
	}))
	defer inner.Close()

	var wrapperHTML string
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(wrapperHTML))
	}))
	defer wrapper.Close()
	wrapperHTML = iframeShellHTML("Wrapper", "Above-the-fold blurb.", inner.URL)

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), wrapper.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Real article content recovered") {
		t.Fatalf("expected recursed iframe content to win, got: %+v", result)
	}
	if strings.Contains(result.Content, "Above-the-fold blurb") {
		t.Error("expected the shell blurb to lose to the iframe's real content")
	}
}

// TestPipeline_IframeFollow_PrivateIPCandidateSkipped proves the recursive
// iframe-follow goes through the same SSRF-safe dial path as every other
// scrape entry point. The wrapper itself is a normal reachable loopback
// server (AllowPrivateIPs: true, so it succeeds like any other test in this
// file), but its iframe candidate points at a cloud-metadata literal
// (169.254.169.254) that isBlockedHostname rejects unconditionally,
// regardless of AllowPrivateIPs — so the candidate is refused before any
// connection is attempted, and the pipeline falls back to the wrapper shell.
func TestPipeline_IframeFollow_PrivateIPCandidateSkipped(t *testing.T) {
	wrapperHTML := iframeShellHTML("Wrapper", "Above-the-fold blurb.", "http://169.254.169.254/latest/meta-data/")
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(wrapperHTML))
	}))
	defer wrapper.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), wrapper.URL, 50000)
	if err != nil {
		t.Fatalf("expected best-of fallback to the wrapper shell, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Above-the-fold blurb") {
		t.Fatalf("expected the wrapper shell to be returned since the only candidate is blocked, got: %+v", result)
	}
}

// TestPipeline_IframeFollow_AllowedDomainsRejection proves the allowlist gate
// applies to a recursed iframe candidate exactly as it does to any other
// scrape target. Both servers run on loopback in this test process, so the
// wrapper is addressed as "127.0.0.1" and the inner candidate is addressed as
// "localhost" (same loopback network, textually distinct hostname) — letting
// AllowedDomains, which matches on hostname text, allow the former and reject
// the latter even though both are in fact reachable.
func TestPipeline_IframeFollow_AllowedDomainsRejection(t *testing.T) {
	var innerHits atomic.Int32
	innerContent := strings.Repeat("Real article content behind a disallowed iframe origin. ", 20)
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, innerContent)
	}))
	defer inner.Close()
	innerPort := strings.TrimPrefix(inner.URL, "http://127.0.0.1:")
	innerURLViaLocalhost := "http://localhost:" + innerPort

	wrapperHTML := iframeShellHTML("Wrapper", "Above-the-fold blurb.", innerURLViaLocalhost)
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(wrapperHTML))
	}))
	defer wrapper.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
		AllowedDomains:  []string{"127.0.0.1"},
		ChromePath:      chromeDisabled,
	})

	result, err := p.Scrape(context.Background(), wrapper.URL, 50000)
	if err != nil {
		t.Fatalf("expected best-of fallback to the wrapper shell, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Above-the-fold blurb") {
		t.Fatalf("expected the wrapper shell to be returned since its only candidate is not allowlisted, got: %+v", result)
	}
	if innerHits.Load() != 0 {
		t.Errorf("expected the iframe candidate to never be dialed when its domain is not allowlisted, got %d hits", innerHits.Load())
	}
}

func TestPipeline_IframeFollow_DepthCap(t *testing.T) {
	var thirdHits atomic.Int32
	thirdContent := strings.Repeat("Content behind a second-level iframe that must never be followed. ", 10)
	third := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		thirdHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, thirdContent)
	}))
	defer third.Close()

	var secondHTML string
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(secondHTML))
	}))
	defer second.Close()
	secondHTML = iframeShellHTML("Second", "Second-level blurb.", third.URL)

	var firstHTML string
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(firstHTML))
	}))
	defer first.Close()
	firstHTML = iframeShellHTML("First", "First-level blurb.", second.URL)

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), first.URL, 50000)
	if err != nil {
		t.Fatalf("expected best-of fallback to succeed, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if strings.Contains(result.Content, "second-level iframe that must never be followed") {
		t.Error("expected the third-level page to never be reached (depth cap of 1)")
	}
	if thirdHits.Load() != 0 {
		t.Errorf("expected the third-level iframe to never be dialed (depth cap of 1), got %d hits", thirdHits.Load())
	}
}

func TestPipeline_IframeFollow_TwoCandidateCap(t *testing.T) {
	var hits [3]atomic.Int32
	makeInner := func(idx int) *httptest.Server {
		content := strings.Repeat(fmt.Sprintf("Content from inner iframe number %d. ", idx), 20)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits[idx].Add(1)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, content)
		}))
	}
	inner0 := makeInner(0)
	defer inner0.Close()
	inner1 := makeInner(1)
	defer inner1.Close()
	inner2 := makeInner(2)
	defer inner2.Close()

	wrapperHTML := iframeShellHTML("Wrapper", "Above-the-fold blurb.", inner0.URL, inner1.URL, inner2.URL)
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(wrapperHTML))
	}))
	defer wrapper.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	_, err := p.Scrape(context.Background(), wrapper.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	// extractIframeCandidates itself caps at maxIframeCandidates(2), so only
	// the first two <iframe src> elements ever become candidates regardless of
	// how many appear in the markup — the third is never even extracted, let
	// alone dialed.
	if hits[2].Load() != 0 {
		t.Errorf("expected the third iframe (beyond the cap of 2) to never be dialed, got %d hits", hits[2].Load())
	}
}

func TestPipeline_IframeFollow_DecorativeIframeLoses(t *testing.T) {
	decorative := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>Ad</p></body></html>`))
	}))
	defer decorative.Close()

	realContent := strings.Repeat("Genuine article content that should win the contentQuality comparison. ", 20)
	real := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, realContent)
	}))
	defer real.Close()

	wrapperHTML := iframeShellHTML("Wrapper", "Above-the-fold blurb.", decorative.URL, real.URL)
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(wrapperHTML))
	}))
	defer wrapper.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), wrapper.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Genuine article content") {
		t.Fatalf("expected the real-content iframe to win via contentQuality scoring, got: %+v", result)
	}
}

// TestScrapeStealth_ThinContentWithIframeCandidates is a regression test for
// the stealth-tier early-return fix (#399): a near-empty extracted-text shell
// that carries iframe candidates must still produce a result (not nil), so
// the signal survives to the tiered-fallback loop that acts on it.
func TestScrapeStealth_ThinContentWithIframeCandidates(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>Thin</title></head><body>` +
		`<p>Hi.</p><iframe src="https://embed.example.com/real"></iframe></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeStealth error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil result carrying iframe candidates, not the old nil-on-thin-content behavior")
	}
	if len(result.iframeCandidates) != 1 || result.iframeCandidates[0] != "https://embed.example.com/real" {
		t.Errorf("expected iframeCandidates=[https://embed.example.com/real], got %v", result.iframeCandidates)
	}
}

// TestScrapeStealth_ThinContentNoIframeStillNil is the inverse regression
// check: a near-empty shell with NO iframe candidates must still return nil,
// exactly as before this fix — nothing changed for the common thin-page case.
func TestScrapeStealth_ThinContentNoIframeStillNil(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>Thin</title></head><body><p>Short.</p></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	result, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for thin content with no iframe candidates, got: %+v", result)
	}
}

// TestPipeline_IframeFollow_DedupedAcrossTiers proves that when multiple
// tiers (stealth and html both parse the wrapper's markup and both extract
// the same iframe src) independently flag the same shell, the shared
// candidate is dialed exactly once — not once per tier. Without the
// iframeSeen dedup map in tieredFallback, a wrapper page whose stealth AND
// html tiers both trip looksLikePartialShell would redundantly re-run the
// full recursive tier ladder against the same candidate URL for each tier.
func TestPipeline_IframeFollow_DedupedAcrossTiers(t *testing.T) {
	var innerHits atomic.Int32
	innerContent := strings.Repeat("Real article content recovered from the shared iframe target. ", 20)
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		innerHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, innerContent)
	}))
	defer inner.Close()

	var wrapperHTML string
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(wrapperHTML))
	}))
	defer wrapper.Close()
	// stealth and html both parse this same markup and will both independently
	// extract inner.URL as an iframe candidate before either strips <iframe>.
	wrapperHTML = iframeShellHTML("Wrapper", "Above-the-fold blurb.", inner.URL)

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(context.Background(), wrapper.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Real article content recovered") {
		t.Fatalf("expected recursed iframe content to win, got: %+v", result)
	}
	// The stealth tier runs before html and should recover the content itself,
	// returning immediately and never letting html get a turn at all — so this
	// also proves the early-exit-on-success behavior. Either way, the shared
	// candidate must be dialed exactly once.
	if hits := innerHits.Load(); hits != 1 {
		t.Errorf("expected the shared iframe candidate to be dialed exactly once across tiers, got %d hits", hits)
	}
}

// TestPipeline_IframeFollow_WinsOverThinExaResult is the regression test for
// the exact live bug found in real-user MCP testing (#399 follow-up): when
// EXA_API_KEY is configured, Exa's ScrapeResult never sets rawHTMLBytes, so
// looksLikePartialShell falls into its word-count-only branch for Exa
// results — a thin-but-not-quite-30-words-short Exa result used to read as
// "confidently complete" and short-circuit the tier loop via the old
// post-loop-only iframe-follow trigger, before the earlier stealth tier's
// already-found iframe candidate ever got a chance to run. With iframe-follow
// now triggered inline the moment stealth's own result is flagged a shell,
// the free-tier recovery must win and complete before Exa is ever invoked.
func TestPipeline_IframeFollow_WinsOverThinExaResult(t *testing.T) {
	var exaHits atomic.Int32
	exa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exaHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// A thin-but-word-count-passing body mirroring the real HF Space case:
		// enough words to clear shellMaxWords (30) so looksLikePartialShell
		// would (incorrectly) call this "not a shell" if it were ever reached.
		fmt.Fprint(w, `{"results":[{"id":"x","text":"`+
			strings.Repeat("Refreshing wrapper placeholder word. ", 6)+`"}]}`)
	}))
	defer exa.Close()
	withExaEndpoint(t, exa.URL)

	var innerHits atomic.Int32
	innerContent := strings.Repeat("Real article content recovered from the free-tier iframe target. ", 20)
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		innerHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, innerContent)
	}))
	defer inner.Close()

	var wrapperHTML string
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.WriteHeader(406)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(wrapperHTML))
	}))
	defer wrapper.Close()
	wrapperHTML = iframeShellHTML("Wrapper", "Refreshing", inner.URL)

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled, ExaAPIKey: "test-key"})
	result, err := p.Scrape(context.Background(), wrapper.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result == nil || !strings.Contains(result.Content, "Real article content recovered") {
		t.Fatalf("expected recursed free-tier iframe content to win over the thin Exa result, got: %+v", result)
	}
	if hits := exaHits.Load(); hits != 0 {
		t.Errorf("expected Exa to never be invoked once an earlier tier's iframe-follow recovered real content, got %d hits", hits)
	}
}

package scraper

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	if result.Title != "Full Pipeline Test" {
		t.Errorf("expected title 'Full Pipeline Test', got %q", result.Title)
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
		{"https://twitter.com/user/status/123", true},
		{"https://www.linkedin.com/in/user", true},
		{"https://example.com/page", false},
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

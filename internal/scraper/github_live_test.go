//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to
// raw.githubusercontent.com and api.github.com). Run with:
//
//	go test -tags=live -run TestGitHub.*Live ./internal/scraper/...
//
// Proves the native GitHub content routing (#395) actually reaches GitHub's
// real, unauthenticated raw CDN and REST API — no GITHUB_TOKEN required,
// since both the raw CDN and the Contents/Gist API fallbacks work
// unauthenticated (a token only raises the rate ceiling).
package scraper

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func skipIfNetworkUnreachableScraper(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		t.Skipf("network unreachable (DNS): %v", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Skipf("network unreachable (timeout): %v", err)
	}
	s := err.Error()
	if strings.Contains(s, "connection refused") || strings.Contains(s, "no such host") || strings.Contains(s, "network is unreachable") {
		t.Skipf("network unreachable: %v", err)
	}
}

func TestGitHubReadmeLive(t *testing.T) {
	p := NewPipeline(PipelineConfig{})

	res, err := p.Scrape(context.Background(), "https://github.com/golang/go", 4096)
	skipIfNetworkUnreachableScraper(t, err)
	if err != nil {
		t.Fatalf("Scrape() error: %v", err)
	}
	if res.Tier != "github:raw" && res.Tier != "github:contents-api" {
		t.Errorf("Tier = %q, want github:raw or github:contents-api", res.Tier)
	}
	if !strings.Contains(res.Content, "Go") {
		t.Errorf("Content = %q, want it to mention Go", res.Content)
	}
	t.Logf("tier=%s title=%q len(content)=%d", res.Tier, res.Title, len(res.Content))
}

func TestGitHubBlobLive(t *testing.T) {
	p := NewPipeline(PipelineConfig{})

	res, err := p.Scrape(context.Background(), "https://github.com/golang/go/blob/master/LICENSE", 4096)
	skipIfNetworkUnreachableScraper(t, err)
	if err != nil {
		t.Fatalf("Scrape() error: %v", err)
	}
	if res.Tier != "github:raw" {
		t.Errorf("Tier = %q, want github:raw", res.Tier)
	}
	if !strings.Contains(res.Content, "Copyright") {
		t.Errorf("Content = %q, want LICENSE text", res.Content)
	}
	t.Logf("tier=%s title=%q len(content)=%d", res.Tier, res.Title, len(res.Content))
}

func TestGitHubGistLive(t *testing.T) {
	p := NewPipeline(PipelineConfig{})

	// A long-lived, stable public gist (octocat's canonical example gist).
	res, err := p.Scrape(context.Background(), "https://gist.github.com/octocat/6cad326836d38bd3a7ae", 4096)
	skipIfNetworkUnreachableScraper(t, err)
	if err != nil {
		t.Fatalf("Scrape() error: %v", err)
	}
	if res.Tier != "github:gist-api" {
		t.Errorf("Tier = %q, want github:gist-api", res.Tier)
	}
	if res.Content == "" {
		t.Error("expected non-empty gist content")
	}
	t.Logf("tier=%s title=%q len(content)=%d", res.Tier, res.Title, len(res.Content))
}

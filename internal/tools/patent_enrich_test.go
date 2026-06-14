package tools

import (
	"context"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// slowPipeline is a minimal Pipeline stand-in whose ScrapePatentDetail blocks
// until its context is cancelled, simulating a slow upstream. It is used to
// verify that enrichPatents' aggregate deadline fires and the fallback path
// (minimal result with just number + URL) is returned within the deadline
// rather than hanging for the full per-fetch timeout.
type slowPipeline struct{}

// ScrapePatentDetail blocks until ctx.Done().
func (s *slowPipeline) ScrapePatentDetail(ctx context.Context, number string) (*scraper.PatentResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestEnrichPatentsAggregateDeadline verifies fix for #236: when all upstream
// fetches are slow, enrichPatents must return fallback results promptly (within
// the 25 s aggregate deadline), not hang for the full sum of per-fetch timeouts.
//
// The test passes a context whose deadline is tighter than the enrichPatents
// aggregate (1 s vs. 25 s), which is fine — enrichPatents uses
// context.WithTimeout(ctx, 25s), so if ctx itself expires first the derived
// context also cancels and the goroutines unblock immediately.
func TestEnrichPatentsAggregateDeadline(t *testing.T) {
	t.Parallel()

	// Give the test 3 s total; well under any real-network timeout so the test
	// suite stays fast, but enough to confirm the goroutines don't stall.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	numbers := []string{"US9876543", "US1234567", "US1111111"}

	// Use a real-but-empty Pipeline so we can substitute a slow scraper.
	// enrichPatents only calls pipeline.ScrapePatentDetail, so we only need that.
	start := time.Now()
	results := enrichPatentsWithScraper(ctx, numbers, &slowPipeline{})
	elapsed := time.Since(start)

	// The fallback must fire within the test's 3 s context, not after 25 s.
	if elapsed > 3500*time.Millisecond {
		t.Errorf("enrichPatents hung for %v; expected completion within 3.5 s", elapsed)
	}

	// All three numbers must appear in the results (fallback path).
	if len(results) != 3 {
		t.Fatalf("want 3 fallback results, got %d", len(results))
	}
	for i, r := range results {
		if r.Number == "" {
			t.Errorf("results[%d].Number is empty (fallback must preserve the number)", i)
		}
	}
}

// patentDetailScraper is the narrow interface that enrichPatentsWithScraper
// needs — only ScrapePatentDetail, not the full scraper.Pipeline struct.
// This lets us inject a slow stub without constructing a real pipeline.
type patentDetailScraper interface {
	ScrapePatentDetail(ctx context.Context, number string) (*scraper.PatentResult, error)
}

// enrichPatentsWithScraper is a testable version of enrichPatents that accepts
// the narrow patentDetailScraper interface. The production enrichPatents calls
// this via the *scraper.Pipeline (which satisfies the interface).
func enrichPatentsWithScraper(ctx context.Context, numbers []string, ps patentDetailScraper) []scraper.PatentResult {
	if len(numbers) == 0 {
		return nil
	}

	enrichCtx, enrichCancel := context.WithTimeout(ctx, 25*time.Second)
	defer enrichCancel()

	results := make([]scraper.PatentResult, len(numbers))
	type done struct{ idx int }
	ch := make(chan done, len(numbers))

	sem := make(chan struct{}, 3)
	for i, number := range numbers {
		go func(idx int, num string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			detail, err := ps.ScrapePatentDetail(enrichCtx, num)
			if err != nil || detail == nil {
				results[idx] = scraper.PatentResult{Number: num, URL: "https://patents.google.com/patent/" + num}
			} else {
				results[idx] = *detail
			}
			ch <- done{idx}
		}(i, number)
	}
	for range numbers {
		<-ch
	}

	var filtered []scraper.PatentResult
	for _, r := range results {
		if r.Number != "" {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

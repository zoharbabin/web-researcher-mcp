package scraper

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hangingServer answers the first failFast requests with HTTP 500 at once and
// then holds every later request open until the client gives up.
func hangingServer(t *testing.T, failFast int32) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= failFast {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(20 * time.Second):
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func deadlinePipeline(total time.Duration) *Pipeline {
	return NewPipeline(PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
		ChromePath:      chromeDisabled,
		JinaDisabled:    true,
		TotalTimeout:    total,
	})
}

// Tiers that each block past their share: the call must return near the
// overall deadline with the typed network error that names the tiers tried.
func TestScrape_TotalDeadline_TypedErrorNamesTiers(t *testing.T) {
	ts := hangingServer(t, 1) // markdown fails fast, the next tier hangs
	p := deadlinePipeline(time.Second)

	start := time.Now()
	res, err := p.Scrape(context.Background(), ts.URL, 50000)
	elapsed := time.Since(start)

	if res != nil {
		t.Fatalf("expected no result, got %+v", res)
	}
	var se *ScrapeError
	if !errors.As(err, &se) {
		t.Fatalf("expected *ScrapeError, got %T: %v", err, err)
	}
	if se.Kind != ErrNetwork {
		t.Errorf("Kind = %v, want ErrNetwork", se.Kind)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the error to unwrap to context.DeadlineExceeded")
	}
	if !strings.Contains(se.Message, "timed out after 1s") || !strings.Contains(se.Message, "markdown: HTTP 500") {
		t.Errorf("message should give the limit and the tiers tried, got: %s", se.Message)
	}
	if elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("returned after %s, want about 1s", elapsed)
	}
}

// Even when no tier answers at all, the call ends at the deadline, not after
// the per-tier timeouts add up.
func TestScrape_TotalDeadline_AllTiersHang(t *testing.T) {
	ts := hangingServer(t, 0)
	p := deadlinePipeline(time.Second)

	start := time.Now()
	_, err := p.Scrape(context.Background(), ts.URL, 50000)
	elapsed := time.Since(start)

	var se *ScrapeError
	if !errors.As(err, &se) || se.Kind != ErrNetwork {
		t.Fatalf("expected ErrNetwork ScrapeError, got %T: %v", err, err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("returned after %s, want about 1s", elapsed)
	}
}

// ScrapeRaw shares the same deadline.
func TestScrapeRaw_TotalDeadline(t *testing.T) {
	ts := hangingServer(t, 0)
	p := deadlinePipeline(time.Second)

	start := time.Now()
	_, err := p.ScrapeRaw(context.Background(), ts.URL, 50000)
	var se *ScrapeError
	if !errors.As(err, &se) || se.Kind != ErrNetwork {
		t.Fatalf("expected ErrNetwork ScrapeError, got %T: %v", err, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("returned after %s, want about 1s", elapsed)
	}
}

// A scrape that finishes in time is not touched by the deadline.
func TestScrape_TotalDeadline_FastScrapeUnaffected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Ok</title></head><body><article>%s</article></body></html>`,
			strings.Repeat("Good content from the server. ", 20))
	}))
	defer ts.Close()
	p := deadlinePipeline(30 * time.Second)

	res, err := p.Scrape(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(res.Content, "Good content") {
		t.Errorf("unexpected content: %q", res.Content)
	}
}

// The caller's own cancellation or deadline still returns promptly and stays
// the bare context error, not the typed timeout.
func TestScrape_TotalDeadline_CallerContextStillWins(t *testing.T) {
	ts := hangingServer(t, 0)
	p := deadlinePipeline(time.Minute)

	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(200*time.Millisecond, cancel)
		start := time.Now()
		_, err := p.Scrape(ctx, ts.URL, 50000)
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("cancel took %s to return", elapsed)
		}
		var se *ScrapeError
		if errors.As(err, &se) && strings.Contains(se.Message, "timed out after") {
			t.Fatalf("caller cancel was reported as the total timeout: %v", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %T: %v", err, err)
		}
	})

	t.Run("caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := p.Scrape(ctx, ts.URL, 50000)
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("caller deadline took %s to return", elapsed)
		}
		var se *ScrapeError
		if errors.As(err, &se) && strings.Contains(se.Message, "timed out after") {
			t.Fatalf("caller deadline was reported as the total timeout: %v", err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %T: %v", err, err)
		}
	})
}

// A pre-canceled ctx keeps returning the bare context.Canceled value.
func TestScrape_TotalDeadline_BareCanceledPreserved(t *testing.T) {
	p := deadlinePipeline(time.Minute)
	p.semaphore <- struct{}{}
	defer func() { <-p.semaphore }()
	p.semaphore <- struct{}{}
	defer func() { <-p.semaphore }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Scrape(ctx, "https://example.com", 1000); err != context.Canceled {
		t.Errorf("expected bare context.Canceled, got %T: %v", err, err)
	}
}

// The profile path uses the same wrapper: any body that blocks on its context
// is cut at the deadline and typed.
func TestWithTotalDeadline_BlockingBody(t *testing.T) {
	p := deadlinePipeline(500 * time.Millisecond)
	start := time.Now()
	_, err := p.withTotalDeadline(context.Background(), "https://example.com/x", func(ctx context.Context) (*ScrapeResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	var se *ScrapeError
	if !errors.As(err, &se) || se.Kind != ErrNetwork {
		t.Fatalf("expected ErrNetwork ScrapeError, got %T: %v", err, err)
	}
	if !strings.Contains(se.Message, "timed out after 500ms") {
		t.Errorf("message = %q", se.Message)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s", elapsed)
	}
}

// A definite answer that arrives as the deadline hits keeps its own kind.
func TestWithTotalDeadline_DefiniteErrorKept(t *testing.T) {
	p := deadlinePipeline(100 * time.Millisecond)
	_, err := p.withTotalDeadline(context.Background(), "https://example.com/x", func(ctx context.Context) (*ScrapeResult, error) {
		<-ctx.Done()
		return nil, notFoundError("https://example.com/x", "html", 404)
	})
	var se *ScrapeError
	if !errors.As(err, &se) || se.Kind != ErrNotFound {
		t.Fatalf("expected ErrNotFound kept, got %T: %v", err, err)
	}
}

func TestNewPipeline_TotalTimeoutDefault(t *testing.T) {
	if got := NewPipeline(PipelineConfig{}).config.TotalTimeout; got != DefaultTotalTimeout {
		t.Errorf("default TotalTimeout = %s, want %s", got, DefaultTotalTimeout)
	}
	if got := NewPipeline(PipelineConfig{TotalTimeout: 5 * time.Second}).config.TotalTimeout; got != 5*time.Second {
		t.Errorf("TotalTimeout = %s, want 5s", got)
	}
}

// A caller cancel that lands while the last tier runs leaves the tier loop with
// a composite error, not a context error. The caller's cancellation must still
// win (this was a timing flake in TestScrape_TotalDeadline_CallerContextStillWins).
func TestWithTotalDeadline_CallerCancelDuringLastTier(t *testing.T) {
	p := deadlinePipeline(time.Minute)
	for name, mkCtx := range map[string]func() (context.Context, context.CancelFunc){
		"cancel": func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		"deadline": func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Nanosecond)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := mkCtx()
			defer cancel()
			_, err := p.withTotalDeadline(ctx, "http://example.test", func(context.Context) (*ScrapeResult, error) {
				cancel()
				<-ctx.Done()
				return nil, &ScrapeError{Kind: ErrNetwork, Message: "no content extracted (html: context canceled)"}
			})
			if !errors.Is(err, ctx.Err()) {
				t.Fatalf("got %T: %v, want the caller's %v", err, err, ctx.Err())
			}
			var se *ScrapeError
			if errors.As(err, &se) {
				t.Fatalf("caller context error was wrapped in a ScrapeError: %v", err)
			}
		})
	}
}

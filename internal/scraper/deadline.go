package scraper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultTotalTimeout is the overall deadline for one scrape call when
// PipelineConfig.TotalTimeout is unset (SCRAPE_TOTAL_TIMEOUT default, #853).
// The slowest single tier is 30s, so a normal call fits well inside it.
const DefaultTotalTimeout = 90 * time.Second

// tierAbortError carries the tiers already tried when the tier loop stops on a
// context error. Callers outside the package never see it: withTotalDeadline
// unwraps it back to the bare context error.
type tierAbortError struct {
	cause error
	tried string
}

func (e *tierAbortError) Error() string { return e.cause.Error() }
func (e *tierAbortError) Unwrap() error { return e.cause }

// tierSummary renders one tier outcome the way the composite error does.
func tierSummary(name string, r *ScrapeResult, err error) string {
	switch {
	case err != nil:
		return fmt.Sprintf("%s: %s", name, sanitizeTierError(err))
	case r != nil:
		return fmt.Sprintf("%s: %d bytes", name, len(r.Content))
	default:
		return fmt.Sprintf("%s: empty", name)
	}
}

// withTotalDeadline runs fn under the pipeline's overall deadline. A result
// that arrives in time is returned as is. When only our deadline expired (the
// caller's ctx is still live), a context error or a network failure becomes a
// typed ErrNetwork ScrapeError that names the tiers tried. The caller's own
// cancellation or deadline is returned unchanged, as the bare context error.
func (p *Pipeline) withTotalDeadline(ctx context.Context, url string, fn func(context.Context) (*ScrapeResult, error)) (*ScrapeResult, error) {
	total := p.config.TotalTimeout
	sctx, cancel := context.WithTimeout(ctx, total)
	defer cancel()

	result, err := fn(sctx)
	if err == nil {
		return result, nil
	}

	var tried string
	var abort *tierAbortError
	if errors.As(err, &abort) {
		tried = abort.tried
		err = abort.cause
	}

	ours := ctx.Err() == nil && errors.Is(sctx.Err(), context.DeadlineExceeded)
	if !ours {
		return nil, err
	}
	var se *ScrapeError
	isCtxErr := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
	isNetwork := errors.As(err, &se) && se.Kind == ErrNetwork
	if !isCtxErr && !isNetwork {
		// A definite answer (404, blocked, auth, ...) that finished as the
		// deadline hit is still the better diagnosis.
		return nil, err
	}
	if tried == "" && isNetwork {
		tried = se.Message
	}
	msg := fmt.Sprintf("scrape timed out after %s for %s", total, url)
	if tried != "" {
		msg += " (tried: " + tried + ")"
	}
	return nil, &ScrapeError{Kind: ErrNetwork, Message: strings.TrimSpace(msg), Cause: context.DeadlineExceeded, URL: url}
}

// Scrape fetches rawURL through the tier ladder (or a native route) under one
// overall deadline, PipelineConfig.TotalTimeout.
func (p *Pipeline) Scrape(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	return p.withTotalDeadline(ctx, rawURL, func(c context.Context) (*ScrapeResult, error) {
		return p.scrape(c, rawURL, maxLength)
	})
}

// ScrapeRaw is Scrape in raw mode, under the same overall deadline.
func (p *Pipeline) ScrapeRaw(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	return p.withTotalDeadline(ctx, rawURL, func(c context.Context) (*ScrapeResult, error) {
		return p.scrapeRaw(c, rawURL, maxLength)
	})
}

// ScrapeWithProfile is the saved-profile browser scrape, under the same
// overall deadline.
func (p *Pipeline) ScrapeWithProfile(ctx context.Context, rawURL string, maxLength int, spec ProfileSpec) (*ScrapeResult, error) {
	return p.withTotalDeadline(ctx, rawURL, func(c context.Context) (*ScrapeResult, error) {
		return p.scrapeWithProfile(c, rawURL, maxLength, spec)
	})
}

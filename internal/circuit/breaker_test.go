package circuit

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func TestNewDefaults(t *testing.T) {
	b := New(Config{})
	if b.config.FailureThreshold != 5 {
		t.Errorf("expected default FailureThreshold=5, got %d", b.config.FailureThreshold)
	}
	if b.config.ResetTimeout != 60 {
		t.Errorf("expected default ResetTimeout=60, got %d", b.config.ResetTimeout)
	}
	if b.config.HalfOpenAttempts != 1 {
		t.Errorf("expected default HalfOpenAttempts=1, got %d", b.config.HalfOpenAttempts)
	}
}

func TestClosedStatePassesThrough(t *testing.T) {
	b := New(Config{FailureThreshold: 3, ResetTimeout: 1})

	called := false
	err := b.Execute(func() error {
		called = true
		return nil
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Fatal("function was not called in closed state")
	}
	if b.State() != StateClosed {
		t.Errorf("expected state Closed, got %v", b.State())
	}
}

func TestClosedStateReturnsErrors(t *testing.T) {
	b := New(Config{FailureThreshold: 3, ResetTimeout: 1})

	err := b.Execute(func() error {
		return errTest
	})

	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
	// Should still be closed (only 1 failure, threshold is 3)
	if b.State() != StateClosed {
		t.Errorf("expected state Closed after single failure, got %v", b.State())
	}
}

func TestOpensAfterThresholdFailures(t *testing.T) {
	b := New(Config{FailureThreshold: 3, ResetTimeout: 1})

	for i := 0; i < 3; i++ {
		_ = b.Execute(func() error { return errTest })
	}

	if b.State() != StateOpen {
		t.Fatalf("expected state Open after %d failures, got %v", 3, b.State())
	}

	// Subsequent calls should be rejected without calling the function
	called := false
	err := b.Execute(func() error {
		called = true
		return nil
	})

	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
	if called {
		t.Error("function should not be called when circuit is open")
	}
}

func TestHalfOpenAfterTimeout(t *testing.T) {
	b := New(Config{FailureThreshold: 2, ResetTimeout: 1, HalfOpenAttempts: 1})

	// Trip the breaker
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errTest })
	}
	if b.State() != StateOpen {
		t.Fatalf("expected Open, got %v", b.State())
	}

	// Wait for reset timeout to elapse
	time.Sleep(1100 * time.Millisecond)

	// Next call should be allowed (transitions to half-open)
	called := false
	err := b.Execute(func() error {
		called = true
		return nil
	})

	if err != nil {
		t.Fatalf("expected no error in half-open, got %v", err)
	}
	if !called {
		t.Error("function should be called in half-open state")
	}
	// After success, should be closed again
	if b.State() != StateClosed {
		t.Errorf("expected Closed after half-open success, got %v", b.State())
	}
}

func TestHalfOpenFailureReopensCircuit(t *testing.T) {
	b := New(Config{FailureThreshold: 2, ResetTimeout: 1, HalfOpenAttempts: 1})

	// Trip the breaker
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errTest })
	}

	// Wait for reset timeout
	time.Sleep(1100 * time.Millisecond)

	// Fail in half-open state
	err := b.Execute(func() error { return errTest })
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}

	// Should be back to open
	if b.State() != StateOpen {
		t.Errorf("expected Open after half-open failure, got %v", b.State())
	}
}

func TestHalfOpenLimitsAttempts(t *testing.T) {
	b := New(Config{FailureThreshold: 2, ResetTimeout: 1, HalfOpenAttempts: 1})

	// Trip the breaker
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errTest })
	}

	// Wait for reset timeout
	time.Sleep(1100 * time.Millisecond)

	// First call in half-open should work (fail it to keep in half-open → open)
	_ = b.Execute(func() error { return errTest })

	// Circuit should be open again, rejecting calls
	err := b.Execute(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen after half-open attempts exhausted, got %v", err)
	}
}

func TestResetManually(t *testing.T) {
	b := New(Config{FailureThreshold: 2, ResetTimeout: 60})

	// Trip the breaker
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errTest })
	}
	if b.State() != StateOpen {
		t.Fatalf("expected Open, got %v", b.State())
	}

	b.Reset()

	if b.State() != StateClosed {
		t.Errorf("expected Closed after Reset, got %v", b.State())
	}

	// Should be able to execute again
	err := b.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("expected no error after reset, got %v", err)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	b := New(Config{FailureThreshold: 3, ResetTimeout: 1})

	// Two failures (below threshold)
	_ = b.Execute(func() error { return errTest })
	_ = b.Execute(func() error { return errTest })

	// One success resets the counter
	_ = b.Execute(func() error { return nil })

	// Two more failures should NOT trip the breaker (counter was reset)
	_ = b.Execute(func() error { return errTest })
	_ = b.Execute(func() error { return errTest })

	if b.State() != StateClosed {
		t.Errorf("expected Closed (counter should have been reset), got %v", b.State())
	}
}

// TestRateLimitOpensImmediately (#276): a single ErrRateLimit failure opens
// the circuit even though FailureThreshold is far from reached.
func TestRateLimitOpensImmediately(t *testing.T) {
	b := New(Config{FailureThreshold: 5, ResetTimeout: 60})

	err := b.Execute(func() error { return ErrRateLimit })
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
	if b.State() != StateOpen {
		t.Fatalf("expected Open after one rate-limit failure, got %v", b.State())
	}

	called := false
	err = b.Execute(func() error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
	if called {
		t.Error("function should not be called when circuit is open")
	}
}

// TestRateLimitDoesNotIncrementFailureCounter (#276): an immediate-open on
// ErrRateLimit must not touch the generic failures counter — otherwise a
// rate-limit would count toward, and prematurely trip, the normal threshold.
func TestRateLimitDoesNotIncrementFailureCounter(t *testing.T) {
	b := New(Config{FailureThreshold: 5, ResetTimeout: 60})

	_ = b.Execute(func() error { return ErrRateLimit })
	if b.State() != StateOpen {
		t.Fatalf("expected Open after rate-limit, got %v", b.State())
	}

	b.Reset()

	for i := 0; i < 4; i++ {
		_ = b.Execute(func() error { return errTest })
	}
	if b.State() != StateClosed {
		t.Errorf("expected Closed after 4/5 generic failures, got %v (failures counter may have been polluted by the earlier rate-limit)", b.State())
	}
}

// TestWrappedErrRateLimitOpensImmediately (#276): errors.Is unwrapping must
// see through a provider's %w wrap (e.g. "brave: rate limited: %w").
func TestWrappedErrRateLimitOpensImmediately(t *testing.T) {
	b := New(Config{FailureThreshold: 5, ResetTimeout: 60})

	wrapped := fmt.Errorf("brave: rate limited: %w", ErrRateLimit)
	_ = b.Execute(func() error { return wrapped })

	if b.State() != StateOpen {
		t.Fatalf("expected Open after wrapped ErrRateLimit, got %v", b.State())
	}
}

// TestNormalThresholdUnchanged (#276): non-rate-limit errors must still
// require FailureThreshold occurrences before opening — the new immediate-open
// path must not affect the existing generic-failure behavior.
func TestNormalThresholdUnchanged(t *testing.T) {
	b := New(Config{FailureThreshold: 5, ResetTimeout: 60})

	for i := 0; i < 4; i++ {
		_ = b.Execute(func() error { return errTest })
	}
	if b.State() != StateClosed {
		t.Fatalf("expected Closed after 4/5 failures, got %v", b.State())
	}

	_ = b.Execute(func() error { return errTest })
	if b.State() != StateOpen {
		t.Fatalf("expected Open after 5/5 failures, got %v", b.State())
	}
}

// TestHalfOpenRateLimitReopensImmediately (#276): a rate-limit hit while
// half-open must reopen the circuit on the first attempt, not require a
// second half-open failure.
func TestHalfOpenRateLimitReopensImmediately(t *testing.T) {
	b := New(Config{FailureThreshold: 2, ResetTimeout: 1, HalfOpenAttempts: 1})

	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errTest })
	}
	if b.State() != StateOpen {
		t.Fatalf("expected Open, got %v", b.State())
	}

	time.Sleep(1100 * time.Millisecond)

	err := b.Execute(func() error { return ErrRateLimit })
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
	if b.State() != StateOpen {
		t.Fatalf("expected Open after half-open rate-limit, got %v", b.State())
	}
}

func TestConcurrentAccess(t *testing.T) {
	b := New(Config{FailureThreshold: 100, ResetTimeout: 1})

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = b.Execute(func() error { return nil })
			_ = b.Execute(func() error { return errTest })
			_ = b.State()
		}()
	}

	for i := 0; i < 50; i++ {
		<-done
	}
	// No panics means concurrent access is safe
}

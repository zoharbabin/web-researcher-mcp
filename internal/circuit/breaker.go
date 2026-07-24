package circuit

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker open")

// ErrRateLimit is the sentinel a provider wraps its 429 error with so the
// circuit breaker opens immediately on the first rate-limit, without waiting
// for FailureThreshold generic failures to accumulate.
var ErrRateLimit = errors.New("rate limited")

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

type Config struct {
	FailureThreshold int
	ResetTimeout     int // seconds
	HalfOpenAttempts int
}

type Breaker struct {
	mu               sync.Mutex
	state            State
	failures         int
	lastFailure      time.Time
	halfOpenAttempts int
	config           Config
}

func New(cfg Config) *Breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 60
	}
	if cfg.HalfOpenAttempts <= 0 {
		cfg.HalfOpenAttempts = 1
	}
	return &Breaker{config: cfg}
}

func (b *Breaker) Execute(fn func() error) error {
	if !b.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()

	if err != nil {
		b.onFailure(err)
		return err
	}

	b.onSuccess()
	return nil
}

func (b *Breaker) allowRequest() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(b.lastFailure) > time.Duration(b.config.ResetTimeout)*time.Second {
			b.state = StateHalfOpen
			b.halfOpenAttempts = 0
			return true
		}
		return false
	case StateHalfOpen:
		return b.halfOpenAttempts < b.config.HalfOpenAttempts
	}
	return false
}

func (b *Breaker) onSuccess() {
	b.state = StateClosed
	b.failures = 0
	b.halfOpenAttempts = 0
}

// onFailure records a failure and updates the circuit state. A wrapped
// ErrRateLimit opens the circuit immediately, bypassing FailureThreshold — a
// 429 is an unambiguous saturation signal, unlike a generic transient error.
func (b *Breaker) onFailure(err error) {
	b.lastFailure = time.Now()

	if errors.Is(err, ErrRateLimit) {
		b.state = StateOpen
		return
	}

	b.failures++
	switch b.state {
	case StateClosed:
		if b.failures >= b.config.FailureThreshold {
			b.state = StateOpen
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.halfOpenAttempts++
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = StateClosed
	b.failures = 0
}

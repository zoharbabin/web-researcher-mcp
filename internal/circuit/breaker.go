package circuit

import (
	"errors"
	"math/rand"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker open")

// ErrRateLimit is the sentinel a provider wraps its 429 error with so the
// circuit breaker opens immediately on the first rate-limit, without waiting
// for FailureThreshold generic failures to accumulate.
var ErrRateLimit = errors.New("rate limited")

// ErrNonTripping is the sentinel a provider wraps an error with when it must
// never count toward the breaker's failure threshold at all — e.g. a 402
// payment-required response (credits exhausted), which is a billing/quota
// state, not a service-health signal. Execute still returns the original error
// to the caller; only the breaker's internal accounting ignores it.
var ErrNonTripping = errors.New("non-tripping error")

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
	resetJitter      time.Duration
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
		if time.Since(b.lastFailure) > time.Duration(b.config.ResetTimeout)*time.Second+b.resetJitter {
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
	b.resetJitter = 0
}

// onFailure records a failure and updates the circuit state. A wrapped
// ErrRateLimit opens the circuit immediately, bypassing FailureThreshold — a
// 429 is an unambiguous saturation signal, unlike a generic transient error. A
// wrapped ErrNonTripping is recorded as a failure timestamp but never
// increments the counter or changes state — it is not a service-health signal.
func (b *Breaker) onFailure(err error) {
	b.lastFailure = time.Now()

	if errors.Is(err, ErrNonTripping) {
		return
	}

	if errors.Is(err, ErrRateLimit) {
		b.state = StateOpen
		b.resetJitter = b.newResetJitter()
		return
	}

	b.failures++
	switch b.state {
	case StateClosed:
		if b.failures >= b.config.FailureThreshold {
			b.state = StateOpen
			b.resetJitter = b.newResetJitter()
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.halfOpenAttempts++
		b.resetJitter = b.newResetJitter()
	}
}

// newResetJitter returns a random duration in [0, ResetTimeout/5) — up to 20%
// of the configured reset timeout — added to the half-open eligibility check.
// Without it, every breaker tripped by the same outage becomes eligible to
// probe the recovering provider at the exact same instant, producing a
// synchronized retry thundering-herd (#471).
func (b *Breaker) newResetJitter() time.Duration {
	maxJitter := time.Duration(b.config.ResetTimeout) * time.Second / 5
	if maxJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(maxJitter))) // #nosec G404 -- jitter for retry timing, not security-sensitive
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
	b.resetJitter = 0
}

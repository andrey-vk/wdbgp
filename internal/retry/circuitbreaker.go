package retry

import (
	"context"
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed State = iota
	StateHalfOpen
	StateOpen
)

// String returns a string representation of the state.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

// Config holds circuit breaker configuration.
type CircuitBreakerConfig struct {
	// Name identifies the circuit breaker (for logging).
	Name string
	// FailureThreshold is the number of failures before opening the circuit.
	FailureThreshold int
	// SuccessThreshold is the number of consecutive successes needed in half-open state to close.
	SuccessThreshold int
	// OpenTimeout is how long the circuit stays open before moving to half-open.
	OpenTimeout time.Duration
	// HalfOpenMaxAttempts is the maximum number of attempts allowed in half-open state.
	HalfOpenMaxAttempts int
}

// DefaultCircuitBreakerConfig provides reasonable defaults.
var DefaultCircuitBreakerConfig = CircuitBreakerConfig{
	Name:                "default",
	FailureThreshold:    5,
	SuccessThreshold:    2,
	OpenTimeout:         30 * time.Second,
	HalfOpenMaxAttempts: 1,
}

// HTTPCircuitBreakerConfig provides defaults for HTTP operations.
var HTTPCircuitBreakerConfig = CircuitBreakerConfig{
	Name:                "http",
	FailureThreshold:    3,
	SuccessThreshold:    1,
	OpenTimeout:         60 * time.Second,
	HalfOpenMaxAttempts: 1,
}

// DatabaseCircuitBreakerConfig provides defaults for database operations.
var DatabaseCircuitBreakerConfig = CircuitBreakerConfig{
	Name:                "database",
	FailureThreshold:    10,
	SuccessThreshold:    3,
	OpenTimeout:         10 * time.Second,
	HalfOpenMaxAttempts: 1,
}

// CircuitBreaker implements the circuit breaker pattern.
type CircuitBreaker struct {
	mu  sync.RWMutex
	cfg CircuitBreakerConfig

	state         State
	failureCount  int
	successCount  int
	halfOpenCount int
	lastFailure   time.Time
	openUntil     time.Time
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		cfg:   cfg,
		state: StateClosed,
	}
}

// Execute runs fn through the circuit breaker.
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	// Check if circuit is open
	if !cb.allowRequest() {
		return errors.New("circuit breaker is open")
	}

	// Execute the function
	err := fn()

	// Record result
	cb.recordResult(err)

	return err
}

// ExecuteWithResult runs fn through the circuit breaker and returns a result.
// This is a type-safe wrapper that can be used with concrete types.
func (cb *CircuitBreaker) ExecuteWithResult(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
	// Check if circuit is open
	if !cb.allowRequest() {
		return nil, errors.New("circuit breaker is open")
	}

	// Execute the function
	result, err := fn()

	// Record result
	cb.recordResult(err)

	return result, err
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Stats returns circuit breaker statistics.
func (cb *CircuitBreaker) Stats() (failureCount, successCount int, lastFailure time.Time) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failureCount, cb.successCount, cb.lastFailure
}

// Reset resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.reset()
}

// allowRequest checks if a request should be allowed.
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		return true

	case StateHalfOpen:
		if cb.halfOpenCount >= cb.cfg.HalfOpenMaxAttempts {
			return false
		}
		cb.halfOpenCount++
		return true

	case StateOpen:
		if now.After(cb.openUntil) {
			// Move to half-open state
			cb.state = StateHalfOpen
			cb.halfOpenCount = 0
			cb.successCount = 0
			return true
		}
		return false

	default:
		return false
	}
}

// recordResult records the result of an operation.
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		if err != nil && TransientError(err) {
			cb.failureCount++
			cb.lastFailure = now

			if cb.failureCount >= cb.cfg.FailureThreshold {
				// Open the circuit
				cb.state = StateOpen
				cb.openUntil = now.Add(cb.cfg.OpenTimeout)
			}
		} else {
			// Reset failure count on success
			cb.failureCount = 0
			cb.successCount++
		}

	case StateHalfOpen:
		if err != nil && TransientError(err) {
			// Failure in half-open state -> back to open
			cb.state = StateOpen
			cb.openUntil = now.Add(cb.cfg.OpenTimeout)
			cb.failureCount++
			cb.lastFailure = now
		} else {
			// Success in half-open state
			cb.successCount++
			if cb.successCount >= cb.cfg.SuccessThreshold {
				// Close the circuit
				cb.state = StateClosed
				cb.failureCount = 0
				cb.successCount = 0
			}
		}

	case StateOpen:
		// Nothing to do in open state
	}
}

// reset resets the circuit breaker (internal, assumes lock is held).
func (cb *CircuitBreaker) reset() {
	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenCount = 0
	cb.lastFailure = time.Time{}
	cb.openUntil = time.Time{}
}

// Manager manages multiple circuit breakers.
type CircuitBreakerManager struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
}

// NewCircuitBreakerManager creates a new circuit breaker manager.
func NewCircuitBreakerManager() *CircuitBreakerManager {
	return &CircuitBreakerManager{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// GetOrCreate gets an existing circuit breaker or creates a new one.
func (m *CircuitBreakerManager) GetOrCreate(name string, cfg CircuitBreakerConfig) *CircuitBreaker {
	m.mu.Lock()
	defer m.mu.Unlock()

	if breaker, exists := m.breakers[name]; exists {
		return breaker
	}

	cfg.Name = name
	breaker := NewCircuitBreaker(cfg)
	m.breakers[name] = breaker
	return breaker
}

// Get returns an existing circuit breaker or nil.
func (m *CircuitBreakerManager) Get(name string) *CircuitBreaker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.breakers[name]
}

// ResetAll resets all circuit breakers.
func (m *CircuitBreakerManager) ResetAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, breaker := range m.breakers {
		breaker.Reset()
	}
}

// Global circuit breaker manager instance.
var (
	globalCircuitBreakerManager = NewCircuitBreakerManager()
)

// GetGlobalCircuitBreaker gets or creates a global circuit breaker.
func GetGlobalCircuitBreaker(name string, cfg CircuitBreakerConfig) *CircuitBreaker {
	return globalCircuitBreakerManager.GetOrCreate(name, cfg)
}

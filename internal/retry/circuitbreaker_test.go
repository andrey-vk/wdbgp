//nolint:errcheck // test file, errors in cleanup intentionally ignored
package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedState(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig)

	if cb.State() != StateClosed {
		t.Fatalf("expected closed state, got %v", cb.State())
	}

	// Successful execution
	callCount := 0
	err := cb.Execute(context.Background(), func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
	if cb.State() != StateClosed {
		t.Fatalf("expected closed state, got %v", cb.State())
	}
}

func TestCircuitBreaker_OpenOnFailures(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		OpenTimeout:      100 * time.Millisecond,
	})

	// First failure
	err := cb.Execute(context.Background(), func() error {
		return errors.New("connection timeout") // This should be recognized as transient
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if cb.State() != StateClosed {
		t.Fatalf("expected closed state after 1 failure, got %v", cb.State())
	}

	// Second failure (should open circuit)
	err = cb.Execute(context.Background(), func() error {
		return errors.New("connection timeout") // This should be recognized as transient
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if cb.State() != StateOpen {
		t.Fatalf("expected open state after 2 failures, got %v", cb.State())
	}

	// Third attempt should be rejected immediately
	err = cb.Execute(context.Background(), func() error {
		t.Fatal("should not be called when circuit is open")
		return nil
	})

	if err == nil || err.Error() != "circuit breaker is open" {
		t.Fatalf("expected 'circuit breaker is open' error, got: %v", err)
	}
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenTimeout:      10 * time.Millisecond,
	})

	// Open the circuit
	for i := 0; i < 2; i++ {
		cb.Execute(context.Background(), func() error { //nolint:gosec // test code, errors intentionally ignored
			return errors.New("connection timeout")
		})
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected open state, got %v", cb.State())
	}

	// Wait for circuit to move to half-open
	// Need to wait longer than OpenTimeout
	time.Sleep(30 * time.Millisecond)

	// Should be in half-open state now (check by trying to execute)
	callCount := 0
	err := cb.Execute(context.Background(), func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Fatalf("expected no error in half-open state, got: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call in half-open state, got %d", callCount)
	}

	// Successful execution in half-open state
	callCount = 0
	err = cb.Execute(context.Background(), func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Should be back to closed state
	if cb.State() != StateClosed {
		t.Fatalf("expected closed state after success, got %v", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		OpenTimeout:      10 * time.Millisecond,
	})

	// Open the circuit
	for i := 0; i < 2; i++ {
		cb.Execute(context.Background(), func() error { //nolint:gosec // test code, errors intentionally ignored
			return errors.New("connection timeout")
		})
	}

	// Wait for circuit to move to half-open
	time.Sleep(20 * time.Millisecond)

	// Failure in half-open state
	err := cb.Execute(context.Background(), func() error {
		return errors.New("connection timeout")
	})

	if err == nil {
		t.Fatal("expected error")
	}

	// Should be back to open state
	if cb.State() != StateOpen {
		t.Fatalf("expected open state after half-open failure, got %v", cb.State())
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig)

	// Open the circuit
	for i := 0; i < 5; i++ {
		cb.Execute(context.Background(), func() error { //nolint:gosec // test code, errors intentionally ignored
			return errors.New("connection timeout")
		})
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected open state, got %v", cb.State())
	}

	// Reset
	cb.Reset()

	if cb.State() != StateClosed {
		t.Fatalf("expected closed state after reset, got %v", cb.State())
	}

	// Should accept requests again
	callCount := 0
	err := cb.Execute(context.Background(), func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestCircuitBreakerManager(t *testing.T) {
	manager := NewCircuitBreakerManager()

	// Get or create circuit breaker
	cb1 := manager.GetOrCreate("test1", DefaultCircuitBreakerConfig)
	if cb1 == nil {
		t.Fatal("expected circuit breaker")
	}

	// Get existing circuit breaker
	cb2 := manager.GetOrCreate("test1", DefaultCircuitBreakerConfig)
	if cb1 != cb2 {
		t.Fatal("expected same circuit breaker instance")
	}

	// Get non-existent circuit breaker
	cb3 := manager.Get("nonexistent")
	if cb3 != nil {
		t.Fatal("expected nil for non-existent circuit breaker")
	}

	// Reset all
	_ = cb1.Execute(context.Background(), func() error {
		return errors.New("connection timeout")
	})

	manager.ResetAll()

	if cb1.State() != StateClosed {
		t.Fatalf("expected closed state after reset, got %v", cb1.State())
	}
}

package retry

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

// Config holds retry configuration.
type Config struct {
	// MaxAttempts is the maximum number of attempts (including initial).
	MaxAttempts int
	// BaseDelay is the base delay between retries.
	BaseDelay time.Duration
	// MaxDelay is the maximum delay between retries.
	MaxDelay time.Duration
	// JitterFactor adds random jitter to retry delays (0.0 to 1.0).
	JitterFactor float64
}

// DefaultConfig provides reasonable defaults for retry operations.
var DefaultConfig = Config{
	MaxAttempts:  3,
	BaseDelay:    100 * time.Millisecond,
	MaxDelay:     30 * time.Second,
	JitterFactor: 0.25,
}

// HTTPConfig provides defaults for HTTP requests.
var HTTPConfig = Config{
	MaxAttempts:  3,
	BaseDelay:    1 * time.Second,
	MaxDelay:     30 * time.Second,
	JitterFactor: 0.25,
}

// DatabaseConfig provides defaults for database operations.
var DatabaseConfig = Config{
	MaxAttempts:  5,
	BaseDelay:    50 * time.Millisecond,
	MaxDelay:     5 * time.Second,
	JitterFactor: 0.1,
}

// BGPConfig provides defaults for BGP operations.
var BGPConfig = Config{
	MaxAttempts:  10,
	BaseDelay:    2 * time.Second,
	MaxDelay:     60 * time.Second,
	JitterFactor: 0.3,
}

// AlwaysRetry returns true for any error (except context cancellation).
func AlwaysRetry(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// TransientError returns true for errors that are likely transient.
func TransientError(err error) bool {
	if err == nil {
		return false
	}
	
	// Context errors are not transient
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	
	// Check for network errors
	errStr := err.Error()
	switch {
	case containsAny(errStr, "timeout", "timed out", "deadline exceeded", "temporary", "try again"):
		return true
	case containsAny(errStr, "connection refused", "connection reset", "broken pipe"):
		return true
	case containsAny(errStr, "no route to host", "host unreachable", "network unreachable"):
		return true
	case containsAny(errStr, "too many open files", "resource temporarily unavailable"):
		return true
	case containsAny(errStr, "rate limit", "rate exceeded", "too many requests"):
		return true
	case containsAny(errStr, "service unavailable", "server error", "bad gateway", "gateway timeout"):
		return true
	case containsAny(errStr, "sqlite busy", "database is locked", "database locked"):
		return true
	case containsAny(errStr, "transient error"): // For testing
		return true
	default:
		return false
	}
}

// HTTPTransientError returns true for HTTP errors that are likely transient.
func HTTPTransientError(err error) bool {
	if err == nil {
		return false
	}
	
	// Context errors are not transient
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	
	// Check for network errors
	errStr := err.Error()
	switch {
	case containsAny(errStr, "timeout", "timed out", "deadline exceeded", "temporary", "try again"):
		return true
	case containsAny(errStr, "connection refused", "connection reset", "broken pipe"):
		return true
	case containsAny(errStr, "no route to host", "host unreachable", "network unreachable"):
		return true
	case containsAny(errStr, "too many open files", "resource temporarily unavailable"):
		return true
	case containsAny(errStr, "rate limit", "rate exceeded", "too many requests"):
		return true
	case containsAny(errStr, "service unavailable", "server error", "bad gateway", "gateway timeout"):
		return true
	case containsAny(errStr, "429", "503", "504"):
		// HTTP status codes: 429 (Too Many Requests), 503 (Service Unavailable), 504 (Gateway Timeout)
		return true
	default:
		return false
	}
}

// Do executes fn with exponential backoff retry.
func Do(ctx context.Context, config Config, fn func() error, shouldRetry func(error) bool) error {
	if config.MaxAttempts < 1 {
		config.MaxAttempts = 1
	}
	
	var lastErr error
	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Execute the function
		err := fn()
		if err == nil {
			return nil
		}
		
		lastErr = err
		
		// Check if we should retry
		if attempt == config.MaxAttempts-1 || !shouldRetry(err) {
			break
		}
		
		// Calculate delay with exponential backoff
		delay := config.BaseDelay * time.Duration(1<<uint(attempt))
		if delay > config.MaxDelay {
			delay = config.MaxDelay
		}
		
		// Add jitter
		if config.JitterFactor > 0 {
			jitter := float64(delay) * config.JitterFactor
			delay += time.Duration(rand.Float64() * jitter)
		}
		
		// Wait for delay or context cancellation
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Continue to next attempt
		}
	}
	
	return lastErr
}

// DoWithResult executes fn with exponential backoff retry and returns a result.
func DoWithResult[T any](ctx context.Context, config Config, fn func() (T, error), shouldRetry func(error) bool) (T, error) {
	if config.MaxAttempts < 1 {
		config.MaxAttempts = 1
	}
	
	var lastErr error
	var zero T
	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Execute the function
		result, err := fn()
		if err == nil {
			return result, nil
		}
		
		lastErr = err
		
		// Check if we should retry
		if attempt == config.MaxAttempts-1 || !shouldRetry(err) {
			break
		}
		
		// Calculate delay with exponential backoff
		delay := config.BaseDelay * time.Duration(1<<uint(attempt))
		if delay > config.MaxDelay {
			delay = config.MaxDelay
		}
		
		// Add jitter
		if config.JitterFactor > 0 {
			jitter := float64(delay) * config.JitterFactor
			delay += time.Duration(rand.Float64() * jitter)
		}
		
		// Wait for delay or context cancellation
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
			// Continue to next attempt
		}
	}
	
	return zero, lastErr
}

// containsAny checks if string contains any of the substrings.
func containsAny(s string, substrings ...string) bool {
	for _, substr := range substrings {
		if contains(s, substr) {
			return true
		}
	}
	return false
}

// contains checks if string contains substring (case-insensitive).
func contains(s, substr string) bool {
	s, substr = lower(s), lower(substr)
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}

// lower returns lowercase string.
func lower(s string) string {
	// Simple ASCII lowercase for performance
	bytes := []byte(s)
	for i, b := range bytes {
		if 'A' <= b && b <= 'Z' {
			bytes[i] = b + ('a' - 'A')
		}
	}
	return string(bytes)
}
package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	
	err := Do(ctx, DefaultConfig, func() error {
		callCount++
		return nil
	}, AlwaysRetry)
	
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestDo_RetryAndSucceed(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	
	err := Do(ctx, Config{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
	}, func() error {
		callCount++
		if callCount < 2 {
			return errors.New("temporary failure")
		}
		return nil
	}, TransientError)
	
	if err != nil {
		t.Fatalf("expected no error after retry, got: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount)
	}
}

func TestDo_RetryAndFail(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	expectedErr := errors.New("persistent failure")
	
	// Custom retry predicate that always retries
	alwaysRetry := func(err error) bool {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	
	err := Do(ctx, Config{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
	}, func() error {
		callCount++
		return expectedErr
	}, alwaysRetry)
	
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got: %v", expectedErr, err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
}

func TestDo_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0
	
	err := Do(ctx, Config{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    1 * time.Second,
	}, func() error {
		callCount++
		if callCount == 1 {
			cancel() // Cancel after first attempt
		}
		return errors.New("failure")
	}, AlwaysRetry)
	
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestDoWithResult_Success(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	expectedResult := "success"
	
	result, err := DoWithResult(ctx, DefaultConfig, func() (string, error) {
		callCount++
		return expectedResult, nil
	}, AlwaysRetry)
	
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != expectedResult {
		t.Fatalf("expected result %q, got %q", expectedResult, result)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestTransientError_Detection(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "timeout error",
			err:      errors.New("connection timeout"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "rate limit",
			err:      errors.New("rate limit exceeded"),
			expected: true,
		},
		{
			name:     "service unavailable",
			err:      errors.New("service unavailable"),
			expected: true,
		},
		{
			name:     "sqlite busy",
			err:      errors.New("sqlite busy"),
			expected: true,
		},
		{
			name:     "context cancelled",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: false,
		},
		{
			name:     "permanent error",
			err:      errors.New("invalid syntax"),
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TransientError(tt.err)
			if result != tt.expected {
				t.Errorf("TransientError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestHTTPTransientError_Detection(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "HTTP 429",
			err:      errors.New("HTTP 429 Too Many Requests"),
			expected: true,
		},
		{
			name:     "HTTP 503",
			err:      errors.New("HTTP 503 Service Unavailable"),
			expected: true,
		},
		{
			name:     "HTTP 504",
			err:      errors.New("HTTP 504 Gateway Timeout"),
			expected: true,
		},
		{
			name:     "HTTP 200",
			err:      errors.New("HTTP 200 OK"),
			expected: false,
		},
		{
			name:     "HTTP 404",
			err:      errors.New("HTTP 404 Not Found"),
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HTTPTransientError(tt.err)
			if result != tt.expected {
				t.Errorf("HTTPTransientError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestExponentialBackoffWithJitter(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	startTime := time.Now()
	
	err := Do(ctx, Config{
		MaxAttempts:  4,
		BaseDelay:    50 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		JitterFactor: 0.5,
	}, func() error {
		callCount++
		if callCount < 4 {
			return errors.New("temporary failure")
		}
		return nil
	}, AlwaysRetry)
	
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	
	// Verify it took some time (due to backoff)
	elapsed := time.Since(startTime)
	if elapsed < 100*time.Millisecond {
		t.Fatalf("expected at least 100ms of backoff, got %v", elapsed)
	}
	
	// But not too much time (max delay would be 1s, but we have jitter)
	if elapsed > 3*time.Second {
		t.Fatalf("expected less than 3s, got %v", elapsed)
	}
}
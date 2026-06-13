package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerCreation(t *testing.T) {
	// Test creating logger with different levels
	logger := NewLogger(slog.LevelDebug, "text")
	if logger == nil {
		t.Fatal("logger is nil")
	}
	
	// Test creating JSON logger
	jsonLogger := NewLogger(slog.LevelInfo, "json")
	if jsonLogger == nil {
		t.Fatal("JSON logger is nil")
	}
}

func TestLogLevelParsing(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"invalid", slog.LevelInfo}, // default
		{"", slog.LevelInfo},        // default
	}
	
	for _, test := range tests {
		level := parseLogLevel(test.input)
		if level != test.expected {
			t.Errorf("parseLogLevel(%q) = %v, want %v", test.input, level, test.expected)
		}
	}
}

func TestLogFormatParsing(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"text", "text"},
		{"TEXT", "text"},
		{"json", "json"},
		{"JSON", "json"},
		{"invalid", "text"}, // default
		{"", "text"},        // default
	}
	
	for _, test := range tests {
		format := parseLogFormat(test.input)
		// parseLogFormat returns lowercase
		expected := strings.ToLower(test.expected)
		if format != expected {
			t.Errorf("parseLogFormat(%q) = %v, want %v", test.input, format, expected)
		}
	}
}

func TestContextLogger(t *testing.T) {
	// Test getting logger from context
	ctx := context.Background()
	logger1 := FromContext(ctx)
	if logger1 == nil {
		t.Fatal("FromContext returned nil")
	}
	
	// Test adding logger to context
	newLogger := NewLogger(slog.LevelDebug, "text")
	ctxWithLogger := newLogger.WithContext(ctx)
	
	logger2 := FromContext(ctxWithLogger)
	if logger2 != newLogger {
		t.Error("FromContext didn't return the logger added to context")
	}
	
	// Test request ID context
	ctxWithRequestID := WithRequestIDContext(ctx, "test-request-id")
	requestID := RequestIDFromContext(ctxWithRequestID)
	if requestID != "test-request-id" {
		t.Errorf("RequestIDFromContext = %q, want %q", requestID, "test-request-id")
	}
	
	logger3 := FromContext(ctxWithRequestID)
	if logger3 == nil {
		t.Fatal("FromContext returned nil for request ID context")
	}
}

func TestHelperFunctions(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	testLogger := &Logger{slog.New(handler)}
	
	// Temporarily replace default logger
	oldDefault := defaultLogger
	defaultLogger = testLogger
	defer func() { defaultLogger = oldDefault }()
	
	// Test helper functions
	Debug("test debug")
	Info("test info")
	Warn("test warn")
	Error("test error")
	
	output := buf.String()
	if !strings.Contains(output, "test debug") {
		t.Error("Debug log not found in output")
	}
	if !strings.Contains(output, "test info") {
		t.Error("Info log not found in output")
	}
	if !strings.Contains(output, "test warn") {
		t.Error("Warn log not found in output")
	}
	if !strings.Contains(output, "test error") {
		t.Error("Error log not found in output")
	}
}
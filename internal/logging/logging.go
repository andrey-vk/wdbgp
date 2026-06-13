package logging

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

type Logger struct {
	*slog.Logger
}

type contextKey string

const (
	requestIDKey contextKey = "request_id"
)

var (
	defaultLogger *Logger
)

func init() {
	// Initialize with default settings (will be reconfigured later with actual config)
	level := parseLogLevel("INFO")
	format := parseLogFormat("text")
	defaultLogger = NewLogger(level, format)
}

// Configure configures the default logger based on configuration
func Configure(logLevel, logFormat string) {
	level := parseLogLevel(logLevel)
	format := parseLogFormat(logFormat)
	defaultLogger = NewLogger(level, format)
}

// NewLogger creates a new logger with the specified level and format
func NewLogger(level slog.Level, format string) *Logger {
	var handler slog.Handler
	
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize attribute names if needed
			if a.Key == slog.TimeKey {
				// Format time to RFC3339 with milliseconds
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format("2006-01-02T15:04:05.000Z07:00"))
				}
			}
			return a
		},
	}
	
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	
	return &Logger{slog.New(handler)}
}

// Default returns the default logger instance
func Default() *Logger {
	return defaultLogger
}

// WithRequestID returns a new logger with request ID added to all logs
func (l *Logger) WithRequestID(requestID string) *Logger {
	return &Logger{l.Logger.With("request_id", requestID)}
}

// FromContext extracts logger from context, with request ID if available
func FromContext(ctx context.Context) *Logger {
	if logger, ok := ctx.Value(contextKey("logger")).(*Logger); ok && logger != nil {
		return logger
	}
	
	// Check for request ID in context
	if requestID, ok := ctx.Value(requestIDKey).(string); ok && requestID != "" {
		return defaultLogger.WithRequestID(requestID)
	}
	
	return defaultLogger
}

// WithContext creates a new context with the logger embedded
func (l *Logger) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey("logger"), l)
}

// WithRequestIDContext creates a new context with request ID
func WithRequestIDContext(ctx context.Context, requestID string) context.Context {
	ctx = context.WithValue(ctx, requestIDKey, requestID)
	return WithLogger(ctx, defaultLogger.WithRequestID(requestID))
}

// WithLogger adds a logger to the context
func WithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, contextKey("logger"), logger)
}

// RequestIDFromContext extracts request ID from context
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// Log levels
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// Helper functions for common log operations
func Debug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

func Info(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

func Warn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

func Error(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
}

func Fatal(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
	log.Fatal(msg)
}

func Fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	defaultLogger.Error(msg)
	log.Fatal(msg)
}

// Parse log level from string
func parseLogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Parse log format from string
func parseLogFormat(format string) string {
	lowerFormat := strings.ToLower(format)
	switch lowerFormat {
	case "json", "text":
		return lowerFormat
	default:
		return "text"
	}
}

// HTTPMiddleware creates middleware that adds request ID and logger to context
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate or extract request ID
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			// Generate a simple request ID
			requestID = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		
		// Create context with request ID and logger
		ctx := WithRequestIDContext(r.Context(), requestID)
		logger := FromContext(ctx)
		
		// Log the request
		start := time.Now()
		logger.Info("HTTP request started",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
		
		// Create response wrapper to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		
		// Process request with enhanced context
		next.ServeHTTP(rw, r.WithContext(ctx))
		
		// Log completion
		duration := time.Since(start)
		level := LevelInfo
		if rw.statusCode >= 500 {
			level = LevelError
		} else if rw.statusCode >= 400 {
			level = LevelWarn
		}
		
		logger.Log(ctx, level, "HTTP request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration_ms", duration.Milliseconds(),
			"bytes_written", rw.bytesWritten,
			"remote_addr", r.RemoteAddr,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written
type responseWriter struct {
	http.ResponseWriter
	statusCode    int
	bytesWritten  int64
	wroteHeader   bool
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	if !rw.wroteHeader {
		rw.statusCode = statusCode
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}
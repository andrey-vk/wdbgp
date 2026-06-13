package errors

import (
	"errors"
	"fmt"
)

// ErrorType represents the type of error.
type ErrorType string

const (
	// TypeSystem represents system-level errors (e.g., database, network).
	TypeSystem ErrorType = "system"
	// TypeUser represents user errors (e.g., invalid input, permissions).
	TypeUser ErrorType = "user"
	// TypeTransient represents transient errors that may succeed on retry.
	TypeTransient ErrorType = "transient"
	// TypeFatal represents fatal errors that should not be retried.
	TypeFatal ErrorType = "fatal"
)

// Error represents a structured application error.
type Error struct {
	Type    ErrorType
	Message string
	Err     error
	Context map[string]interface{}
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// Unwrap returns the wrapped error.
func (e *Error) Unwrap() error {
	return e.Err
}

// New creates a new structured error.
func New(t ErrorType, message string) *Error {
	return &Error{
		Type:    t,
		Message: message,
		Context: make(map[string]interface{}),
	}
}

// Wrap wraps an existing error with structured information.
func Wrap(t ErrorType, message string, err error) *Error {
	return &Error{
		Type:    t,
		Message: message,
		Err:     err,
		Context: make(map[string]interface{}),
	}
}

// WithContext adds context to the error.
func (e *Error) WithContext(key string, value interface{}) *Error {
	e.Context[key] = value
	return e
}

// WithContextMap adds multiple context values to the error.
func (e *Error) WithContextMap(context map[string]interface{}) *Error {
	for k, v := range context {
		e.Context[k] = v
	}
	return e
}

// IsSystem returns true if the error is a system error.
func IsSystem(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Type == TypeSystem
	}
	return false
}

// IsUser returns true if the error is a user error.
func IsUser(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Type == TypeUser
	}
	return false
}

// IsTransient returns true if the error is transient.
func IsTransient(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Type == TypeTransient
	}
	return false
}

// IsFatal returns true if the error is fatal.
func IsFatal(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Type == TypeFatal
	}
	return false
}

// SystemError creates a system error.
func SystemError(message string, err error) *Error {
	return Wrap(TypeSystem, message, err)
}

// UserError creates a user error.
func UserError(message string, err error) *Error {
	return Wrap(TypeUser, message, err)
}

// TransientError creates a transient error.
func TransientError(message string, err error) *Error {
	return Wrap(TypeTransient, message, err)
}

// FatalError creates a fatal error.
func FatalError(message string, err error) *Error {
	return Wrap(TypeFatal, message, err)
}

// HTTPError represents an HTTP-related error.
type HTTPError struct {
	*Error
	StatusCode int
	URL        string
	Method     string
}

// NewHTTPError creates a new HTTP error.
func NewHTTPError(statusCode int, url, method, message string, err error) *HTTPError {
	return &HTTPError{
		Error:      Wrap(TypeSystem, message, err),
		StatusCode: statusCode,
		URL:        url,
		Method:     method,
	}
}

// DatabaseError represents a database-related error.
type DatabaseError struct {
	*Error
	Operation string
	Query     string
}

// NewDatabaseError creates a new database error.
func NewDatabaseError(operation, query, message string, err error) *DatabaseError {
	return &DatabaseError{
		Error:     Wrap(TypeSystem, message, err),
		Operation: operation,
		Query:     query,
	}
}

// BGPRerror represents a BGP-related error.
type BGPRerror struct {
	*Error
	PeerIP  string
	PeerASN uint32
	Operation string
}

// NewBGPRerror creates a new BGP error.
func NewBGPRerror(peerIP string, peerASN uint32, operation, message string, err error) *BGPRerror {
	return &BGPRerror{
		Error:     Wrap(TypeSystem, message, err),
		PeerIP:    peerIP,
		PeerASN:   peerASN,
		Operation: operation,
	}
}

// FeedError represents a feed-related error.
type FeedError struct {
	*Error
	FeedID   int64
	FeedName string
	URL      string
}

// NewFeedError creates a new feed error.
func NewFeedError(feedID int64, feedName, url, message string, err error) *FeedError {
	return &FeedError{
		Error:    Wrap(TypeSystem, message, err),
		FeedID:   feedID,
		FeedName: feedName,
		URL:      url,
	}
}

// AdapterError represents a JavaScript adapter-related error.
type AdapterError struct {
	*Error
	AdapterID   int64
	AdapterKey  string
	AdapterName string
}

// NewAdapterError creates a new adapter error.
func NewAdapterError(adapterID int64, adapterKey, adapterName, message string, err error) *AdapterError {
	return &AdapterError{
		Error:       Wrap(TypeSystem, message, err),
		AdapterID:   adapterID,
		AdapterKey:  adapterKey,
		AdapterName: adapterName,
	}
}

// ErrorBuilder provides a fluent interface for building errors.
type ErrorBuilder struct {
	err *Error
}

// NewBuilder creates a new error builder.
func NewBuilder(t ErrorType, message string) *ErrorBuilder {
	return &ErrorBuilder{
		err: New(t, message),
	}
}

// WrapBuilder creates a new error builder wrapping an existing error.
func WrapBuilder(t ErrorType, message string, err error) *ErrorBuilder {
	return &ErrorBuilder{
		err: Wrap(t, message, err),
	}
}

// WithContext adds context to the error.
func (b *ErrorBuilder) WithContext(key string, value interface{}) *ErrorBuilder {
	b.err.Context[key] = value
	return b
}

// WithContextMap adds multiple context values to the error.
func (b *ErrorBuilder) WithContextMap(context map[string]interface{}) *ErrorBuilder {
	for k, v := range context {
		b.err.Context[k] = v
	}
	return b
}

// Build returns the built error.
func (b *ErrorBuilder) Build() *Error {
	return b.err
}

// BuildHTTPError builds an HTTP error.
func (b *ErrorBuilder) BuildHTTPError(statusCode int, url, method string) *HTTPError {
	return &HTTPError{
		Error:      b.err,
		StatusCode: statusCode,
		URL:        url,
		Method:     method,
	}
}

// BuildDatabaseError builds a database error.
func (b *ErrorBuilder) BuildDatabaseError(operation, query string) *DatabaseError {
	return &DatabaseError{
		Error:     b.err,
		Operation: operation,
		Query:     query,
	}
}
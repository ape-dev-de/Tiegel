package api

import (
	"errors"
	"fmt"
)

// Error codes. The standard JSON-RPC codes are reused; application errors use
// the implementation-defined range -32000..-32099.
const (
	CodeInvalidParams  = -32602
	CodeMethodNotFound = -32601
	CodeInternal       = -32603

	// CodeFailed is a generic failure of an otherwise valid request.
	CodeFailed = -32000
	// CodeNotFound: a file, document, revision, module or session does not exist.
	CodeNotFound = -32001
	// CodeNoProject: the method needs an open project.
	CodeNoProject = -32003
	// CodeConflict: the document version does not match (optimistic locking).
	CodeConflict = -32004
	// CodeUnavailable: an external program or service is missing or not running.
	CodeUnavailable = -32005
	// CodeModuleDisabled: the method belongs to a module that is disabled
	// or not running (see modules.enable).
	CodeModuleDisabled = -32006
	// CodeUnauthorized: a socket client has not authenticated (or sent a
	// wrong token); the server closes the connection.
	CodeUnauthorized = -32007
)

// Error is an API error with a stable code. Transports map it to their own
// error representation (JSON-RPC error object, CLI exit code).
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	// Data carries structured details (e.g. the kind of a failed push).
	// Transports pass it on as the JSON-RPC error data.
	Data any   `json:"data,omitempty"`
	Err  error `json:"-"`
}

// WithData returns e with Data set.
func (e *Error) WithData(data any) *Error {
	e.Data = data
	return e
}

// Error implements error.
func (e *Error) Error() string { return e.Message }

// Unwrap returns the underlying error, if any.
func (e *Error) Unwrap() error { return e.Err }

// Errorf creates an *Error with a formatted message. A %w verb is honoured
// for Unwrap.
func Errorf(code int, format string, args ...any) *Error {
	err := fmt.Errorf(format, args...)
	return &Error{Code: code, Message: err.Error(), Err: errors.Unwrap(err)}
}

// ErrorCode returns the API code of err: the code of a wrapped *Error, or
// CodeFailed for other errors.
func ErrorCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeFailed
}

// IsUsageError reports whether err was caused by the caller (unknown method
// or invalid parameters) rather than by a failing operation.
func IsUsageError(err error) bool {
	c := ErrorCode(err)
	return c == CodeInvalidParams || c == CodeMethodNotFound
}

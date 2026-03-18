package platform

import (
	"errors"
	"fmt"
)

// Error is an interface for platform-specific errors that carry a machine-readable
// ErrorCode alongside a human-readable message.
type Error interface {
	error
	// Code returns the machine-readable error code.
	Code() ErrorCode
	// Message returns the human-readable error message.
	Message() string
}

// ErrorCode represents a category of platform error.
type ErrorCode int

const (
	// CodeInternal indicates an unexpected internal error.
	CodeInternal ErrorCode = iota
	// CodeNotFound indicates that a requested resource was not found.
	CodeNotFound
	// CodeAlreadyExists indicates that a resource already exists.
	CodeAlreadyExists
	// CodeInvalidArgument indicates that a caller-provided argument was invalid.
	CodeInvalidArgument
	// CodePermissionDenied indicates that the caller lacks permission.
	CodePermissionDenied
	// CodeUnauthenticated indicates that the request lacks valid authentication.
	CodeUnauthenticated
	// CodeUnavailable indicates that the service is currently unavailable.
	CodeUnavailable
)

// String returns a human-readable representation of the ErrorCode.
func (c ErrorCode) String() string {
	switch c {
	case CodeInternal:
		return "internal"
	case CodeNotFound:
		return "not_found"
	case CodeAlreadyExists:
		return "already_exists"
	case CodeInvalidArgument:
		return "invalid_argument"
	case CodePermissionDenied:
		return "permission_denied"
	case CodeUnauthenticated:
		return "unauthenticated"
	case CodeUnavailable:
		return "unavailable"
	default:
		return fmt.Sprintf("unknown(%d)", int(c))
	}
}

// platformError is the concrete implementation of the Error interface.
type platformError struct {
	code  ErrorCode
	msg   string
	cause error
}

// NewError creates a new Error with the given code and message.
func NewError(code ErrorCode, msg string) Error {
	return &platformError{code: code, msg: msg}
}

// NewErrorf creates a new Error with the given code and a formatted message.
func NewErrorf(code ErrorCode, format string, args ...any) Error {
	return &platformError{code: code, msg: fmt.Sprintf(format, args...)}
}

// WrapError creates a new Error that wraps an underlying cause error.
// The cause is accessible via errors.Unwrap and participates in errors.Is/errors.As chains.
func WrapError(code ErrorCode, msg string, cause error) Error {
	return &platformError{code: code, msg: msg, cause: cause}
}

// IsError checks whether err is a platform Error and returns it if so.
func IsError(err error) (Error, bool) {
	var pe Error
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// GetCode extracts the ErrorCode from err. If err does not implement Error,
// CodeInternal is returned as a safe default.
func GetCode(err error) ErrorCode {
	if pe, ok := IsError(err); ok {
		return pe.Code()
	}
	return CodeInternal
}

// Error returns a string representation of the error, including the cause if present.
func (e *platformError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("platform: %s: %s: %v", e.code, e.msg, e.cause)
	}
	return fmt.Sprintf("platform: %s: %s", e.code, e.msg)
}

// Code returns the error's ErrorCode.
func (e *platformError) Code() ErrorCode {
	return e.code
}

// Message returns the error's human-readable message.
func (e *platformError) Message() string {
	return e.msg
}

// Unwrap returns the underlying cause error, enabling errors.Is and errors.As
// to traverse the error chain.
func (e *platformError) Unwrap() error {
	return e.cause
}

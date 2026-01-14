// Package core defines sentinel errors.
package core

import "errors"

// ErrorCode represents a typed error code.
type ErrorCode string

const (
	CodeInvalidInput       ErrorCode = "INVALID_INPUT"
	CodeInvalidCost        ErrorCode = "INVALID_COST"
	CodeRuleNotFound       ErrorCode = "RULE_NOT_FOUND"
	CodeLimiterUnavailable ErrorCode = "LIMITER_UNAVAILABLE"
	CodeLimiterError       ErrorCode = "LIMITER_ERROR"
	CodeConflict           ErrorCode = "CONFLICT"
	CodeNotFound           ErrorCode = "NOT_FOUND"
	CodeUnauthorized       ErrorCode = "UNAUTHORIZED"
	CodeForbidden          ErrorCode = "FORBIDDEN"
)

// AppError is a typed application error.
type AppError struct {
	Code    ErrorCode
	Message string
	Err     error
}

// Error returns the error message.
func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap returns the underlying error.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Wrap creates a new AppError.
func Wrap(code ErrorCode, msg string, err error) error {
	return &AppError{Code: code, Message: msg, Err: err}
}

// CodeOf returns the ErrorCode for an error.
func CodeOf(err error) ErrorCode {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}

// ErrInvalidInput indicates validation failures.
var ErrInvalidInput = &AppError{Code: CodeInvalidInput, Message: "invalid input"}

// ErrConflict indicates optimistic concurrency conflicts.
var ErrConflict = &AppError{Code: CodeConflict, Message: "conflict"}

// ErrNotFound indicates missing resources.
var ErrNotFound = &AppError{Code: CodeNotFound, Message: "not found"}

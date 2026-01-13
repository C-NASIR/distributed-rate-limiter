// Package ratelimit defines sentinel errors.
package ratelimit

import "errors"

// ErrInvalidInput indicates validation failures.
var ErrInvalidInput = errors.New("invalid input")

// ErrConflict indicates optimistic concurrency conflicts.
var ErrConflict = errors.New("conflict")

// ErrNotFound indicates missing resources.
var ErrNotFound = errors.New("not found")

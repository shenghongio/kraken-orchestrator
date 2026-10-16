package domain

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound                = errors.New("not found")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrInvalidPayload          = errors.New("invalid payload")
	ErrIdempotencyConflict     = errors.New("idempotency conflict")
	ErrLeaseExpired            = errors.New("lease expired")
	ErrStaleCallback           = errors.New("stale callback")
	ErrCancelled               = errors.New("task cancelled")
)

type ErrorCode string

const (
	CodeInvalidInput ErrorCode = "invalid_input"
	CodeNotFound     ErrorCode = "not_found"
	CodeConflict     ErrorCode = "conflict"
	CodeInternal     ErrorCode = "internal"
)

type DomainError struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Cause     error
}

func (e *DomainError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}
func (e *DomainError) Unwrap() error {
	return e.Cause
}

func NewInvalidInputError(message string, cause error) *DomainError {
	return &DomainError{
		Code:      CodeInvalidInput,
		Message:   message,
		Retryable: false,
		Cause:     cause,
	}
}
func NewNotFoundError(message string) *DomainError {
	return &DomainError{
		Code:      CodeNotFound,
		Message:   message,
		Retryable: false,
	}
}

func NewRetryableError(message string, cause error) *DomainError {
	return &DomainError{
		Code:      CodeInternal,
		Message:   message,
		Retryable: true,
		Cause:     cause,
	}
}
func IsRetryable(err error) bool {
	var de *DomainError
	if errors.As(err, &de) {
		return de.Retryable
	}
	return false
}

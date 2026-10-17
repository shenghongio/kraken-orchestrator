package domain

import (
	"errors"
	"testing"
)

func TestIsRetryable(t *testing.T) {
	retryable := NewRetryableError("retry", nil)
	if !IsRetryable(retryable) {
		t.Error("expected retryable")
	}

	notRetryable := NewInvalidInputError("invalid", nil)
	if IsRetryable(notRetryable) {
		t.Error("expected not retryable")
	}
	plain := errors.New("plain")
	if IsRetryable(plain) {
		t.Error("expected not retryable for plain error")
	}
}
func TestDomainErrorUnwrap(t *testing.T) {
	cause := errors.New("cause")
	retryableError := NewRetryableError("msg", cause)
	if !errors.Is(retryableError, cause) {
		t.Error("errors.Is should match cause")
	}
}

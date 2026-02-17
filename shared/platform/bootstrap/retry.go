package bootstrap

import (
	"errors"
	"net"
	"strings"
	"syscall"
)

// PermanentError wraps an error to signal that it should not be retried.
// Use Permanent() to create one.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	return "permanent: " + e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// Permanent wraps err to indicate that it is a permanent (non-retryable) error.
// Returns nil if err is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether err (or any error in its chain) is a PermanentError.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// transientSubstrings are substrings in error messages that indicate
// transient infrastructure errors worth retrying during startup.
var transientSubstrings = []string{
	"connection refused",
	"connection reset",
	"i/o timeout",
	"dial tcp",
	"server is not ready",
	"node is not ready",
}

// IsRetryableStartupError classifies err as retryable (transient infrastructure
// error) or not. It returns false for nil, PermanentError, and unrecognized errors.
func IsRetryableStartupError(err error) bool {
	if err == nil {
		return false
	}
	if IsPermanent(err) {
		return false
	}

	// Check for net.OpError in the chain.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Check for known transient syscall errors in the chain.
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	// Fall back to substring matching on the error message.
	msg := strings.ToLower(err.Error())
	for _, sub := range transientSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}

	return false
}

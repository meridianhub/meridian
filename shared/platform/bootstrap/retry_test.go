package bootstrap

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

var errSentinel = errors.New("sentinel error")

func TestPermanent_NilReturnsNil(t *testing.T) {
	if got := Permanent(nil); got != nil {
		t.Errorf("Permanent(nil) = %v, want nil", got)
	}
}

func TestIsPermanent_ReturnsTrueForPermanentError(t *testing.T) {
	err := Permanent(errors.New("bad config"))
	if !IsPermanent(err) {
		t.Error("IsPermanent(Permanent(err)) = false, want true")
	}
}

func TestIsPermanent_ReturnsFalseForRegularError(t *testing.T) {
	err := fmt.Errorf("some error")
	if IsPermanent(err) {
		t.Error("IsPermanent(regular error) = true, want false")
	}
}

func TestPermanent_UnwrapChainPreservesSentinel(t *testing.T) {
	wrapped := Permanent(errSentinel)
	if !errors.Is(wrapped, errSentinel) {
		t.Error("errors.Is(Permanent(sentinel), sentinel) = false, want true")
	}
}

func TestPermanentError_ErrorMessage(t *testing.T) {
	inner := errors.New("bad config value")
	pe := Permanent(inner)
	want := "permanent: bad config value"
	if got := pe.Error(); got != want {
		t.Errorf("PermanentError.Error() = %q, want %q", got, want)
	}
}

func TestIsRetryableStartupError_Nil(t *testing.T) {
	if IsRetryableStartupError(nil) {
		t.Error("IsRetryableStartupError(nil) = true, want false")
	}
}

func TestIsRetryableStartupError_PermanentError(t *testing.T) {
	err := Permanent(errors.New("bad config"))
	if IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(PermanentError) = true, want false")
	}
}

func TestIsRetryableStartupError_NetOpError(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Addr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 5432,
		},
		Err: syscall.ECONNREFUSED,
	}
	if !IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(net.OpError) = false, want true")
	}
}

func TestIsRetryableStartupError_WrappedNetOpError(t *testing.T) {
	inner := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Addr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 5432,
		},
		Err: syscall.ECONNREFUSED,
	}
	err := fmt.Errorf("failed to connect: %w", inner)
	if !IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(wrapped net.OpError) = false, want true")
	}
}

func TestIsRetryableStartupError_ConnectionRefusedString(t *testing.T) {
	err := fmt.Errorf("connection refused")
	if !IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError('connection refused') = false, want true")
	}
}

func TestIsRetryableStartupError_TransientStrings(t *testing.T) {
	transientMessages := []string{
		"connection refused",
		"connection reset by peer",
		"i/o timeout",
		"dial tcp 127.0.0.1:5432: connect: connection refused",
		"server is not ready",
		"node is not ready",
	}

	for _, msg := range transientMessages {
		t.Run(msg, func(t *testing.T) {
			err := fmt.Errorf("startup failed: %s", msg)
			if !IsRetryableStartupError(err) {
				t.Errorf("IsRetryableStartupError(%q) = false, want true", msg)
			}
		})
	}
}

func TestIsRetryableStartupError_UnrecognizedError(t *testing.T) {
	err := errors.New("invalid configuration value")
	if IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(unrecognized error) = true, want false")
	}
}

func TestIsRetryableStartupError_SyscallErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ECONNREFUSED", syscall.ECONNREFUSED},
		{"ETIMEDOUT", syscall.ETIMEDOUT},
		{"ECONNRESET", syscall.ECONNRESET},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := fmt.Errorf("dial failed: %w", tt.err)
			if !IsRetryableStartupError(wrapped) {
				t.Errorf("IsRetryableStartupError(%s) = false, want true", tt.name)
			}
		})
	}
}

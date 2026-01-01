package defaults_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
)

// TestTimeoutConstants verifies that all timeout constants have their expected values.
// This test serves as documentation and guards against accidental changes to
// platform-wide defaults that could affect system behavior.
func TestTimeoutConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      time.Duration
		want     time.Duration
		category string
	}{
		{
			name:     "DefaultRPCTimeout",
			got:      defaults.DefaultRPCTimeout,
			want:     30 * time.Second,
			category: "RPC",
		},
		{
			name:     "DefaultContextTimeout",
			got:      defaults.DefaultContextTimeout,
			want:     30 * time.Second,
			category: "RPC",
		},
		{
			name:     "DefaultHealthCheckTimeout",
			got:      defaults.DefaultHealthCheckTimeout,
			want:     5 * time.Second,
			category: "Health Check",
		},
		{
			name:     "DefaultCircuitBreakerTimeout",
			got:      defaults.DefaultCircuitBreakerTimeout,
			want:     60 * time.Second,
			category: "Circuit Breaker",
		},
		{
			name:     "DefaultGracefulShutdown",
			got:      defaults.DefaultGracefulShutdown,
			want:     30 * time.Second,
			category: "Lifecycle",
		},
		{
			name:     "DefaultRetryDelay",
			got:      defaults.DefaultRetryDelay,
			want:     100 * time.Millisecond,
			category: "Retry",
		},
		{
			name:     "DefaultMaxRetryInterval",
			got:      defaults.DefaultMaxRetryInterval,
			want:     5 * time.Second,
			category: "Retry",
		},
		{
			name:     "DefaultHTTPReadHeaderTimeout",
			got:      defaults.DefaultHTTPReadHeaderTimeout,
			want:     10 * time.Second,
			category: "HTTP",
		},
		{
			name:     "DefaultHTTPReadTimeout",
			got:      defaults.DefaultHTTPReadTimeout,
			want:     30 * time.Second,
			category: "HTTP",
		},
		{
			name:     "DefaultHTTPWriteTimeout",
			got:      defaults.DefaultHTTPWriteTimeout,
			want:     30 * time.Second,
			category: "HTTP",
		},
		{
			name:     "DefaultHTTPIdleTimeout",
			got:      defaults.DefaultHTTPIdleTimeout,
			want:     60 * time.Second,
			category: "HTTP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s (%s): got %v, want %v",
					tt.name, tt.category, tt.got, tt.want)
			}
		})
	}
}

// TestTimeoutRelationships verifies sensible relationships between timeouts.
// These are invariants that should hold for proper system behavior.
func TestTimeoutRelationships(t *testing.T) {
	t.Run("HealthCheckTimeout is shorter than RPC timeout", func(t *testing.T) {
		// Health checks should respond faster than general RPC calls
		if defaults.DefaultHealthCheckTimeout >= defaults.DefaultRPCTimeout {
			t.Errorf("HealthCheckTimeout (%v) should be shorter than RPCTimeout (%v)",
				defaults.DefaultHealthCheckTimeout, defaults.DefaultRPCTimeout)
		}
	})

	t.Run("RetryDelay is sub-second", func(t *testing.T) {
		// Initial retry delay should be quick for fast recovery
		if defaults.DefaultRetryDelay >= time.Second {
			t.Errorf("RetryDelay (%v) should be sub-second for quick retry",
				defaults.DefaultRetryDelay)
		}
	})

	t.Run("GracefulShutdown allows multiple RPC calls to complete", func(t *testing.T) {
		// Shutdown should allow at least one in-flight RPC to complete
		if defaults.DefaultGracefulShutdown < defaults.DefaultRPCTimeout {
			t.Errorf("GracefulShutdown (%v) should be >= RPCTimeout (%v) to allow in-flight requests to complete",
				defaults.DefaultGracefulShutdown, defaults.DefaultRPCTimeout)
		}
	})

	t.Run("CircuitBreakerTimeout is longer than RPC timeout", func(t *testing.T) {
		// Circuit breaker should stay open longer than a single call takes
		if defaults.DefaultCircuitBreakerTimeout <= defaults.DefaultRPCTimeout {
			t.Errorf("CircuitBreakerTimeout (%v) should be longer than RPCTimeout (%v)",
				defaults.DefaultCircuitBreakerTimeout, defaults.DefaultRPCTimeout)
		}
	})
}

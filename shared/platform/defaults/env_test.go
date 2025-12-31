package defaults_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
)

// TestGetTimeoutFunctions tests all Get* functions for proper environment variable handling.
// Each function is tested with:
//   - Environment variable not set (should return default)
//   - Environment variable set to valid value (should return parsed value)
//   - Environment variable set to invalid value (should return default)
//   - Environment variable set to out-of-range value (should return default)
func TestGetTimeoutFunctions(t *testing.T) {
	tests := []struct {
		name         string
		envVar       string
		getFunc      func() time.Duration
		defaultValue time.Duration
		validValue   string
		validParsed  time.Duration
		tooLow       string
		tooHigh      string
	}{
		{
			name:         "GetRPCTimeout",
			envVar:       "TIMEOUT_RPC",
			getFunc:      defaults.GetRPCTimeout,
			defaultValue: 30 * time.Second,
			validValue:   "45s",
			validParsed:  45 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetHealthCheckTimeout",
			envVar:       "TIMEOUT_HEALTH_CHECK",
			getFunc:      defaults.GetHealthCheckTimeout,
			defaultValue: 5 * time.Second,
			validValue:   "10s",
			validParsed:  10 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetCircuitBreakerTimeout",
			envVar:       "TIMEOUT_CIRCUIT_BREAKER",
			getFunc:      defaults.GetCircuitBreakerTimeout,
			defaultValue: 60 * time.Second,
			validValue:   "90s",
			validParsed:  90 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetGracefulShutdown",
			envVar:       "TIMEOUT_GRACEFUL_SHUTDOWN",
			getFunc:      defaults.GetGracefulShutdown,
			defaultValue: 30 * time.Second,
			validValue:   "45s",
			validParsed:  45 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetContextTimeout",
			envVar:       "TIMEOUT_CONTEXT",
			getFunc:      defaults.GetContextTimeout,
			defaultValue: 30 * time.Second,
			validValue:   "60s",
			validParsed:  60 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetRetryDelay",
			envVar:       "TIMEOUT_RETRY_DELAY",
			getFunc:      defaults.GetRetryDelay,
			defaultValue: 100 * time.Millisecond,
			validValue:   "250ms",
			validParsed:  250 * time.Millisecond,
			tooLow:       "5ms",
			tooHigh:      "2m",
		},
		{
			name:         "GetMaxRetryInterval",
			envVar:       "TIMEOUT_MAX_RETRY_INTERVAL",
			getFunc:      defaults.GetMaxRetryInterval,
			defaultValue: 5 * time.Second,
			validValue:   "10s",
			validParsed:  10 * time.Second,
			tooLow:       "5ms",
			tooHigh:      "2m",
		},
		{
			name:         "GetHTTPReadHeaderTimeout",
			envVar:       "TIMEOUT_HTTP_READ_HEADER",
			getFunc:      defaults.GetHTTPReadHeaderTimeout,
			defaultValue: 10 * time.Second,
			validValue:   "15s",
			validParsed:  15 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetHTTPReadTimeout",
			envVar:       "TIMEOUT_HTTP_READ",
			getFunc:      defaults.GetHTTPReadTimeout,
			defaultValue: 30 * time.Second,
			validValue:   "45s",
			validParsed:  45 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetHTTPWriteTimeout",
			envVar:       "TIMEOUT_HTTP_WRITE",
			getFunc:      defaults.GetHTTPWriteTimeout,
			defaultValue: 30 * time.Second,
			validValue:   "45s",
			validParsed:  45 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
		{
			name:         "GetHTTPIdleTimeout",
			envVar:       "TIMEOUT_HTTP_IDLE",
			getFunc:      defaults.GetHTTPIdleTimeout,
			defaultValue: 60 * time.Second,
			validValue:   "90s",
			validParsed:  90 * time.Second,
			tooLow:       "500ms",
			tooHigh:      "10m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test with env var unset
			t.Run("unset returns default", func(t *testing.T) {
				t.Setenv(tt.envVar, "")
				got := tt.getFunc()
				if got != tt.defaultValue {
					t.Errorf("with env unset: got %v, want %v", got, tt.defaultValue)
				}
			})

			// Test with valid env var
			t.Run("valid value returns parsed", func(t *testing.T) {
				t.Setenv(tt.envVar, tt.validValue)
				got := tt.getFunc()
				if got != tt.validParsed {
					t.Errorf("with env=%s: got %v, want %v", tt.validValue, got, tt.validParsed)
				}
			})

			// Test with invalid format
			t.Run("invalid format returns default", func(t *testing.T) {
				t.Setenv(tt.envVar, "not-a-duration")
				got := tt.getFunc()
				if got != tt.defaultValue {
					t.Errorf("with invalid env: got %v, want %v", got, tt.defaultValue)
				}
			})

			// Test with value too low
			t.Run("too low returns default", func(t *testing.T) {
				t.Setenv(tt.envVar, tt.tooLow)
				got := tt.getFunc()
				if got != tt.defaultValue {
					t.Errorf("with env=%s (too low): got %v, want %v", tt.tooLow, got, tt.defaultValue)
				}
			})

			// Test with value too high
			t.Run("too high returns default", func(t *testing.T) {
				t.Setenv(tt.envVar, tt.tooHigh)
				got := tt.getFunc()
				if got != tt.defaultValue {
					t.Errorf("with env=%s (too high): got %v, want %v", tt.tooHigh, got, tt.defaultValue)
				}
			})
		})
	}
}

// TestGetTimeoutBoundaryValues tests the exact boundary conditions for timeout validation.
func TestGetTimeoutBoundaryValues(t *testing.T) {
	// Test standard timeout boundaries (1s to 5m)
	t.Run("standard timeout boundaries", func(t *testing.T) {
		// Exactly at minimum should be accepted
		t.Setenv("TIMEOUT_RPC", "1s")
		got := defaults.GetRPCTimeout()
		if got != 1*time.Second {
			t.Errorf("at minimum boundary: got %v, want 1s", got)
		}

		// Exactly at maximum should be accepted
		t.Setenv("TIMEOUT_RPC", "5m")
		got = defaults.GetRPCTimeout()
		if got != 5*time.Minute {
			t.Errorf("at maximum boundary: got %v, want 5m", got)
		}

		// Just below minimum should return default
		t.Setenv("TIMEOUT_RPC", "999ms")
		got = defaults.GetRPCTimeout()
		if got != defaults.DefaultRPCTimeout {
			t.Errorf("below minimum: got %v, want default %v", got, defaults.DefaultRPCTimeout)
		}

		// Just above maximum should return default
		t.Setenv("TIMEOUT_RPC", "5m1s")
		got = defaults.GetRPCTimeout()
		if got != defaults.DefaultRPCTimeout {
			t.Errorf("above maximum: got %v, want default %v", got, defaults.DefaultRPCTimeout)
		}
	})

	// Test retry delay boundaries (10ms to 1m)
	t.Run("retry delay boundaries", func(t *testing.T) {
		// Exactly at minimum should be accepted
		t.Setenv("TIMEOUT_RETRY_DELAY", "10ms")
		got := defaults.GetRetryDelay()
		if got != 10*time.Millisecond {
			t.Errorf("at minimum boundary: got %v, want 10ms", got)
		}

		// Exactly at maximum should be accepted
		t.Setenv("TIMEOUT_RETRY_DELAY", "1m")
		got = defaults.GetRetryDelay()
		if got != 1*time.Minute {
			t.Errorf("at maximum boundary: got %v, want 1m", got)
		}

		// Just below minimum should return default
		t.Setenv("TIMEOUT_RETRY_DELAY", "9ms")
		got = defaults.GetRetryDelay()
		if got != defaults.DefaultRetryDelay {
			t.Errorf("below minimum: got %v, want default %v", got, defaults.DefaultRetryDelay)
		}

		// Just above maximum should return default
		t.Setenv("TIMEOUT_RETRY_DELAY", "1m1s")
		got = defaults.GetRetryDelay()
		if got != defaults.DefaultRetryDelay {
			t.Errorf("above maximum: got %v, want default %v", got, defaults.DefaultRetryDelay)
		}
	})
}

// TestGetTimeoutVariosDurationFormats tests that various valid Go duration formats are accepted.
func TestGetTimeoutVariousDurationFormats(t *testing.T) {
	formats := []struct {
		input    string
		expected time.Duration
	}{
		{"30s", 30 * time.Second},
		{"1m", 1 * time.Minute},
		{"1m30s", 90 * time.Second},
		{"2m30s", 150 * time.Second},
		{"90s", 90 * time.Second},
		{"1500ms", 1500 * time.Millisecond},
	}

	for _, tt := range formats {
		t.Run(tt.input, func(t *testing.T) {
			t.Setenv("TIMEOUT_RPC", tt.input)
			got := defaults.GetRPCTimeout()
			if got != tt.expected {
				t.Errorf("format %s: got %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// TestEnvVarConstants verifies that environment variable constants have expected values.
func TestEnvVarConstants(t *testing.T) {
	// Verify the exported constants match expected naming convention
	expectedVars := map[string]string{
		"EnvRPCTimeout":            "TIMEOUT_RPC",
		"EnvHealthCheckTimeout":    "TIMEOUT_HEALTH_CHECK",
		"EnvCircuitBreakerTimeout": "TIMEOUT_CIRCUIT_BREAKER",
		"EnvGracefulShutdown":      "TIMEOUT_GRACEFUL_SHUTDOWN",
		"EnvContextTimeout":        "TIMEOUT_CONTEXT",
		"EnvRetryDelay":            "TIMEOUT_RETRY_DELAY",
		"EnvMaxRetryInterval":      "TIMEOUT_MAX_RETRY_INTERVAL",
		"EnvHTTPReadHeaderTimeout": "TIMEOUT_HTTP_READ_HEADER",
		"EnvHTTPReadTimeout":       "TIMEOUT_HTTP_READ",
		"EnvHTTPWriteTimeout":      "TIMEOUT_HTTP_WRITE",
		"EnvHTTPIdleTimeout":       "TIMEOUT_HTTP_IDLE",
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"EnvRPCTimeout", defaults.EnvRPCTimeout, expectedVars["EnvRPCTimeout"]},
		{"EnvHealthCheckTimeout", defaults.EnvHealthCheckTimeout, expectedVars["EnvHealthCheckTimeout"]},
		{"EnvCircuitBreakerTimeout", defaults.EnvCircuitBreakerTimeout, expectedVars["EnvCircuitBreakerTimeout"]},
		{"EnvGracefulShutdown", defaults.EnvGracefulShutdown, expectedVars["EnvGracefulShutdown"]},
		{"EnvContextTimeout", defaults.EnvContextTimeout, expectedVars["EnvContextTimeout"]},
		{"EnvRetryDelay", defaults.EnvRetryDelay, expectedVars["EnvRetryDelay"]},
		{"EnvMaxRetryInterval", defaults.EnvMaxRetryInterval, expectedVars["EnvMaxRetryInterval"]},
		{"EnvHTTPReadHeaderTimeout", defaults.EnvHTTPReadHeaderTimeout, expectedVars["EnvHTTPReadHeaderTimeout"]},
		{"EnvHTTPReadTimeout", defaults.EnvHTTPReadTimeout, expectedVars["EnvHTTPReadTimeout"]},
		{"EnvHTTPWriteTimeout", defaults.EnvHTTPWriteTimeout, expectedVars["EnvHTTPWriteTimeout"]},
		{"EnvHTTPIdleTimeout", defaults.EnvHTTPIdleTimeout, expectedVars["EnvHTTPIdleTimeout"]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

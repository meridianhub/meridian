package scheduler

import (
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
)

// Config holds shared configuration for scheduled workers.
type Config struct {
	// PollInterval is how often the worker polls for new work.
	// Default: 5 seconds (defaults.DefaultHealthCheckTimeout).
	PollInterval time.Duration

	// ShutdownTimeout is the maximum time to wait for in-flight work during shutdown.
	// Default: 30 seconds (defaults.DefaultGracefulShutdown).
	ShutdownTimeout time.Duration

	// MaxCatchUpAge is the maximum age of entries to process during catch-up.
	// Entries older than this are skipped. Default: 5 minutes.
	MaxCatchUpAge time.Duration
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		PollInterval:    defaults.DefaultHealthCheckTimeout,
		ShutdownTimeout: defaults.DefaultGracefulShutdown,
		MaxCatchUpAge:   5 * time.Minute,
	}
}

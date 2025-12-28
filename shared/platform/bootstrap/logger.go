package bootstrap

import (
	"log/slog"
	"os"
)

// NewLogger creates and configures a structured JSON logger for service startup.
// It sets the logger as the default slog logger and logs a startup message
// with the service name, version, commit, and build date.
//
// This function consolidates the common logging initialization pattern used
// across all Meridian services.
func NewLogger(serviceName, version, commit, buildDate string) *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting "+serviceName,
		"version", version,
		"commit", commit,
		"build_date", buildDate)

	return logger
}

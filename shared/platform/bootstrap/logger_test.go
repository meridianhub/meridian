package bootstrap

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger(t *testing.T) {
	t.Run("creates logger and logs startup message", func(t *testing.T) {
		// Capture stdout to verify log output
		// Since NewLogger writes to os.Stdout, we need to redirect it
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		require.NoError(t, err)
		os.Stdout = w

		logger := NewLogger("test-service", "1.0.0", "abc123", "2024-01-01")

		// Close writer and restore stdout
		w.Close()
		os.Stdout = oldStdout

		// Read captured output
		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		require.NoError(t, err)
		r.Close()

		output := buf.String()

		// Verify logger is not nil
		assert.NotNil(t, logger)

		// Parse JSON log line
		var logEntry map[string]interface{}
		err = json.Unmarshal([]byte(output), &logEntry)
		require.NoError(t, err, "log output should be valid JSON: %s", output)

		// Verify log fields
		assert.Equal(t, "starting test-service", logEntry["msg"])
		assert.Equal(t, "1.0.0", logEntry["version"])
		assert.Equal(t, "abc123", logEntry["commit"])
		assert.Equal(t, "2024-01-01", logEntry["build_date"])
		assert.Equal(t, "INFO", logEntry["level"])
	})

	t.Run("sets logger as default", func(t *testing.T) {
		// Capture stdout
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		require.NoError(t, err)
		os.Stdout = w

		logger := NewLogger("default-test", "2.0.0", "def456", "2024-06-01")

		// Use the default logger
		slog.Info("test message from default")

		// Close writer and restore stdout
		w.Close()
		os.Stdout = oldStdout

		// Read captured output
		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		require.NoError(t, err)
		r.Close()

		output := buf.String()

		// Verify both log lines are present (startup + test message)
		assert.Contains(t, output, "starting default-test")
		assert.Contains(t, output, "test message from default")

		// Verify logger is the same as default
		// We can't directly compare, but we verify the default works
		assert.NotNil(t, logger)
	})

	t.Run("uses JSON handler with INFO level", func(t *testing.T) {
		// Capture stdout
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		require.NoError(t, err)
		os.Stdout = w

		logger := NewLogger("level-test", "1.0.0", "abc", "2024-01-01")

		// Log a debug message (should NOT appear)
		logger.Debug("debug message")
		// Log an info message (should appear)
		logger.Info("info message")

		w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		require.NoError(t, err)
		r.Close()

		output := buf.String()

		// Debug should not appear (level is INFO)
		assert.NotContains(t, output, "debug message")
		// Info should appear
		assert.Contains(t, output, "info message")
	})
}

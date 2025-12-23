package main

import (
	"bytes"
	"log/slog"
	"testing"
	"time"
)

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue time.Duration
		want         time.Duration
		expectWarn   bool
	}{
		{
			name:         "returns env value when valid duration",
			envValue:     "10s",
			setEnv:       true,
			defaultValue: 5 * time.Second,
			want:         10 * time.Second,
			expectWarn:   false,
		},
		{
			name:         "returns default when env empty",
			envValue:     "",
			setEnv:       true,
			defaultValue: 5 * time.Second,
			want:         5 * time.Second,
			expectWarn:   false,
		},
		{
			name:         "returns default and warns when env invalid",
			envValue:     "invalid",
			setEnv:       true,
			defaultValue: 5 * time.Second,
			want:         5 * time.Second,
			expectWarn:   true,
		},
		{
			name:         "returns default when env not set",
			envValue:     "",
			setEnv:       false,
			defaultValue: 5 * time.Second,
			want:         5 * time.Second,
			expectWarn:   false,
		},
		{
			name:         "parses minutes correctly",
			envValue:     "2m",
			setEnv:       true,
			defaultValue: time.Minute,
			want:         2 * time.Minute,
			expectWarn:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const testKey = "TEST_DURATION_VAR"

			// Set up logger to capture output
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			}))
			slog.SetDefault(logger)

			if tt.setEnv {
				t.Setenv(testKey, tt.envValue)
			}

			got := getEnvDuration(testKey, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvDuration(%q, %v) = %v, want %v", testKey, tt.defaultValue, got, tt.want)
			}

			// Check if warning was logged
			logOutput := buf.String()
			hasWarn := len(logOutput) > 0
			if hasWarn != tt.expectWarn {
				t.Errorf("getEnvDuration(%q, %v) warning logged = %v, want %v", testKey, tt.defaultValue, hasWarn, tt.expectWarn)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue int
		want         int
		expectWarn   bool
	}{
		{
			name:         "returns env value when valid int",
			envValue:     "5",
			setEnv:       true,
			defaultValue: 10,
			want:         5,
			expectWarn:   false,
		},
		{
			name:         "returns default when env empty",
			envValue:     "",
			setEnv:       true,
			defaultValue: 10,
			want:         10,
			expectWarn:   false,
		},
		{
			name:         "returns default and warns when env invalid",
			envValue:     "abc",
			setEnv:       true,
			defaultValue: 10,
			want:         10,
			expectWarn:   true,
		},
		{
			name:         "returns default when env not set",
			envValue:     "",
			setEnv:       false,
			defaultValue: 10,
			want:         10,
			expectWarn:   false,
		},
		{
			name:         "parses zero correctly",
			envValue:     "0",
			setEnv:       true,
			defaultValue: 10,
			want:         0,
			expectWarn:   false,
		},
		{
			name:         "parses negative int correctly",
			envValue:     "-5",
			setEnv:       true,
			defaultValue: 10,
			want:         -5,
			expectWarn:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const testKey = "TEST_INT_VAR"

			// Set up logger to capture output
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			}))
			slog.SetDefault(logger)

			if tt.setEnv {
				t.Setenv(testKey, tt.envValue)
			}

			got := getEnvInt(testKey, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvInt(%q, %d) = %d, want %d", testKey, tt.defaultValue, got, tt.want)
			}

			// Check if warning was logged
			logOutput := buf.String()
			hasWarn := len(logOutput) > 0
			if hasWarn != tt.expectWarn {
				t.Errorf("getEnvInt(%q, %d) warning logged = %v, want %v", testKey, tt.defaultValue, hasWarn, tt.expectWarn)
			}
		})
	}
}

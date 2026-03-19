package main

import (
	"bytes"
	"log/slog"
	"testing"
	"time"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},           // empty defaults to info
		{"unknown", slog.LevelInfo},    // unknown defaults to info
		{"trace", slog.LevelInfo},      // unsupported defaults to info
		{"Debug", slog.LevelDebug},     // mixed case
		{"WaRnInG", slog.LevelWarn},    // mixed case
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLogLevel(tt.input)
			if got != tt.expected {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetConfigSource(t *testing.T) {
	t.Run("returns env when set", func(t *testing.T) {
		t.Setenv("TEST_CONFIG_SOURCE_VAR", "somevalue")
		got := getConfigSource("TEST_CONFIG_SOURCE_VAR")
		if got != "env" {
			t.Errorf("getConfigSource() = %q, want %q", got, "env")
		}
	})

	t.Run("returns default when not set", func(t *testing.T) {
		got := getConfigSource("NONEXISTENT_CONFIG_VAR_12345")
		if got != "default" {
			t.Errorf("getConfigSource() = %q, want %q", got, "default")
		}
	})

	t.Run("returns default for empty string value", func(t *testing.T) {
		t.Setenv("EMPTY_CONFIG_VAR", "")
		got := getConfigSource("EMPTY_CONFIG_VAR")
		if got != "default" {
			t.Errorf("getConfigSource() = %q, want %q for empty env var", got, "default")
		}
	})
}

func TestWorkerConfig_Validate_RetryBaseDelayEqualToMax(t *testing.T) {
	config := WorkerConfig{
		PollInterval:   5 * time.Second,
		MaxRetries:     5,
		RetryBaseDelay: 5 * time.Second,
		RetryMaxDelay:  5 * time.Second,
		MaxConcurrent:  10,
	}
	err := config.Validate()
	if err == nil {
		t.Error("expected error when retry base delay equals max delay")
	}
}

func TestWorkerConfig_Validate_NegativeRetryBaseDelay(t *testing.T) {
	config := WorkerConfig{
		PollInterval:   5 * time.Second,
		MaxRetries:     5,
		RetryBaseDelay: -1 * time.Second,
		RetryMaxDelay:  5 * time.Second,
		MaxConcurrent:  10,
	}
	err := config.Validate()
	if err == nil {
		t.Error("expected error for negative retry base delay")
	}
}

func TestWorkerConfig_Validate_NegativeRetryMaxDelay(t *testing.T) {
	config := WorkerConfig{
		PollInterval:   5 * time.Second,
		MaxRetries:     5,
		RetryBaseDelay: 2 * time.Second,
		RetryMaxDelay:  -1 * time.Second,
		MaxConcurrent:  10,
	}
	err := config.Validate()
	if err == nil {
		t.Error("expected error for negative retry max delay")
	}
}

func TestWorkerConfig_Validate_NegativeMaxConcurrent(t *testing.T) {
	config := WorkerConfig{
		PollInterval:   5 * time.Second,
		MaxRetries:     5,
		RetryBaseDelay: 2 * time.Second,
		RetryMaxDelay:  5 * time.Second,
		MaxConcurrent:  -1,
	}
	err := config.Validate()
	if err == nil {
		t.Error("expected error for negative max concurrent")
	}
}

func TestLoadWorkerConfig_MaxConcurrentOutOfRange(t *testing.T) {
	t.Setenv("PROVISIONING_MAX_CONCURRENT", "200")
	_, err := loadWorkerConfig()
	if err == nil {
		t.Error("expected error for max concurrent out of range")
	}
}

func TestLoadWorkerConfig(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		want    WorkerConfig
		wantErr bool
	}{
		{
			name:    "returns all defaults when no env vars set",
			envVars: nil,
			want: WorkerConfig{
				PollInterval:   10 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  5,
			},
			wantErr: false,
		},
		{
			name: "returns custom values when env vars set",
			envVars: map[string]string{
				"PROVISIONING_WORKER_POLL_INTERVAL": "1s",
				"PROVISIONING_MAX_RETRIES":          "10",
				"PROVISIONING_RETRY_BASE_DELAY":     "5s",
				"PROVISIONING_RETRY_MAX_DELAY":      "60s",
				"PROVISIONING_MAX_CONCURRENT":       "20",
			},
			want: WorkerConfig{
				PollInterval:   1 * time.Second,
				MaxRetries:     10,
				RetryBaseDelay: 5 * time.Second,
				RetryMaxDelay:  60 * time.Second,
				MaxConcurrent:  20,
			},
			wantErr: false,
		},
		{
			name: "returns defaults for invalid values",
			envVars: map[string]string{
				"PROVISIONING_WORKER_POLL_INTERVAL": "invalid",
				"PROVISIONING_MAX_RETRIES":          "not-a-number",
			},
			want: WorkerConfig{
				PollInterval:   10 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  5,
			},
			wantErr: false,
		},
		{
			name: "returns partial custom values with defaults",
			envVars: map[string]string{
				"PROVISIONING_WORKER_POLL_INTERVAL": "15s",
				"PROVISIONING_MAX_RETRIES":          "7",
			},
			want: WorkerConfig{
				PollInterval:   15 * time.Second,
				MaxRetries:     7,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  5,
			},
			wantErr: false,
		},
		{
			name: "returns error for invalid poll interval",
			envVars: map[string]string{
				"PROVISIONING_WORKER_POLL_INTERVAL": "500ms",
			},
			wantErr: true,
		},
		{
			name: "returns error for invalid max retries",
			envVars: map[string]string{
				"PROVISIONING_MAX_RETRIES": "25",
			},
			wantErr: true,
		},
		{
			name: "returns error when retry base delay >= retry max delay",
			envVars: map[string]string{
				"PROVISIONING_RETRY_BASE_DELAY": "40s",
				"PROVISIONING_RETRY_MAX_DELAY":  "30s",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up logger to avoid noise in test output
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			}))
			slog.SetDefault(logger)

			// Set environment variables
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			got, err := loadWorkerConfig()

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Errorf("loadWorkerConfig() expected error, got nil")
				}
				return
			}

			// No error expected
			if err != nil {
				t.Errorf("loadWorkerConfig() unexpected error: %v", err)
				return
			}

			// Compare each field
			if got.PollInterval != tt.want.PollInterval {
				t.Errorf("loadWorkerConfig().PollInterval = %v, want %v", got.PollInterval, tt.want.PollInterval)
			}
			if got.MaxRetries != tt.want.MaxRetries {
				t.Errorf("loadWorkerConfig().MaxRetries = %d, want %d", got.MaxRetries, tt.want.MaxRetries)
			}
			if got.RetryBaseDelay != tt.want.RetryBaseDelay {
				t.Errorf("loadWorkerConfig().RetryBaseDelay = %v, want %v", got.RetryBaseDelay, tt.want.RetryBaseDelay)
			}
			if got.RetryMaxDelay != tt.want.RetryMaxDelay {
				t.Errorf("loadWorkerConfig().RetryMaxDelay = %v, want %v", got.RetryMaxDelay, tt.want.RetryMaxDelay)
			}
			if got.MaxConcurrent != tt.want.MaxConcurrent {
				t.Errorf("loadWorkerConfig().MaxConcurrent = %d, want %d", got.MaxConcurrent, tt.want.MaxConcurrent)
			}
		})
	}
}

func TestWorkerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  WorkerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid configuration",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: false,
		},
		{
			name: "poll interval too small",
			config: WorkerConfig{
				PollInterval:   500 * time.Millisecond,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: true,
			errMsg:  "poll interval must be >= 1s",
		},
		{
			name: "poll interval exactly 1s (boundary)",
			config: WorkerConfig{
				PollInterval:   1 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: false,
		},
		{
			name: "max retries negative",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     -1,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: true,
			errMsg:  "max retries must be >= 0",
		},
		{
			name: "max retries zero (boundary)",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     0,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: false,
		},
		{
			name: "max retries too high",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     21,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: true,
			errMsg:  "max retries must be >= 0 and <= 20",
		},
		{
			name: "max retries exactly 20 (boundary)",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     20,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: false,
		},
		{
			name: "retry base delay zero",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 0,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: true,
			errMsg:  "retry base delay must be > 0",
		},
		{
			name: "retry max delay zero",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  0,
				MaxConcurrent:  10,
			},
			wantErr: true,
			errMsg:  "retry max delay must be > 0",
		},
		{
			name: "retry base delay >= retry max delay (equal)",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 30 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: true,
			errMsg:  "retry base delay",
		},
		{
			name: "retry base delay > retry max delay",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 40 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  10,
			},
			wantErr: true,
			errMsg:  "retry base delay",
		},
		{
			name: "max concurrent zero",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  0,
			},
			wantErr: true,
			errMsg:  "max concurrent must be >= 1",
		},
		{
			name: "max concurrent exactly 1 (boundary)",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  1,
			},
			wantErr: false,
		},
		{
			name: "max concurrent too high",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  101,
			},
			wantErr: true,
			errMsg:  "max concurrent must be >= 1 and <= 100",
		},
		{
			name: "max concurrent exactly 100 (boundary)",
			config: WorkerConfig{
				PollInterval:   5 * time.Second,
				MaxRetries:     5,
				RetryBaseDelay: 2 * time.Second,
				RetryMaxDelay:  5 * time.Second,
				MaxConcurrent:  100,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
				} else if tt.errMsg != "" && !bytes.Contains([]byte(err.Error()), []byte(tt.errMsg)) {
					t.Errorf("Validate() error = %v, want substring %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}

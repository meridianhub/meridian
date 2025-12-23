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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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
				RetryMaxDelay:  30 * time.Second,
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

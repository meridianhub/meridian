package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestGetEnvAsBool(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue bool
		want         bool
	}{
		{
			name:         "returns default when env not set",
			envValue:     "",
			setEnv:       false,
			defaultValue: true,
			want:         true,
		},
		{
			name:         "returns default when env is empty",
			envValue:     "",
			setEnv:       true,
			defaultValue: false,
			want:         false,
		},
		{
			name:         "returns true for 'true'",
			envValue:     "true",
			setEnv:       true,
			defaultValue: false,
			want:         true,
		},
		{
			name:         "returns true for 'TRUE' (case insensitive)",
			envValue:     "TRUE",
			setEnv:       true,
			defaultValue: false,
			want:         true,
		},
		{
			name:         "returns true for '1'",
			envValue:     "1",
			setEnv:       true,
			defaultValue: false,
			want:         true,
		},
		{
			name:         "returns true for 'yes'",
			envValue:     "yes",
			setEnv:       true,
			defaultValue: false,
			want:         true,
		},
		{
			name:         "returns false for 'false'",
			envValue:     "false",
			setEnv:       true,
			defaultValue: true,
			want:         false,
		},
		{
			name:         "returns false for '0'",
			envValue:     "0",
			setEnv:       true,
			defaultValue: true,
			want:         false,
		},
		{
			name:         "returns false for 'no'",
			envValue:     "no",
			setEnv:       true,
			defaultValue: true,
			want:         false,
		},
		{
			name:         "returns default for invalid value",
			envValue:     "maybe",
			setEnv:       true,
			defaultValue: true,
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const testKey = "TEST_BOOL_VAR"
			if tt.setEnv {
				t.Setenv(testKey, tt.envValue)
			} else {
				_ = os.Unsetenv(testKey)
			}

			got := getEnvAsBool(testKey, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvAsBool(%q, %v) = %v, want %v", testKey, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestInitAuth_Disabled(t *testing.T) {
	// Ensure auth is disabled
	t.Setenv("AUTH_ENABLED", "false")

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	interceptor, err := initAuth(ctx, logger)
	if err != nil {
		t.Errorf("initAuth() error = %v, want nil", err)
	}

	if interceptor != nil {
		t.Errorf("initAuth() returned non-nil interceptor when auth is disabled")
	}
}

func TestInitAuth_DisabledByDefault(t *testing.T) {
	// Ensure AUTH_ENABLED is not set (use empty string to simulate unset)
	t.Setenv("AUTH_ENABLED", "")

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	interceptor, err := initAuth(ctx, logger)
	if err != nil {
		t.Errorf("initAuth() error = %v, want nil", err)
	}

	if interceptor != nil {
		t.Errorf("initAuth() returned non-nil interceptor when AUTH_ENABLED not set")
	}
}

func TestInitAuth_EnabledWithInvalidJWKSResponse(t *testing.T) {
	// Use httptest server to return invalid JWKS response (deterministic, no network flakiness)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer ts.Close()

	// Enable auth with httptest server URL
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("JWKS_URL", ts.URL)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	interceptor, err := initAuth(ctx, logger)

	// Should fail because JWKS response is invalid
	if err == nil {
		t.Errorf("initAuth() error = nil, want error for invalid JWKS response")
	}

	if interceptor != nil {
		t.Errorf("initAuth() returned non-nil interceptor on error")
	}
}

func TestInitAuth_UsesConfiguredValues(t *testing.T) {
	// This test verifies that configured values are read from environment
	// It only exercises cheap environment-parsing helpers, no network calls

	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("JWKS_URL", "http://localhost:18080/realms/meridian/protocol/openid-connect/certs")
	t.Setenv("JWKS_CACHE_TTL", "2h")
	t.Setenv("JWKS_REFRESH_TTL", "45m")
	t.Setenv("MULTI_ORG_MODE", "true")

	// Verify environment variables are read correctly
	if jwksURL := getEnvOrDefault("JWKS_URL", ""); jwksURL != "http://localhost:18080/realms/meridian/protocol/openid-connect/certs" {
		t.Errorf("JWKS_URL = %q, want configured value", jwksURL)
	}

	cacheTTL := getEnvAsDuration("JWKS_CACHE_TTL", time.Hour)
	if cacheTTL != 2*time.Hour {
		t.Errorf("JWKS_CACHE_TTL = %v, want 2h", cacheTTL)
	}

	refreshTTL := getEnvAsDuration("JWKS_REFRESH_TTL", 30*time.Minute)
	if refreshTTL != 45*time.Minute {
		t.Errorf("JWKS_REFRESH_TTL = %v, want 45m", refreshTTL)
	}

	multiOrgMode := getEnvAsBool("MULTI_ORG_MODE", false)
	if !multiOrgMode {
		t.Errorf("MULTI_ORG_MODE = %v, want true", multiOrgMode)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
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
		{"", slog.LevelInfo},
		{"invalid", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLogLevel(tt.input)
			if got != tt.want {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue string
		want         string
	}{
		{
			name:         "returns env value when set",
			envValue:     "custom-value",
			setEnv:       true,
			defaultValue: "default",
			want:         "custom-value",
		},
		{
			name:         "returns default when env empty",
			envValue:     "",
			setEnv:       true,
			defaultValue: "default",
			want:         "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const testKey = "TEST_STRING_VAR"
			if tt.setEnv {
				t.Setenv(testKey, tt.envValue)
			}

			got := getEnvOrDefault(testKey, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvOrDefault(%q, %q) = %q, want %q", testKey, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestGetEnvAsInt(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue int
		want         int
	}{
		{
			name:         "returns env value when valid int",
			envValue:     "42",
			setEnv:       true,
			defaultValue: 10,
			want:         42,
		},
		{
			name:         "returns default when env empty",
			envValue:     "",
			setEnv:       true,
			defaultValue: 10,
			want:         10,
		},
		{
			name:         "returns default when env invalid",
			envValue:     "not-a-number",
			setEnv:       true,
			defaultValue: 10,
			want:         10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const testKey = "TEST_INT_VAR"
			if tt.setEnv {
				t.Setenv(testKey, tt.envValue)
			}

			got := getEnvAsInt(testKey, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvAsInt(%q, %d) = %d, want %d", testKey, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestGetEnvAsDuration(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue time.Duration
		want         time.Duration
	}{
		{
			name:         "returns env value when valid duration",
			envValue:     "5m",
			setEnv:       true,
			defaultValue: time.Minute,
			want:         5 * time.Minute,
		},
		{
			name:         "returns default when env empty",
			envValue:     "",
			setEnv:       true,
			defaultValue: time.Minute,
			want:         time.Minute,
		},
		{
			name:         "returns default when env invalid",
			envValue:     "not-a-duration",
			setEnv:       true,
			defaultValue: time.Minute,
			want:         time.Minute,
		},
		{
			name:         "parses hours correctly",
			envValue:     "2h",
			setEnv:       true,
			defaultValue: time.Hour,
			want:         2 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const testKey = "TEST_DURATION_VAR"
			if tt.setEnv {
				t.Setenv(testKey, tt.envValue)
			}

			got := getEnvAsDuration(testKey, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvAsDuration(%q, %v) = %v, want %v", testKey, tt.defaultValue, got, tt.want)
			}
		})
	}
}

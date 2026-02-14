package main

import (
	"log/slog"
	"os"
	"testing"

	"github.com/meridianhub/meridian/services/party/config"
)

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

func TestVerificationConfigGracefulDegradation(t *testing.T) {
	// Ensure no verification env vars are set
	os.Unsetenv("VERIFICATION_PROVIDER")
	os.Unsetenv("VERIFICATION_WEBHOOK_SECRET")
	os.Unsetenv("VERIFICATION_WEBHOOK_URL")
	os.Unsetenv("VERIFICATION_API_KEY")
	os.Unsetenv("VERIFICATION_API_SECRET")

	cfg, err := config.LoadVerificationConfig()
	if err == nil {
		t.Fatal("expected error when VERIFICATION_PROVIDER is not set")
	}
	if cfg != nil {
		t.Fatal("expected nil config when loading fails")
	}
}

func TestVerificationConfigMockProvider(t *testing.T) {
	// Set mock provider env var
	t.Setenv("VERIFICATION_PROVIDER", "mock")

	cfg, err := config.LoadVerificationConfig()
	if err != nil {
		t.Fatalf("expected no error for mock provider, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config for mock provider")
	}
	if cfg.Provider != "mock" {
		t.Errorf("expected provider 'mock', got %q", cfg.Provider)
	}
	if !cfg.IsMock() {
		t.Error("expected IsMock() to return true")
	}
}

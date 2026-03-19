package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestNewHTTPHealthHandler_NilConfig(t *testing.T) {
	handler := newHTTPHealthHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var resp httpHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if resp.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if resp.VerificationEnabled {
		t.Error("expected verification_enabled to be false when config is nil")
	}
	if resp.VerificationProvider != "" {
		t.Errorf("expected empty verification_provider, got %q", resp.VerificationProvider)
	}
}

func TestNewHTTPHealthHandler_WithVerificationConfig(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "stripe",
	}

	handler := newHTTPHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp httpHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if resp.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if !resp.VerificationEnabled {
		t.Error("expected verification_enabled to be true when config is provided")
	}
	if resp.VerificationProvider != "stripe" {
		t.Errorf("expected verification_provider 'stripe', got %q", resp.VerificationProvider)
	}
}

func TestNewHTTPHealthHandler_JSONStructure(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "mock",
	}

	handler := newHTTPHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	// Decode into a raw map to verify exact JSON field names
	var raw map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode response as map: %v", err)
	}

	expectedFields := []string{"status", "timestamp", "verification_enabled", "verification_provider"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("expected JSON field %q to be present", field)
		}
	}

	// Verify no unexpected fields
	if len(raw) != len(expectedFields) {
		t.Errorf("expected %d fields, got %d: %v", len(expectedFields), len(raw), raw)
	}
}

func TestNewHTTPHealthHandler_OmitsProviderWhenNil(t *testing.T) {
	handler := newHTTPHealthHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	// Decode into raw map to confirm verification_provider is omitted (omitempty)
	var raw map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode response as map: %v", err)
	}

	if _, ok := raw["verification_provider"]; ok {
		t.Error("expected verification_provider to be omitted when config is nil")
	}

	// Should have exactly 3 fields (status, timestamp, verification_enabled)
	if len(raw) != 3 {
		t.Errorf("expected 3 fields when provider omitted, got %d: %v", len(raw), raw)
	}
}

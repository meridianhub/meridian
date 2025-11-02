package models

import (
	"os"
	"testing"
)

func TestGetEnvOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		envValue     string
		want         string
	}{
		{
			name:         "returns env value when set",
			key:          "TEST_VAR",
			defaultValue: "default",
			envValue:     "custom",
			want:         "custom",
		},
		{
			name:         "returns default when env not set",
			key:          "NONEXISTENT_VAR",
			defaultValue: "default",
			envValue:     "",
			want:         "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.envValue != "" {
				if err := os.Setenv(tt.key, tt.envValue); err != nil {
					t.Fatalf("Failed to set env var: %v", err)
				}
				defer func() {
					if err := os.Unsetenv(tt.key); err != nil {
						t.Errorf("Failed to unset env var: %v", err)
					}
				}()
			}

			// Test
			got := getEnvOrDefault(tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvOrDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAtlasDSN(t *testing.T) {
	// Save original env vars
	originalHost := os.Getenv("ATLAS_DB_HOST")
	originalUser := os.Getenv("ATLAS_DB_USER")
	originalPassword := os.Getenv("ATLAS_DB_PASSWORD")
	originalDBName := os.Getenv("ATLAS_DB_NAME")
	originalPort := os.Getenv("ATLAS_DB_PORT")

	// Cleanup
	defer func() {
		_ = os.Setenv("ATLAS_DB_HOST", originalHost)
		_ = os.Setenv("ATLAS_DB_USER", originalUser)
		_ = os.Setenv("ATLAS_DB_PASSWORD", originalPassword)
		_ = os.Setenv("ATLAS_DB_NAME", originalDBName)
		_ = os.Setenv("ATLAS_DB_PORT", originalPort)
	}()

	tests := []struct {
		name    string
		envVars map[string]string
		wantDSN string
	}{
		{
			name:    "uses defaults when no env vars set",
			envVars: map[string]string{},
			wantDSN: "host=localhost user=postgres password=postgres dbname=atlas_dev port=5432 sslmode=disable",
		},
		{
			name: "uses custom values from env vars",
			envVars: map[string]string{
				"ATLAS_DB_HOST":     "customhost",
				"ATLAS_DB_USER":     "customuser",
				"ATLAS_DB_PASSWORD": "custompass",
				"ATLAS_DB_NAME":     "customdb",
				"ATLAS_DB_PORT":     "5433",
			},
			wantDSN: "host=customhost user=customuser password=custompass dbname=customdb port=5433 sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars
			_ = os.Unsetenv("ATLAS_DB_HOST")
			_ = os.Unsetenv("ATLAS_DB_USER")
			_ = os.Unsetenv("ATLAS_DB_PASSWORD")
			_ = os.Unsetenv("ATLAS_DB_NAME")
			_ = os.Unsetenv("ATLAS_DB_PORT")

			// Set test env vars
			for k, v := range tt.envVars {
				_ = os.Setenv(k, v)
			}

			got := getAtlasDSN()
			if got != tt.wantDSN {
				t.Errorf("getAtlasDSN() = %v, want %v", got, tt.wantDSN)
			}
		})
	}
}

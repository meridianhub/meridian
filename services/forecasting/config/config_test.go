package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Ensure env var is unset so default is used
	os.Unsetenv("DATABASE_URL")

	cfg := LoadConfig()
	assert.Equal(t, "postgres://meridian_user@localhost:26257/meridian_forecasting?sslmode=disable", cfg.DatabaseURL)
}

func TestLoadConfig_FromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://custom@db:5432/forecast")

	cfg := LoadConfig()
	assert.Equal(t, "postgres://custom@db:5432/forecast", cfg.DatabaseURL)
}

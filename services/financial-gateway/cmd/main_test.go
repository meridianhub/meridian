package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/services/financial-gateway/config"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"  debug  ", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseLogLevel(tt.input))
		})
	}
}

func TestEnvTenantConfigProvider_WithAccountID(t *testing.T) {
	p := &envTenantConfigProvider{
		webhookSecret: "whsec_test",
		accountID:     "acct_123",
	}

	cfg, err := p.GetTenantConfig("any-tenant")
	assert.NoError(t, err)
	assert.Equal(t, "acct_123", cfg.ConnectedAccountID)
	assert.Equal(t, "whsec_test", cfg.WebhookEndpointSecret)
}

func TestEnvTenantConfigProvider_EmptyAccountID(t *testing.T) {
	p := &envTenantConfigProvider{
		webhookSecret: "whsec_test",
		accountID:     "",
	}

	_, err := p.GetTenantConfig("any-tenant")
	assert.ErrorIs(t, err, stripeadapter.ErrTenantConfigNotFound)
}

func TestCreateTenantConfigProvider_EnvFallback(t *testing.T) {
	logger := slog.Default()
	cfg := config.Config{ControlPlaneAddr: ""}

	provider, conn, err := createTenantConfigProvider(cfg, logger)
	require.NoError(t, err)
	assert.NotNil(t, provider)
	assert.Nil(t, conn) // no gRPC conn when using env fallback
}

func TestCreateTenantConfigProvider_WithControlPlane(t *testing.T) {
	logger := slog.Default()
	cfg := config.Config{ControlPlaneAddr: "localhost:50099"}

	provider, conn, err := createTenantConfigProvider(cfg, logger)
	require.NoError(t, err)
	assert.NotNil(t, provider)
	assert.NotNil(t, conn)
	_ = conn.Close()
}

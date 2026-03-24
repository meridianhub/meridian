package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig_returns_sensible_defaults(t *testing.T) {
	connStr := "postgresql://user:pass@host:26257/testdb?sslmode=require"
	cfg := DefaultConfig(connStr)

	assert.Equal(t, connStr, cfg.ConnectionString)
	assert.Equal(t, 50, cfg.MaxConnections)
	assert.Equal(t, 5, cfg.MinConnections)
	assert.Greater(t, cfg.ConnectionTimeout, time.Duration(0))
	assert.Greater(t, cfg.HealthCheckInterval, time.Duration(0))
	assert.Equal(t, 1*time.Hour, cfg.MaxConnectionLifetime)
	assert.Equal(t, 10*time.Minute, cfg.MaxConnectionIdleTime)
	assert.Greater(t, cfg.StatementTimeout, time.Duration(0))
}

func TestDefaultConfig_preserves_connection_string(t *testing.T) {
	tests := []struct {
		name    string
		connStr string
	}{
		{"standard", "postgresql://user:pass@host:26257/mydb"},
		{"with_params", "postgresql://user:pass@host:26257/mydb?sslmode=require&pool_max_conns=10"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig(tt.connStr)
			assert.Equal(t, tt.connStr, cfg.ConnectionString)
		})
	}
}

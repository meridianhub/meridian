package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_RequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := LoadConfig()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyDatabaseURL)
}

func TestLoadConfig_Success(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user@localhost:26257/reconciliation?sslmode=disable")
	t.Setenv("GRPC_PORT", "50060")
	t.Setenv("KAFKA_BROKERS", "kafka:9092")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("POSITION_KEEPING_URL", "position-keeping:50053")
	t.Setenv("SETTLEMENT_SCHEDULER_ENABLED", "true")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "50060", cfg.Server.Port)
	assert.Equal(t, "postgres://user@localhost:26257/reconciliation?sslmode=disable", cfg.Database.URL)
	assert.Equal(t, "kafka:9092", cfg.Kafka.Brokers)
	assert.True(t, cfg.Kafka.Enabled)
	assert.Equal(t, "redis://localhost:6379", cfg.Redis.URL)
	assert.True(t, cfg.Redis.Enabled)
	assert.Equal(t, "position-keeping:50053", cfg.Services.PositionKeepingURL)
	assert.True(t, cfg.Scheduler.Enabled)
}

func TestLoadConfig_DefaultPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user@localhost:26257/reconciliation?sslmode=disable")
	// Don't set GRPC_PORT - should default to 50060

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "50060", cfg.Server.Port)
}

func TestLoadConfig_KafkaDisabledWhenBrokersEmpty(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user@localhost:26257/reconciliation?sslmode=disable")
	t.Setenv("KAFKA_BROKERS", "")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.False(t, cfg.Kafka.Enabled)
}

func TestLoadConfig_RedisDisabledWhenURLEmpty(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user@localhost:26257/reconciliation?sslmode=disable")
	t.Setenv("REDIS_URL", "")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.False(t, cfg.Redis.Enabled)
}

func TestValidate_InvalidMaxOpenConns(t *testing.T) {
	cfg := &Config{
		Server:        ServerConfig{Port: "50060"},
		Database:      DatabaseConfig{URL: "postgres://...", MaxOpenConns: 0, MaxIdleConns: 0},
		Observability: ObservabilityConfig{MetricsPort: "9090"},
	}

	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrInvalidMaxOpenConns)
}

func TestValidate_InvalidMaxIdleConns(t *testing.T) {
	cfg := &Config{
		Server:        ServerConfig{Port: "50060"},
		Database:      DatabaseConfig{URL: "postgres://...", MaxOpenConns: 25, MaxIdleConns: -1},
		Observability: ObservabilityConfig{MetricsPort: "9090"},
	}

	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrInvalidMaxIdleConns)
}

func TestValidate_EmptyMetricsPort(t *testing.T) {
	cfg := &Config{
		Server:        ServerConfig{Port: "50060"},
		Database:      DatabaseConfig{URL: "postgres://...", MaxOpenConns: 25, MaxIdleConns: 5},
		Observability: ObservabilityConfig{MetricsPort: ""},
	}

	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrInvalidMetricsPort)
}

func TestValidate_Success(t *testing.T) {
	cfg := &Config{
		Server:        ServerConfig{Port: "50060"},
		Database:      DatabaseConfig{URL: "postgres://...", MaxOpenConns: 25, MaxIdleConns: 5},
		Observability: ObservabilityConfig{MetricsPort: "9090"},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}

// Package bootstrap provides shared infrastructure initialization utilities for Meridian services.
// It consolidates duplicated bootstrap patterns for database and Redis connections across services.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DatabaseConfig holds configuration for PostgreSQL/CockroachDB database connections.
type DatabaseConfig struct {
	// DSN is the database connection string.
	DSN string

	// MaxOpenConns is the maximum number of open connections to the database.
	// Zero means unlimited.
	MaxOpenConns int

	// MaxIdleConns is the maximum number of idle connections in the pool.
	MaxIdleConns int

	// ConnMaxLifetime is the maximum amount of time a connection may be reused.
	ConnMaxLifetime time.Duration

	// ConnMaxIdleTime is the maximum amount of time a connection may be idle.
	ConnMaxIdleTime time.Duration

	// Logger is an optional structured logger for connection events.
	// If nil, connection pool configuration will not be logged.
	Logger *slog.Logger
}

// DefaultDatabaseConfig returns a DatabaseConfig populated from environment variables
// with sensible defaults suitable for production workloads.
//
// Environment variables:
//   - DATABASE_URL: Connection string. Default port is 26257 for CockroachDB (default)
//     or 5432 when DB_DRIVER=postgres.
//   - DB_DRIVER: Database driver ("cockroachdb" default, "postgres" for PostgreSQL).
//   - DB_MAX_OPEN_CONNS: Maximum open connections (default: 25)
//   - DB_MAX_IDLE_CONNS: Maximum idle connections (default: 5)
//   - DB_CONN_MAX_LIFETIME: Connection max lifetime (default: 5m)
//   - DB_CONN_MAX_IDLE_TIME: Connection max idle time (default: 10m)
func DefaultDatabaseConfig() DatabaseConfig {
	defaultDSN := "postgres://meridian_user@localhost:26257/meridian?sslmode=disable"
	if strings.ToLower(os.Getenv("DB_DRIVER")) == "postgres" || strings.ToLower(os.Getenv("DB_DRIVER")) == "postgresql" {
		defaultDSN = "postgres://meridian_user@localhost:5432/meridian?sslmode=disable"
	}
	return DatabaseConfig{
		DSN:             env.GetEnvOrDefault("DATABASE_URL", defaultDSN),
		MaxOpenConns:    env.GetEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		MaxIdleConns:    env.GetEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime: env.GetEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		ConnMaxIdleTime: env.GetEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute),
	}
}

// NewDatabase creates a new GORM database connection with the provided configuration.
// It configures connection pooling, verifies connectivity with a 5-second timeout ping,
// and returns an error if the connection cannot be established.
//
// The returned *gorm.DB is configured with:
//   - SkipDefaultTransaction: true (for better performance)
//   - PrepareStmt: true (for prepared statement caching)
//   - No GORM logger (services should use slog)
func NewDatabase(ctx context.Context, cfg DatabaseConfig) (*gorm.DB, error) {
	// Open database connection
	db, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
		Logger:                 nil,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	if cfg.Logger != nil {
		cfg.Logger.Info("database connection pool configured",
			"max_open_conns", cfg.MaxOpenConns,
			"max_idle_conns", cfg.MaxIdleConns,
			"conn_max_lifetime", cfg.ConnMaxLifetime,
			"conn_max_idle_time", cfg.ConnMaxIdleTime)
	}

	// Verify connection with timeout
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// CloseDatabase gracefully closes the database connection.
// It logs any errors encountered during closing if a logger is provided.
func CloseDatabase(db *gorm.DB, logger *slog.Logger) {
	if db == nil {
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		if logger != nil {
			logger.Error("failed to get database instance for closing", "error", err)
		}
		return
	}

	if err := sqlDB.Close(); err != nil {
		if logger != nil {
			logger.Error("failed to close database connection", "error", err)
		}
	} else if logger != nil {
		logger.Info("database connection closed")
	}
}

// RedisConfig holds configuration for Redis client connections.
type RedisConfig struct {
	// URL is the Redis connection URL (e.g., redis://localhost:6379).
	URL string

	// Password overrides the password from URL if non-empty.
	Password string

	// DB is the Redis database number (0-15).
	DB int

	// PoolSize is the maximum number of socket connections.
	PoolSize int

	// MinIdleConns is the minimum number of idle connections to maintain.
	MinIdleConns int

	// Logger is an optional structured logger for connection events.
	Logger *slog.Logger
}

// DefaultRedisConfig returns a RedisConfig populated from environment variables
// with sensible defaults.
//
// Environment variables:
//   - REDIS_URL: Redis connection URL (default: redis://localhost:6379)
//   - REDIS_PASSWORD: Password override (default: empty)
//   - REDIS_DB: Database number (default: 0)
//   - REDIS_POOL_SIZE: Pool size (default: 10)
//   - REDIS_MIN_IDLE_CONNS: Minimum idle connections (default: 2)
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		URL:          env.GetEnvOrDefault("REDIS_URL", "redis://localhost:6379"),
		Password:     env.GetEnvOrDefault("REDIS_PASSWORD", ""),
		DB:           env.GetEnvAsInt("REDIS_DB", 0),
		PoolSize:     env.GetEnvAsInt("REDIS_POOL_SIZE", 10),
		MinIdleConns: env.GetEnvAsInt("REDIS_MIN_IDLE_CONNS", 2),
	}
}

// NewRedisClient creates a new Redis client with the provided configuration.
// It parses the URL, applies configuration overrides, and verifies connectivity
// with a 5-second timeout ping.
//
// The Password field, if non-empty, overrides any password in the URL.
// This allows separating secrets from connection strings.
func NewRedisClient(ctx context.Context, cfg RedisConfig) (*redis.Client, error) {
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
	}

	// Apply configuration overrides
	if cfg.Password != "" {
		opt.Password = cfg.Password
	}
	opt.DB = cfg.DB
	opt.PoolSize = cfg.PoolSize
	opt.MinIdleConns = cfg.MinIdleConns

	client := redis.NewClient(opt)

	// Verify connection with timeout
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		// Close client on ping failure to avoid leaking connections
		_ = client.Close()
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	if cfg.Logger != nil {
		cfg.Logger.Info("Redis client connected",
			"addr", opt.Addr,
			"db", cfg.DB,
			"pool_size", cfg.PoolSize,
			"min_idle_conns", cfg.MinIdleConns)
	}

	return client, nil
}

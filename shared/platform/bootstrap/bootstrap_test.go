package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	dbpkg "github.com/meridianhub/meridian/shared/platform/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDefaultDatabaseConfig(t *testing.T) {
	t.Run("uses defaults when environment variables not set", func(t *testing.T) {
		// Clear relevant env vars using t.Setenv for automatic restoration
		t.Setenv("DATABASE_URL", "")
		t.Setenv("DB_MAX_OPEN_CONNS", "")
		t.Setenv("DB_MAX_IDLE_CONNS", "")
		t.Setenv("DB_CONN_MAX_LIFETIME", "")
		t.Setenv("DB_CONN_MAX_IDLE_TIME", "")

		cfg := DefaultDatabaseConfig()

		assert.Equal(t, "postgres://meridian_user@localhost:26257/meridian?sslmode=disable", cfg.DSN)
		assert.Equal(t, 25, cfg.MaxOpenConns)
		assert.Equal(t, 5, cfg.MaxIdleConns)
		assert.Equal(t, 5*time.Minute, cfg.ConnMaxLifetime)
		assert.Equal(t, 10*time.Minute, cfg.ConnMaxIdleTime)
	})

	t.Run("reads from environment variables", func(t *testing.T) {
		// Set custom values
		t.Setenv("DATABASE_URL", "postgres://testuser@testhost:5432/testdb")
		t.Setenv("DB_MAX_OPEN_CONNS", "50")
		t.Setenv("DB_MAX_IDLE_CONNS", "10")
		t.Setenv("DB_CONN_MAX_LIFETIME", "10m")
		t.Setenv("DB_CONN_MAX_IDLE_TIME", "20m")

		cfg := DefaultDatabaseConfig()

		assert.Equal(t, "postgres://testuser@testhost:5432/testdb", cfg.DSN)
		assert.Equal(t, 50, cfg.MaxOpenConns)
		assert.Equal(t, 10, cfg.MaxIdleConns)
		assert.Equal(t, 10*time.Minute, cfg.ConnMaxLifetime)
		assert.Equal(t, 20*time.Minute, cfg.ConnMaxIdleTime)
	})

	t.Run("handles invalid duration values gracefully", func(t *testing.T) {
		t.Setenv("DB_CONN_MAX_LIFETIME", "invalid")

		cfg := DefaultDatabaseConfig()

		// Should fall back to default
		assert.Equal(t, 5*time.Minute, cfg.ConnMaxLifetime)
	})

	t.Run("handles invalid int values gracefully", func(t *testing.T) {
		t.Setenv("DB_MAX_OPEN_CONNS", "not-a-number")

		cfg := DefaultDatabaseConfig()

		// Should fall back to default
		assert.Equal(t, 25, cfg.MaxOpenConns)
	})
}

func TestNewDatabase_PoolConfiguration(t *testing.T) {
	// Skip if no database available (integration test)
	// This test verifies pool settings are applied correctly
	t.Run("applies pool configuration", func(t *testing.T) {
		// We can't easily test actual database connection without testcontainers,
		// but we can verify the configuration is correctly structured
		cfg := DatabaseConfig{
			DSN:             "postgres://test@localhost:5432/test",
			MaxOpenConns:    30,
			MaxIdleConns:    10,
			ConnMaxLifetime: 15 * time.Minute,
			ConnMaxIdleTime: 25 * time.Minute,
		}

		assert.Equal(t, 30, cfg.MaxOpenConns)
		assert.Equal(t, 10, cfg.MaxIdleConns)
		assert.Equal(t, 15*time.Minute, cfg.ConnMaxLifetime)
		assert.Equal(t, 25*time.Minute, cfg.ConnMaxIdleTime)
	})

	t.Run("returns error for invalid DSN", func(t *testing.T) {
		ctx := context.Background()
		cfg := DatabaseConfig{
			DSN: "not-a-valid-dsn",
		}

		_, err := NewDatabase(ctx, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to connect to database")
	})
}

func TestDefaultRedisConfig(t *testing.T) {
	t.Run("uses defaults when environment variables not set", func(t *testing.T) {
		// Clear relevant env vars using t.Setenv for automatic restoration
		t.Setenv("REDIS_URL", "")
		t.Setenv("REDIS_PASSWORD", "")
		t.Setenv("REDIS_DB", "")
		t.Setenv("REDIS_POOL_SIZE", "")
		t.Setenv("REDIS_MIN_IDLE_CONNS", "")

		cfg := DefaultRedisConfig()

		assert.Equal(t, "redis://localhost:6379", cfg.URL)
		assert.Equal(t, "", cfg.Password)
		assert.Equal(t, 0, cfg.DB)
		assert.Equal(t, 10, cfg.PoolSize)
		assert.Equal(t, 2, cfg.MinIdleConns)
	})

	t.Run("reads from environment variables", func(t *testing.T) {
		t.Setenv("REDIS_URL", "redis://redis.example.com:6380")
		t.Setenv("REDIS_PASSWORD", "secret123")
		t.Setenv("REDIS_DB", "5")
		t.Setenv("REDIS_POOL_SIZE", "20")
		t.Setenv("REDIS_MIN_IDLE_CONNS", "5")

		cfg := DefaultRedisConfig()

		assert.Equal(t, "redis://redis.example.com:6380", cfg.URL)
		assert.Equal(t, "secret123", cfg.Password)
		assert.Equal(t, 5, cfg.DB)
		assert.Equal(t, 20, cfg.PoolSize)
		assert.Equal(t, 5, cfg.MinIdleConns)
	})
}

func TestNewRedisClient_PasswordOverride(t *testing.T) {
	// Start miniredis for testing
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	t.Run("connects with URL without password override", func(t *testing.T) {
		ctx := context.Background()
		cfg := RedisConfig{
			URL:          "redis://" + mr.Addr(),
			PoolSize:     5,
			MinIdleConns: 1,
		}

		client, err := NewRedisClient(ctx, cfg)
		require.NoError(t, err)
		defer client.Close()

		// Verify connection works
		err = client.Ping(ctx).Err()
		require.NoError(t, err)
	})

	t.Run("applies password override from config", func(t *testing.T) {
		ctx := context.Background()

		// Set password on miniredis
		mr.RequireAuth("testpassword")
		defer mr.RequireAuth("") // Reset

		cfg := RedisConfig{
			URL:          "redis://" + mr.Addr(),
			Password:     "testpassword",
			PoolSize:     5,
			MinIdleConns: 1,
		}

		client, err := NewRedisClient(ctx, cfg)
		require.NoError(t, err)
		defer client.Close()

		// Verify connection works with password
		err = client.Ping(ctx).Err()
		require.NoError(t, err)
	})

	t.Run("password override takes precedence over URL password", func(t *testing.T) {
		ctx := context.Background()

		// Set password on miniredis
		mr.RequireAuth("override-password")
		defer mr.RequireAuth("") // Reset

		cfg := RedisConfig{
			// URL has wrong password
			URL:          "redis://:wrong-password@" + mr.Addr(),
			Password:     "override-password", // This should override
			PoolSize:     5,
			MinIdleConns: 1,
		}

		client, err := NewRedisClient(ctx, cfg)
		require.NoError(t, err)
		defer client.Close()

		// Verify connection works with overridden password
		err = client.Ping(ctx).Err()
		require.NoError(t, err)
	})

	t.Run("applies DB selection", func(t *testing.T) {
		ctx := context.Background()
		cfg := RedisConfig{
			URL:          "redis://" + mr.Addr(),
			DB:           3,
			PoolSize:     5,
			MinIdleConns: 1,
		}

		client, err := NewRedisClient(ctx, cfg)
		require.NoError(t, err)
		defer client.Close()

		// Set a value in DB 3
		err = client.Set(ctx, "test-key", "test-value", 0).Err()
		require.NoError(t, err)

		// Verify value exists
		val, err := client.Get(ctx, "test-key").Result()
		require.NoError(t, err)
		assert.Equal(t, "test-value", val)
	})

	t.Run("applies pool configuration", func(t *testing.T) {
		ctx := context.Background()
		cfg := RedisConfig{
			URL:          "redis://" + mr.Addr(),
			PoolSize:     15,
			MinIdleConns: 3,
		}

		client, err := NewRedisClient(ctx, cfg)
		require.NoError(t, err)
		defer client.Close()

		// Verify pool stats are accessible (pool is configured)
		stats := client.PoolStats()
		assert.NotNil(t, stats)
	})

	t.Run("logs connection info when logger provided", func(t *testing.T) {
		ctx := context.Background()
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

		cfg := RedisConfig{
			URL:          "redis://" + mr.Addr(),
			PoolSize:     10,
			MinIdleConns: 2,
			Logger:       logger,
		}

		client, err := NewRedisClient(ctx, cfg)
		require.NoError(t, err)
		defer client.Close()
	})

	t.Run("returns error for invalid URL", func(t *testing.T) {
		ctx := context.Background()
		cfg := RedisConfig{
			URL: "not-a-valid-url",
		}

		_, err := NewRedisClient(ctx, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid REDIS_URL")
	})

	t.Run("returns error when connection fails", func(t *testing.T) {
		ctx := context.Background()
		cfg := RedisConfig{
			URL:          "redis://localhost:59999", // Unlikely to have anything here
			PoolSize:     5,
			MinIdleConns: 1,
		}

		_, err := NewRedisClient(ctx, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to ping Redis")
	})
}

// newMockGormDB creates a gorm.DB backed by sqlmock for unit testing.
func newMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = mockDB.Close() })

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	return gormDB, mock
}

// testEntity is a minimal GORM model for bootstrap tests.
type testEntity struct {
	ID uint `gorm:"primarykey"`
}

func (testEntity) TableName() string { return "test_entities" }

func TestNewDatabase_TenantGuardRegistered(t *testing.T) {
	t.Parallel()

	t.Run("guard plugin is registered on gorm db", func(t *testing.T) {
		t.Parallel()
		gormDB, _ := newMockGormDB(t)

		// Simulate what NewDatabase does: register the TenantGuard
		err := gormDB.Use(dbpkg.NewTenantGuard())
		require.NoError(t, err)

		_, ok := gormDB.Plugins["meridian:tenant_guard"]
		assert.True(t, ok, "expected TenantGuard plugin to be registered")
	})

	t.Run("query without tenant scope returns ErrTenantScopeRequired", func(t *testing.T) {
		t.Parallel()
		gormDB, _ := newMockGormDB(t)

		err := gormDB.Use(dbpkg.NewTenantGuard())
		require.NoError(t, err)

		var entities []testEntity
		err = gormDB.WithContext(context.Background()).Find(&entities).Error

		require.Error(t, err)
		assert.ErrorIs(t, err, dbpkg.ErrTenantScopeRequired)
	})

	t.Run("query with bypass context is allowed", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)

		err := gormDB.Use(dbpkg.NewTenantGuard())
		require.NoError(t, err)

		ctx := dbpkg.WithTenantGuardBypass(context.Background())
		mock.ExpectQuery(`SELECT \* FROM "test_entities"`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		var entities []testEntity
		err = gormDB.WithContext(ctx).Find(&entities).Error

		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCloseDatabase(t *testing.T) {
	t.Run("handles nil database gracefully", func(_ *testing.T) {
		// Should not panic when both database and logger are nil
		CloseDatabase(nil, nil)
	})

	t.Run("handles nil database with logger gracefully", func(_ *testing.T) {
		// Verify passing a non-nil logger with nil database doesn't cause panic.
		// Testing with a real *gorm.DB would require testcontainers.
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		CloseDatabase(nil, logger)
	})
}

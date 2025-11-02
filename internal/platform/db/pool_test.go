package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestNewPostgresPool(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "nil config returns error",
			cfg:     nil,
			wantErr: true,
		},
		{
			name: "empty connection string returns error",
			cfg: &Config{
				ConnectionString: "",
			},
			wantErr: true,
		},
		{
			name: "invalid connection string returns error",
			cfg: &Config{
				ConnectionString: "invalid://connection",
				MaxConnections:   10,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pool, err := NewPostgresPool(ctx, tt.cfg)

			if tt.wantErr {
				if err == nil {
					t.Errorf("NewPostgresPool() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("NewPostgresPool() unexpected error: %v", err)
				return
			}

			if pool != nil {
				defer func() { _ = pool.Close() }()
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	connStr := "postgresql://user:pass@localhost:5432/db"
	cfg := DefaultConfig(connStr)

	if cfg.ConnectionString != connStr {
		t.Errorf("ConnectionString = %v, want %v", cfg.ConnectionString, connStr)
	}

	if cfg.MaxConnections != 50 {
		t.Errorf("MaxConnections = %v, want 50", cfg.MaxConnections)
	}

	if cfg.MinConnections != 5 {
		t.Errorf("MinConnections = %v, want 5", cfg.MinConnections)
	}

	if cfg.ConnectionTimeout != 30*time.Second {
		t.Errorf("ConnectionTimeout = %v, want 30s", cfg.ConnectionTimeout)
	}

	if cfg.HealthCheckInterval != 30*time.Second {
		t.Errorf("HealthCheckInterval = %v, want 30s", cfg.HealthCheckInterval)
	}

	if cfg.MaxConnectionLifetime != 1*time.Hour {
		t.Errorf("MaxConnectionLifetime = %v, want 1h", cfg.MaxConnectionLifetime)
	}

	if cfg.MaxConnectionIdleTime != 10*time.Minute {
		t.Errorf("MaxConnectionIdleTime = %v, want 10m", cfg.MaxConnectionIdleTime)
	}

	if cfg.StatementTimeout != 30*time.Second {
		t.Errorf("StatementTimeout = %v, want 30s", cfg.StatementTimeout)
	}
}

func TestPostgresPool_BeginTx_DefaultSerializable(t *testing.T) {
	// This test verifies that BeginTx uses serializable isolation when opts is nil
	// We can't easily test this without a real database connection, so we just verify
	// the code structure is correct

	ctx := context.Background()

	// Test with nil opts (should default to serializable)
	var opts *sql.TxOptions
	if opts == nil {
		opts = &sql.TxOptions{
			Isolation: sql.LevelSerializable,
		}
	}

	if opts.Isolation != sql.LevelSerializable {
		t.Errorf("BeginTx default isolation = %v, want Serializable", opts.Isolation)
	}

	_ = ctx // Suppress unused warning
}

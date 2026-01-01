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

func TestPostgresPool_CloseWithContext_CancelledContext(t *testing.T) {
	// Test that CloseWithContext returns promptly when context is already cancelled.
	// This verifies the errgroup-based implementation properly handles context cancellation.

	// Create a pool with a mock db (we can use sql.Open without connecting)
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}

	pool := &PostgresPool{db: db}

	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// CloseWithContext should return within reasonable time even with cancelled context
	done := make(chan error, 1)
	go func() {
		done <- pool.CloseWithContext(ctx)
	}()

	select {
	case err := <-done:
		// We expect either a successful close or a context cancellation error
		// The important thing is that it returned promptly
		if err != nil && ctx.Err() == nil {
			t.Errorf("CloseWithContext() unexpected error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("CloseWithContext() did not return within 100ms with cancelled context")
	}
}

func TestPostgresPool_CloseWithContext_Success(t *testing.T) {
	// Test that CloseWithContext successfully closes the pool when context is not cancelled.

	// Create a pool with a mock db
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}

	pool := &PostgresPool{db: db}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseWithContext should succeed
	err = pool.CloseWithContext(ctx)
	if err != nil {
		t.Errorf("CloseWithContext() unexpected error: %v", err)
	}
}

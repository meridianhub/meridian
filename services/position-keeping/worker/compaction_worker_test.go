package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/require"
)

// mockPool creates a minimal pool configuration for testing
// Note: This does not actually connect to a database
func mockPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	// Create a pool config with a test URL - parsing only, no actual connection
	config, err := pgxpool.ParseConfig("postgres://test:test@localhost:5432/testdb")
	if err != nil {
		t.Fatalf("failed to parse pool config: %v", err)
	}
	// Create pool without connecting (for constructor tests only)
	// The pool will fail on actual use, but that's fine for unit tests
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		// Skip test if we can't create a pool (e.g., no PostgreSQL available)
		t.Skipf("skipping test: cannot create pool: %v", err)
	}
	return pool
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

func validConfig() CompactionConfig {
	return CompactionConfig{
		RunInterval:       5 * time.Minute,
		FragmentThreshold: 100,
		BatchSize:         50,
	}
}

func TestNewCompactionWorker_Success(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()
	config := validConfig()

	worker, err := NewCompactionWorker(pool, config, logger)
	if err != nil {
		t.Fatalf("NewCompactionWorker() error = %v, want nil", err)
	}
	if worker == nil {
		t.Fatal("NewCompactionWorker() returned nil worker")
	}

	if worker.pool != pool {
		t.Error("worker.pool does not match provided pool")
	}

	if worker.config.RunInterval != config.RunInterval {
		t.Errorf("worker.config.RunInterval = %v, want %v", worker.config.RunInterval, config.RunInterval)
	}

	if worker.config.FragmentThreshold != config.FragmentThreshold {
		t.Errorf("worker.config.FragmentThreshold = %d, want %d", worker.config.FragmentThreshold, config.FragmentThreshold)
	}

	if worker.config.BatchSize != config.BatchSize {
		t.Errorf("worker.config.BatchSize = %d, want %d", worker.config.BatchSize, config.BatchSize)
	}
}

func TestNewCompactionWorker_NilPool(t *testing.T) {
	logger := testLogger()
	config := validConfig()

	worker, err := NewCompactionWorker(nil, config, logger)
	if err == nil {
		t.Error("NewCompactionWorker() error = nil, want error for nil pool")
	}
	if !errors.Is(err, ErrNilPool) {
		t.Errorf("NewCompactionWorker() error = %v, want ErrNilPool", err)
	}
	if worker != nil {
		t.Error("NewCompactionWorker() returned non-nil worker for nil pool")
	}
}

func TestNewCompactionWorker_NilLogger(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	config := validConfig()

	worker, err := NewCompactionWorker(pool, config, nil)
	if err == nil {
		t.Error("NewCompactionWorker() error = nil, want error for nil logger")
	}
	if !errors.Is(err, ErrNilLogger) {
		t.Errorf("NewCompactionWorker() error = %v, want ErrNilLogger", err)
	}
	if worker != nil {
		t.Error("NewCompactionWorker() returned non-nil worker for nil logger")
	}
}

func TestNewCompactionWorker_InvalidRunInterval(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()

	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"zero", 0},
		{"negative", -1 * time.Second},
		{"negative minutes", -5 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := CompactionConfig{
				RunInterval:       tt.interval,
				FragmentThreshold: 100,
				BatchSize:         50,
			}

			worker, err := NewCompactionWorker(pool, config, logger)
			if err == nil {
				t.Error("NewCompactionWorker() error = nil, want error for invalid run interval")
			}
			if !errors.Is(err, ErrInvalidRunInterval) {
				t.Errorf("NewCompactionWorker() error = %v, want ErrInvalidRunInterval", err)
			}
			if worker != nil {
				t.Error("NewCompactionWorker() returned non-nil worker for invalid run interval")
			}
		})
	}
}

func TestNewCompactionWorker_InvalidFragmentThreshold(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()

	tests := []struct {
		name      string
		threshold int
	}{
		{"zero", 0},
		{"negative", -1},
		{"negative large", -100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := CompactionConfig{
				RunInterval:       5 * time.Minute,
				FragmentThreshold: tt.threshold,
				BatchSize:         50,
			}

			worker, err := NewCompactionWorker(pool, config, logger)
			if err == nil {
				t.Error("NewCompactionWorker() error = nil, want error for invalid fragment threshold")
			}
			if !errors.Is(err, ErrInvalidFragmentThreshold) {
				t.Errorf("NewCompactionWorker() error = %v, want ErrInvalidFragmentThreshold", err)
			}
			if worker != nil {
				t.Error("NewCompactionWorker() returned non-nil worker for invalid fragment threshold")
			}
		})
	}
}

func TestNewCompactionWorker_InvalidBatchSize(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()

	tests := []struct {
		name      string
		batchSize int
	}{
		{"zero", 0},
		{"negative", -1},
		{"negative large", -50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := CompactionConfig{
				RunInterval:       5 * time.Minute,
				FragmentThreshold: 100,
				BatchSize:         tt.batchSize,
			}

			worker, err := NewCompactionWorker(pool, config, logger)
			if err == nil {
				t.Error("NewCompactionWorker() error = nil, want error for invalid batch size")
			}
			if !errors.Is(err, ErrInvalidBatchSize) {
				t.Errorf("NewCompactionWorker() error = %v, want ErrInvalidBatchSize", err)
			}
			if worker != nil {
				t.Error("NewCompactionWorker() returned non-nil worker for invalid batch size")
			}
		})
	}
}

func TestCompactionWorker_StartAlreadyRunning(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()
	config := validConfig()

	worker, err := NewCompactionWorker(pool, config, logger)
	if err != nil {
		t.Fatalf("NewCompactionWorker() error = %v", err)
	}

	// Set running state manually
	worker.mu.Lock()
	worker.running = true
	worker.mu.Unlock()

	// Try to start again
	err = worker.Start(context.Background())
	if err == nil {
		t.Error("Start() error = nil, want error for already running worker")
	}
	if !errors.Is(err, ErrWorkerAlreadyRunning) {
		t.Errorf("Start() error = %v, want ErrWorkerAlreadyRunning", err)
	}
}

func TestCompactionWorker_StopIdempotent(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()
	config := validConfig()

	worker, err := NewCompactionWorker(pool, config, logger)
	if err != nil {
		t.Fatalf("NewCompactionWorker() error = %v", err)
	}

	// Stop should be idempotent (safe to call multiple times)
	// This should not panic
	worker.Stop()
	worker.Stop()
	worker.Stop()
}

func TestCompactionWorker_ContextCancellation(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()
	config := CompactionConfig{
		RunInterval:       100 * time.Millisecond, // Fast interval for testing
		FragmentThreshold: 100,
		BatchSize:         50,
	}

	worker, err := NewCompactionWorker(pool, config, logger)
	if err != nil {
		t.Fatalf("NewCompactionWorker() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start worker in goroutine (it will fail immediately on DB operations, but that's expected)
	startErr := make(chan error, 1)
	go func() {
		startErr <- worker.Start(ctx)
	}()

	// Wait for worker to enter running state
	require.NoError(t, await.New().AtMost(2*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		worker.mu.Lock()
		defer worker.mu.Unlock()
		return worker.running
	}), "worker should reach running state")

	// Cancel context
	cancel()

	// Worker should stop within a reasonable time
	select {
	case <-startErr:
		// Worker stopped as expected
	case <-time.After(2 * time.Second):
		t.Error("Worker did not stop after context cancellation")
	}
}

func TestCompactionWorker_StopSignal(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()
	config := CompactionConfig{
		RunInterval:       100 * time.Millisecond,
		FragmentThreshold: 100,
		BatchSize:         50,
	}

	worker, err := NewCompactionWorker(pool, config, logger)
	if err != nil {
		t.Fatalf("NewCompactionWorker() error = %v", err)
	}

	ctx := context.Background()

	// Start worker in goroutine
	startErr := make(chan error, 1)
	go func() {
		startErr <- worker.Start(ctx)
	}()

	// Wait for worker to enter running state
	require.NoError(t, await.New().AtMost(2*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		worker.mu.Lock()
		defer worker.mu.Unlock()
		return worker.running
	}), "worker should reach running state")

	// Call Stop
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.Stop()
	}()

	// Worker should stop within a reasonable time
	select {
	case <-startErr:
		// Worker stopped as expected
	case <-time.After(2 * time.Second):
		t.Error("Worker did not stop after Stop() was called")
	}

	wg.Wait()
}

func TestCompactionWorker_TryStartIteration(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := testLogger()
	config := validConfig()

	worker, err := NewCompactionWorker(pool, config, logger)
	if err != nil {
		t.Fatalf("NewCompactionWorker() error = %v", err)
	}

	// Before stopped, tryStartIteration should return true
	if !worker.tryStartIteration() {
		t.Error("tryStartIteration() = false, want true when not stopped")
	}
	worker.wg.Done() // Clean up the added wait group

	// After marking stopped, tryStartIteration should return false
	worker.mu.Lock()
	worker.stopped = true
	worker.mu.Unlock()

	if worker.tryStartIteration() {
		t.Error("tryStartIteration() = true, want false when stopped")
	}
}

func TestFragmentedBucket_Struct(t *testing.T) {
	bucket := FragmentedBucket{
		AccountID:      "ACC001",
		InstrumentCode: "USD",
		BucketKey:      "default",
		RowCount:       150,
	}

	if bucket.AccountID != "ACC001" {
		t.Errorf("bucket.AccountID = %s, want ACC001", bucket.AccountID)
	}
	if bucket.InstrumentCode != "USD" {
		t.Errorf("bucket.InstrumentCode = %s, want USD", bucket.InstrumentCode)
	}
	if bucket.BucketKey != "default" {
		t.Errorf("bucket.BucketKey = %s, want default", bucket.BucketKey)
	}
	if bucket.RowCount != 150 {
		t.Errorf("bucket.RowCount = %d, want 150", bucket.RowCount)
	}
}

func TestPositionRow_Struct(t *testing.T) {
	now := time.Now()
	row := PositionRow{
		Dimension:  "Monetary",
		Attributes: map[string]string{"key": "value"},
		CreatedAt:  now,
	}

	if row.Dimension != "Monetary" {
		t.Errorf("row.Dimension = %s, want Monetary", row.Dimension)
	}
	if row.Attributes["key"] != "value" {
		t.Errorf("row.Attributes[key] = %s, want value", row.Attributes["key"])
	}
	if !row.CreatedAt.Equal(now) {
		t.Errorf("row.CreatedAt = %v, want %v", row.CreatedAt, now)
	}
}

func TestCompactionConfig_Struct(t *testing.T) {
	config := CompactionConfig{
		RunInterval:       10 * time.Minute,
		FragmentThreshold: 200,
		BatchSize:         100,
	}

	if config.RunInterval != 10*time.Minute {
		t.Errorf("config.RunInterval = %v, want 10m", config.RunInterval)
	}
	if config.FragmentThreshold != 200 {
		t.Errorf("config.FragmentThreshold = %d, want 200", config.FragmentThreshold)
	}
	if config.BatchSize != 100 {
		t.Errorf("config.BatchSize = %d, want 100", config.BatchSize)
	}
}

func TestCompactionWorker_Errors(t *testing.T) {
	// Verify error variable definitions
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"ErrNilPool", ErrNilPool, "pool cannot be nil"},
		{"ErrNilLogger", ErrNilLogger, "logger cannot be nil"},
		{"ErrInvalidRunInterval", ErrInvalidRunInterval, "run interval must be greater than zero"},
		{"ErrInvalidFragmentThreshold", ErrInvalidFragmentThreshold, "fragment threshold must be greater than zero"},
		{"ErrInvalidBatchSize", ErrInvalidBatchSize, "batch size must be greater than zero"},
		{"ErrWorkerAlreadyRunning", ErrWorkerAlreadyRunning, "worker is already running"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.want {
				t.Errorf("%s.Error() = %s, want %s", tt.name, tt.err.Error(), tt.want)
			}
		})
	}
}

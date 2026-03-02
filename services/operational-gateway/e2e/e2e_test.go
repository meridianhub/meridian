package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/passthrough"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/persistence"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/meridianhub/meridian/services/operational-gateway/worker"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- Test infrastructure ---

var (
	sharedDB      *gorm.DB
	sharedOnce    sync.Once
	sharedInitErr error
	sharedCleanup func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedCleanup != nil {
		sharedCleanup()
	}
	os.Exit(code)
}

func initSharedContainer() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	connConfig, err := container.ConnectionConfig(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return fmt.Errorf("connection config: %w", err)
	}

	db, err := gorm.Open(gormpg.Open(connConfig.ConnString()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = container.Terminate(ctx)
		return fmt.Errorf("gorm open: %w", err)
	}

	if err := createSchema(db); err != nil {
		_ = container.Terminate(ctx)
		return fmt.Errorf("create schema: %w", err)
	}

	sharedDB = db
	sharedCleanup = func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = container.Terminate(cleanupCtx)
	}
	return nil
}

// createSchema applies the actual migration files from the migrations directory,
// ensuring the E2E test schema stays in sync with production.
func createSchema(db *gorm.DB) error {
	migrationsDir := "../migrations"
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(migrationsDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		// Execute each statement in the migration file.
		// Split on semicolons to handle multi-statement files.
		for _, stmt := range strings.Split(string(content), ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(stmt, "--") {
				continue
			}
			if err := db.Exec(stmt).Error; err != nil {
				return fmt.Errorf("migration %s failed: %w\nSQL: %s", entry.Name(), err, stmt)
			}
		}
	}
	return nil
}

func getSharedDB(t *testing.T) *gorm.DB {
	t.Helper()
	sharedOnce.Do(func() {
		sharedInitErr = initSharedContainer()
	})
	if sharedInitErr != nil {
		t.Fatalf("shared CockroachDB setup failed: %v", sharedInitErr)
	}
	return sharedDB
}

func cleanTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	tables := []string{"instruction_attempts", "instructions", "instruction_routes", "provider_connections"}
	for _, tbl := range tables {
		if err := db.Exec("DELETE FROM " + tbl).Error; err != nil {
			t.Fatalf("failed to clean table %s: %v", tbl, err)
		}
	}
}

// --- Test helpers ---

func testTenantID() uuid.UUID {
	return uuid.MustParse("11111111-1111-1111-1111-111111111111")
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// dbRouteResolver implements ports.RouteResolver by looking up routes from the RouteRepository.
type dbRouteResolver struct {
	routeRepo ports.RouteRepository
}

func (r *dbRouteResolver) Resolve(ctx context.Context, tenantID string, instructionType string) (*ports.InstructionRoute, error) {
	route, err := r.routeRepo.FindByInstructionType(ctx, tenantID, instructionType)
	if err != nil {
		return nil, err
	}
	return &ports.InstructionRoute{
		InstructionType: route.InstructionType,
		HTTPMethod:      route.HTTPMethod,
		PathTemplate:    route.PathTemplate,
		OutboundMapping: route.OutboundMapping,
		InboundMapping:  route.InboundMapping,
	}, nil
}

// httpDispatcherAdapter wraps an httptest.Server to implement ports.Dispatcher
// using a simple HTTP client with the passthrough transformer.
type httpDispatcherAdapter struct {
	client      *http.Client
	transformer ports.PayloadTransformer
}

func (d *httpDispatcherAdapter) Dispatch(ctx context.Context, instruction *domain.Instruction, conn *domain.ProviderConnection, route *ports.InstructionRoute) ports.DispatchResult {
	start := time.Now()

	body, _, err := d.transformer.TransformOutbound(ctx, instruction, route)
	if err != nil {
		return ports.DispatchResult{Duration: time.Since(start), Error: fmt.Errorf("outbound transform: %w", err)}
	}

	targetURL := conn.BaseURL + route.PathTemplate

	req, err := http.NewRequestWithContext(ctx, route.HTTPMethod, targetURL, bytes.NewReader(body))
	if err != nil {
		return ports.DispatchResult{Duration: time.Since(start), Error: fmt.Errorf("building request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return ports.DispatchResult{Duration: time.Since(start), Error: fmt.Errorf("executing request: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ports.DispatchResult{StatusCode: resp.StatusCode, Duration: time.Since(start), Error: fmt.Errorf("reading response: %w", err)}
	}

	outcome, err := d.transformer.TransformInbound(ctx, resp.StatusCode, respBody, route)
	if err != nil {
		return ports.DispatchResult{StatusCode: resp.StatusCode, ResponseBody: respBody, Duration: time.Since(start), Error: err}
	}

	return ports.DispatchResult{
		StatusCode:   resp.StatusCode,
		ResponseBody: respBody,
		Outcome:      outcome,
		Duration:     time.Since(start),
	}
}

// testHarness wires up all real components for E2E tests.
type testHarness struct {
	db              *gorm.DB
	instructionRepo *persistence.InstructionRepository
	connectionRepo  *persistence.ConnectionRepository
	routeRepo       *persistence.RouteRepository
	routeResolver   ports.RouteResolver
	dispatcher      ports.Dispatcher
	dispatchWorker  *worker.DispatchWorker
	expiryWorker    *worker.ExpiryWorker
	logger          *slog.Logger
	tenantID        uuid.UUID
}

func setupHarness(t *testing.T, mockServer *httptest.Server, dispatchConfig worker.DispatchWorkerConfig, expiryConfig worker.ExpiryWorkerConfig) *testHarness {
	t.Helper()

	db := getSharedDB(t)
	cleanTables(t, db)

	lg := testLogger()
	tenantID := testTenantID()

	instrRepo := persistence.NewInstructionRepository(db)
	connRepo := persistence.NewConnectionRepository(db)
	routeRepo := persistence.NewRouteRepository(db)
	resolver := &dbRouteResolver{routeRepo: routeRepo}

	transformer := passthrough.NewTransformer()
	disp := &httpDispatcherAdapter{
		client:      mockServer.Client(),
		transformer: transformer,
	}

	dispatchW := worker.NewDispatchWorker(instrRepo, connRepo, resolver, disp, dispatchConfig, lg)
	expiryW := worker.NewExpiryWorker(instrRepo, expiryConfig, lg)

	return &testHarness{
		db:              db,
		instructionRepo: instrRepo,
		connectionRepo:  connRepo,
		routeRepo:       routeRepo,
		routeResolver:   resolver,
		dispatcher:      disp,
		dispatchWorker:  dispatchW,
		expiryWorker:    expiryW,
		logger:          lg,
		tenantID:        tenantID,
	}
}

// seedConnection creates a provider connection pointing at the mock server.
func (h *testHarness) seedConnection(t *testing.T, baseURL string) *domain.ProviderConnection {
	t.Helper()

	conn, err := domain.NewProviderConnection(
		h.tenantID.String(),
		"test-provider",
		"bank",
		domain.ProtocolHTTPS,
		baseURL,
		&domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "test-key"},
		domain.RetryPolicy{
			MaxAttempts:       3,
			InitialBackoff:    10 * time.Millisecond,
			MaxBackoff:        100 * time.Millisecond,
			BackoffMultiplier: 2.0,
		},
		domain.RateLimitConfig{},
	)
	require.NoError(t, err)

	err = h.connectionRepo.Upsert(context.Background(), conn)
	require.NoError(t, err)

	return conn
}

// seedRoute creates an instruction route mapping a type to a connection.
func (h *testHarness) seedRoute(t *testing.T, instructionType, connectionID, httpMethod, pathTemplate string) *domain.Route {
	t.Helper()

	route, err := domain.NewRoute(h.tenantID.String(), instructionType, connectionID)
	require.NoError(t, err)
	route.HTTPMethod = httpMethod
	route.PathTemplate = pathTemplate

	err = h.routeRepo.Upsert(context.Background(), route)
	require.NoError(t, err)

	return route
}

// createInstruction creates an instruction in PENDING state and saves it.
// providerConnectionID should be the real connection ID from seedConnection.
func (h *testHarness) createInstruction(t *testing.T, instructionType string, providerConnectionID string, opts ...domain.InstructionOption) *domain.Instruction {
	t.Helper()

	instr, err := domain.NewInstruction(
		h.tenantID,
		instructionType,
		providerConnectionID,
		map[string]any{"amount": 100, "currency": "GBP"},
		opts...,
	)
	require.NoError(t, err)

	idempotencyKey := uuid.New().String()
	err = h.instructionRepo.Save(context.Background(), instr, idempotencyKey)
	require.NoError(t, err)

	return instr
}

// waitForStatus polls the database until the instruction reaches the expected status.
func (h *testHarness) waitForStatus(t *testing.T, instrID uuid.UUID, expectedStatus domain.InstructionStatus, timeout time.Duration) *domain.Instruction {
	t.Helper()

	var result *domain.Instruction
	err := await.New().AtMost(timeout).PollInterval(50 * time.Millisecond).Until(func() bool {
		instr, err := h.instructionRepo.FindByID(context.Background(), instrID)
		if err != nil {
			return false
		}
		result = instr
		return instr.Status == expectedStatus
	})
	require.NoError(t, err, "instruction %s did not reach status %s within %v (current: %s)",
		instrID, expectedStatus, timeout, statusOrUnknown(result))

	return result
}

func statusOrUnknown(instr *domain.Instruction) domain.InstructionStatus {
	if instr == nil {
		return "UNKNOWN"
	}
	return instr.Status
}

// --- E2E Tests ---

func TestInstructionLifecycle_HappyPath(t *testing.T) {
	// Mock provider: always returns 200
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	}))
	defer mockServer.Close()

	h := setupHarness(t, mockServer, worker.DispatchWorkerConfig{
		BatchSize:    10,
		PollInterval: 100 * time.Millisecond,
	}, worker.ExpiryWorkerConfig{})

	// Seed connection and route
	conn := h.seedConnection(t, mockServer.URL)
	h.seedRoute(t, "payment.create", conn.ConnectionID, "POST", "/v1/payments")

	// Create instruction
	instr := h.createInstruction(t, "payment.create", conn.ConnectionID)

	// Start dispatch worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.dispatchWorker.Start(ctx)
	defer h.dispatchWorker.Stop()

	// Wait for DELIVERED
	delivered := h.waitForStatus(t, instr.ID, domain.InstructionStatusDelivered, 10*time.Second)
	assert.Equal(t, domain.InstructionStatusDelivered, delivered.Status)
	assert.Equal(t, 1, delivered.AttemptCount)
	assert.NotNil(t, delivered.DispatchedAt)
}

func TestInstructionLifecycle_RetryOnTransientFailure(t *testing.T) {
	// Mock provider: returns 500 first 2 times, then 200
	var callCount atomic.Int32
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := callCount.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "temporary failure"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	}))
	defer mockServer.Close()

	h := setupHarness(t, mockServer, worker.DispatchWorkerConfig{
		BatchSize:    10,
		PollInterval: 100 * time.Millisecond,
	}, worker.ExpiryWorkerConfig{})

	conn := h.seedConnection(t, mockServer.URL)
	h.seedRoute(t, "payment.retry-test", conn.ConnectionID, "POST", "/v1/payments")

	// MaxAttempts=5 allows retries after transient failures
	instr := h.createInstruction(t, "payment.retry-test", conn.ConnectionID, domain.WithMaxAttempts(5))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.dispatchWorker.Start(ctx)
	defer h.dispatchWorker.Stop()

	// Wait for DELIVERED - the worker will retry after 500s
	delivered := h.waitForStatus(t, instr.ID, domain.InstructionStatusDelivered, 30*time.Second)
	assert.Equal(t, domain.InstructionStatusDelivered, delivered.Status)
	// Should have taken 3 attempts total (2 failures + 1 success)
	assert.Equal(t, 3, int(callCount.Load()))
}

func TestInstructionLifecycle_FailAfterMaxRetries(t *testing.T) {
	// Mock provider: always returns 500
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "permanent failure"}`))
	}))
	defer mockServer.Close()

	h := setupHarness(t, mockServer, worker.DispatchWorkerConfig{
		BatchSize:    10,
		PollInterval: 100 * time.Millisecond,
	}, worker.ExpiryWorkerConfig{})

	conn := h.seedConnection(t, mockServer.URL)
	h.seedRoute(t, "payment.fail-test", conn.ConnectionID, "POST", "/v1/payments")

	// MaxAttempts=2 means it will fail after 2 attempts
	instr := h.createInstruction(t, "payment.fail-test", conn.ConnectionID, domain.WithMaxAttempts(2))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.dispatchWorker.Start(ctx)
	defer h.dispatchWorker.Stop()

	// Wait for FAILED
	failed := h.waitForStatus(t, instr.ID, domain.InstructionStatusFailed, 30*time.Second)
	assert.Equal(t, domain.InstructionStatusFailed, failed.Status)
	assert.NotEmpty(t, failed.FailureReason)
	assert.NotNil(t, failed.CompletedAt)
}

func TestInstructionLifecycle_Expiry(t *testing.T) {
	// Mock provider: never called (instruction expires before dispatch)
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("provider should not be called for expired instructions")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	h := setupHarness(t, mockServer, worker.DispatchWorkerConfig{
		// Use a long poll interval so the dispatch worker is unlikely to pick up
		// the instruction before the expiry worker does.
		BatchSize:    10,
		PollInterval: 10 * time.Second,
	}, worker.ExpiryWorkerConfig{
		ScanInterval: 200 * time.Millisecond,
		BatchSize:    10,
	})

	conn := h.seedConnection(t, mockServer.URL)
	h.seedRoute(t, "payment.expiry-test", conn.ConnectionID, "POST", "/v1/payments")

	// Create instruction with expires_at in the past
	expiresAt := time.Now().Add(-1 * time.Second)
	instr := h.createInstruction(t, "payment.expiry-test", conn.ConnectionID, domain.WithExpiresAt(expiresAt))

	// Only start the expiry worker (not the dispatch worker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.expiryWorker.Start(ctx)
	defer h.expiryWorker.Stop()

	// Wait for EXPIRED
	expired := h.waitForStatus(t, instr.ID, domain.InstructionStatusExpired, 10*time.Second)
	assert.Equal(t, domain.InstructionStatusExpired, expired.Status)
	assert.NotNil(t, expired.CompletedAt)
}

func TestInstructionLifecycle_Cancellation(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("provider should not be called for cancelled instructions")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	h := setupHarness(t, mockServer, worker.DispatchWorkerConfig{
		// Long poll so dispatch doesn't pick it up before we cancel
		BatchSize:    10,
		PollInterval: 10 * time.Second,
	}, worker.ExpiryWorkerConfig{})

	conn := h.seedConnection(t, mockServer.URL)
	h.seedRoute(t, "payment.cancel-test", conn.ConnectionID, "POST", "/v1/payments")

	// Create instruction in PENDING
	instr := h.createInstruction(t, "payment.cancel-test", conn.ConnectionID)

	// Cancel the instruction before dispatch
	err := instr.Cancel()
	require.NoError(t, err)
	err = h.instructionRepo.Save(context.Background(), instr, "")
	require.NoError(t, err)

	// Verify CANCELLED status directly (no worker needed for cancellation)
	cancelled, err := h.instructionRepo.FindByID(context.Background(), instr.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.InstructionStatusCancelled, cancelled.Status)
	assert.NotNil(t, cancelled.CompletedAt)

	// Start dispatch worker and verify the cancelled instruction is NOT picked up
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.dispatchWorker.Start(ctx)

	// Give the worker a chance to run a few cycles
	err = await.New().AtMost(1 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return false // Just wait for timeout
	})
	// Expected: timeout because we're just waiting
	_ = err

	h.dispatchWorker.Stop()

	// Verify status is still CANCELLED
	stillCancelled, err := h.instructionRepo.FindByID(context.Background(), instr.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.InstructionStatusCancelled, stillCancelled.Status)
}

func TestConcurrentDispatch(t *testing.T) {
	// Mock provider: always returns 200, tracks call count
	var providerCalls atomic.Int32
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	}))
	defer mockServer.Close()

	h := setupHarness(t, mockServer, worker.DispatchWorkerConfig{
		BatchSize:    100,
		PollInterval: 100 * time.Millisecond,
	}, worker.ExpiryWorkerConfig{})

	conn := h.seedConnection(t, mockServer.URL)
	h.seedRoute(t, "payment.concurrent", conn.ConnectionID, "POST", "/v1/payments")

	// Create 50 instructions concurrently
	const numInstructions = 50
	instructionIDs := make([]uuid.UUID, numInstructions)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var createErrors []error

	for i := 0; i < numInstructions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			instr, err := domain.NewInstruction(
				h.tenantID,
				"payment.concurrent",
				conn.ConnectionID,
				map[string]any{"index": idx, "amount": 100},
			)
			if err != nil {
				mu.Lock()
				createErrors = append(createErrors, err)
				mu.Unlock()
				return
			}
			idempotencyKey := uuid.New().String()
			if err := h.instructionRepo.Save(context.Background(), instr, idempotencyKey); err != nil {
				mu.Lock()
				createErrors = append(createErrors, err)
				mu.Unlock()
				return
			}
			mu.Lock()
			instructionIDs[idx] = instr.ID
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	require.Empty(t, createErrors, "instruction creation errors: %v", createErrors)

	// Start dispatch worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.dispatchWorker.Start(ctx)
	defer h.dispatchWorker.Stop()

	// Wait for all instructions to reach DELIVERED
	err := await.New().AtMost(120 * time.Second).PollInterval(500 * time.Millisecond).Until(func() bool {
		deliveredCount := 0
		for _, id := range instructionIDs {
			if id == uuid.Nil {
				continue
			}
			instr, err := h.instructionRepo.FindByID(context.Background(), id)
			if err != nil {
				continue
			}
			if instr.Status == domain.InstructionStatusDelivered {
				deliveredCount++
			}
		}
		return deliveredCount == numInstructions
	})
	require.NoError(t, err, "not all instructions reached DELIVERED within timeout")

	// Verify all 100 were delivered
	for i, id := range instructionIDs {
		instr, err := h.instructionRepo.FindByID(context.Background(), id)
		require.NoError(t, err, "instruction %d", i)
		assert.Equal(t, domain.InstructionStatusDelivered, instr.Status, "instruction %d should be DELIVERED", i)
	}

	// Verify the mock provider was called exactly once per instruction (no double-dispatch).
	assert.Equal(t, numInstructions, int(providerCalls.Load()))
	// Verify each instruction was dispatched exactly once.
	for i, id := range instructionIDs {
		instr, err := h.instructionRepo.FindByID(context.Background(), id)
		require.NoError(t, err, "instruction %d", i)
		assert.Equal(t, 1, instr.AttemptCount, "instruction %d should have exactly 1 attempt", i)
	}
}

package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/meridianhub/meridian/shared/pkg/dispatch"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

type mockInstructionRepo struct {
	mu                sync.Mutex
	fetchDispatchable func(ctx context.Context, params ports.FetchDispatchableParams) ([]*domain.Instruction, error)
	save              func(ctx context.Context, instruction *domain.Instruction, idempotencyKey string) error
	savedInstructions []*domain.Instruction
	fetchCalls        int
	saveCalls         int
}

func (m *mockInstructionRepo) FetchDispatchable(ctx context.Context, params ports.FetchDispatchableParams) ([]*domain.Instruction, error) {
	m.mu.Lock()
	m.fetchCalls++
	m.mu.Unlock()
	if m.fetchDispatchable != nil {
		return m.fetchDispatchable(ctx, params)
	}
	return nil, nil
}

func (m *mockInstructionRepo) Save(ctx context.Context, instruction *domain.Instruction, idempotencyKey string) error {
	m.mu.Lock()
	m.saveCalls++
	m.savedInstructions = append(m.savedInstructions, instruction)
	m.mu.Unlock()
	if m.save != nil {
		return m.save(ctx, instruction, idempotencyKey)
	}
	return nil
}

func (m *mockInstructionRepo) FindByID(_ context.Context, _ uuid.UUID) (*domain.Instruction, error) {
	return nil, ports.ErrInstructionNotFound
}

func (m *mockInstructionRepo) ListByTenant(_ context.Context, _ ports.ListInstructionsParams) ([]*domain.Instruction, int64, error) {
	return nil, 0, nil
}

func (m *mockInstructionRepo) FindExpired(_ context.Context, _ int) ([]*domain.Instruction, error) {
	return nil, nil
}

func (m *mockInstructionRepo) getSavedInstructions() []*domain.Instruction {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*domain.Instruction, len(m.savedInstructions))
	copy(cp, m.savedInstructions)
	return cp
}

func (m *mockInstructionRepo) getSaveCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveCalls
}

func (m *mockInstructionRepo) getFetchCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fetchCalls
}

type mockConnectionRepo struct {
	findByID     func(ctx context.Context, tenantID string, connectionID string) (*domain.ProviderConnection, error)
	updateHealth func(ctx context.Context, conn *domain.ProviderConnection) error
}

func (m *mockConnectionRepo) FindByID(ctx context.Context, tenantID string, connectionID string) (*domain.ProviderConnection, error) {
	if m.findByID != nil {
		return m.findByID(ctx, tenantID, connectionID)
	}
	return nil, ports.ErrConnectionNotFound
}

func (m *mockConnectionRepo) UpdateHealth(ctx context.Context, conn *domain.ProviderConnection) error {
	if m.updateHealth != nil {
		return m.updateHealth(ctx, conn)
	}
	return nil
}

func (m *mockConnectionRepo) Upsert(_ context.Context, _ *domain.ProviderConnection) error {
	return nil
}

func (m *mockConnectionRepo) ListByTenant(_ context.Context, _ string) ([]*domain.ProviderConnection, error) {
	return nil, nil
}

type mockRouteResolver struct {
	resolve func(ctx context.Context, tenantID string, instructionType string) (*ports.InstructionRoute, error)
}

func (m *mockRouteResolver) Resolve(ctx context.Context, tenantID string, instructionType string) (*ports.InstructionRoute, error) {
	if m.resolve != nil {
		return m.resolve(ctx, tenantID, instructionType)
	}
	return nil, ports.ErrRouteNotFound
}

type mockDispatcher struct {
	dispatch func(ctx context.Context, instruction *domain.Instruction, conn *domain.ProviderConnection, route *ports.InstructionRoute) ports.DispatchResult
}

func (m *mockDispatcher) Dispatch(ctx context.Context, instruction *domain.Instruction, conn *domain.ProviderConnection, route *ports.InstructionRoute) ports.DispatchResult {
	if m.dispatch != nil {
		return m.dispatch(ctx, instruction, conn, route)
	}
	return ports.DispatchResult{Error: errors.New("not implemented")}
}

// --- Test helpers ---

func testTenantID() uuid.UUID {
	return uuid.MustParse("11111111-1111-1111-1111-111111111111")
}

func testConnection() *domain.ProviderConnection {
	return &domain.ProviderConnection{
		TenantID:     testTenantID().String(),
		ConnectionID: "conn-123",
		ProviderName: "test-provider",
		Protocol:     domain.ProtocolHTTPS,
		BaseURL:      "https://provider.example.com",
		AuthConfig:   &domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "test-key"},
		RetryPolicy: domain.RetryPolicy{
			MaxAttempts:       3,
			InitialBackoff:    1 * time.Second,
			MaxBackoff:        30 * time.Second,
			BackoffMultiplier: 2.0,
		},
		CircuitState: domain.CircuitStateClosed,
		HealthStatus: domain.HealthStatusHealthy,
	}
}

func testRoute() *ports.InstructionRoute {
	return &ports.InstructionRoute{
		InstructionType: "payment_order.create",
		HTTPMethod:      "POST",
		PathTemplate:    "/v1/payments",
	}
}

// dispatchingInstruction creates an instruction already in DISPATCHING state
// (as returned by FetchDispatchable).
func dispatchingInstruction() *domain.Instruction {
	now := time.Now()
	return &domain.Instruction{
		ID:                   uuid.New(),
		TenantID:             testTenantID(),
		InstructionType:      "payment_order.create",
		ProviderConnectionID: "conn-123",
		Payload:              map[string]any{"amount": 100},
		Priority:             domain.PriorityNormal,
		Status:               domain.InstructionStatusDispatching,
		MaxAttempts:          3,
		AttemptCount:         1,
		Attempts:             []domain.InstructionAttempt{},
		DispatchedAt:         &now,
		Version:              1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

// --- Tests ---

func TestNewDispatchWorker_AppliesDefaults(t *testing.T) {
	w := NewDispatchWorker(
		&mockInstructionRepo{},
		&mockConnectionRepo{},
		&mockRouteResolver{},
		&mockDispatcher{},
		DispatchWorkerConfig{},
		nil,
	)

	assert.Equal(t, defaultBatchSize, w.config.BatchSize)
	assert.Equal(t, defaultPollInterval, w.config.PollInterval)
	assert.NotNil(t, w.logger)
}

func TestNewDispatchWorker_RespectsCustomConfig(t *testing.T) {
	w := NewDispatchWorker(
		&mockInstructionRepo{},
		&mockConnectionRepo{},
		&mockRouteResolver{},
		&mockDispatcher{},
		DispatchWorkerConfig{BatchSize: 10, PollInterval: 500 * time.Millisecond},
		nil,
	)

	assert.Equal(t, 10, w.config.BatchSize)
	assert.Equal(t, 500*time.Millisecond, w.config.PollInterval)
}

func TestDispatchWorker_StartAndStop(t *testing.T) {
	repo := &mockInstructionRepo{}
	w := NewDispatchWorker(
		repo,
		&mockConnectionRepo{},
		&mockRouteResolver{},
		&mockDispatcher{},
		DispatchWorkerConfig{PollInterval: 50 * time.Millisecond},
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	// Wait for at least one fetch cycle.
	err := await.AtMost(2 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return repo.getFetchCalls() >= 1
	})
	require.NoError(t, err)

	w.Stop()

	// Verify that Stop is idempotent.
	w.Stop()
}

func TestDispatchWorker_StopsOnContextCancel(t *testing.T) {
	repo := &mockInstructionRepo{}
	w := NewDispatchWorker(
		repo,
		&mockConnectionRepo{},
		&mockRouteResolver{},
		&mockDispatcher{},
		DispatchWorkerConfig{PollInterval: 50 * time.Millisecond},
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	// Wait for at least one fetch cycle.
	err := await.AtMost(2 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return repo.getFetchCalls() >= 1
	})
	require.NoError(t, err)

	cancel()

	// Worker should stop after context cancel. Wait via wg.
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestProcessInstruction_HappyPath_Delivered(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()
	route := testRoute()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return route, nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				StatusCode: 200,
				Outcome: &ports.InstructionOutcome{
					ExternalID:     "ext-123",
					ProviderStatus: "ACCEPTED",
				},
				Duration: 50 * time.Millisecond,
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusDelivered, saved[0].Status)
}

func TestProcessInstruction_RouteNotFound_MarksFailed(t *testing.T) {
	instr := dispatchingInstruction()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return nil, ports.ErrRouteNotFound
		},
	}
	dispatcher := &mockDispatcher{}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusFailed, saved[0].Status)
	assert.Contains(t, saved[0].FailureReason, "route resolution failed")
	assert.Equal(t, "ROUTE_NOT_FOUND", saved[0].ErrorCode)
}

func TestProcessInstruction_ConnectionNotFound_MarksFailed(t *testing.T) {
	instr := dispatchingInstruction()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return nil, ports.ErrConnectionNotFound
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusFailed, saved[0].Status)
	assert.Equal(t, "CONNECTION_NOT_FOUND", saved[0].ErrorCode)
}

func TestProcessInstruction_RouteTransientError_ReturnsError(t *testing.T) {
	instr := dispatchingInstruction()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return nil, errors.New("database connection lost")
		},
	}
	dispatcher := &mockDispatcher{}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "route resolution transient error")

	// Instruction should NOT be marked failed — stays DISPATCHING for reaper.
	saved := instrRepo.getSavedInstructions()
	assert.Len(t, saved, 0)
}

func TestProcessInstruction_ConnectionTransientError_ReturnsError(t *testing.T) {
	instr := dispatchingInstruction()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return nil, errors.New("network timeout")
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection lookup transient error")

	// Instruction should NOT be marked failed — stays DISPATCHING for reaper.
	saved := instrRepo.getSavedInstructions()
	assert.Len(t, saved, 0)
}

func TestProcessInstruction_CircuitOpen_RetriesWhenPossible(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()
	conn.CircuitState = domain.CircuitStateOpen

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusRetrying, saved[0].Status)
	require.Len(t, saved[0].Attempts, 1)
	assert.Equal(t, "CIRCUIT_OPEN", saved[0].Attempts[0].ErrorCode)
	assert.NotNil(t, saved[0].NextRetryAt)
}

func TestProcessInstruction_CircuitOpen_FailsWhenRetriesExhausted(t *testing.T) {
	instr := dispatchingInstruction()
	instr.AttemptCount = 3 // Matches MaxAttempts, so CanRetry() returns false
	conn := testConnection()
	conn.CircuitState = domain.CircuitStateOpen

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusFailed, saved[0].Status)
}

func TestProcessInstruction_DispatchError_RetriesWithBackoff(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				Error:    errors.New("connection refused"),
				Duration: 100 * time.Millisecond,
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusRetrying, saved[0].Status)
	assert.NotNil(t, saved[0].NextRetryAt)
	assert.Contains(t, saved[0].Attempts[0].FailureReason, "dispatch error")
}

func TestProcessInstruction_ProviderRetry_SchedulesRetry(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				StatusCode:   429,
				ResponseBody: []byte(`{"error":"rate limited"}`),
				Outcome: &ports.InstructionOutcome{
					ShouldRetry:   true,
					FailureReason: "rate limited",
				},
				Duration: 20 * time.Millisecond,
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusRetrying, saved[0].Status)
	assert.NotNil(t, saved[0].NextRetryAt)
}

func TestProcessInstruction_ProviderReject_MarksFailed(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				StatusCode:   400,
				ResponseBody: []byte(`{"error":"bad request"}`),
				Outcome: &ports.InstructionOutcome{
					FailureReason:  "invalid payload",
					ProviderStatus: "REJECTED",
				},
				Duration: 20 * time.Millisecond,
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusFailed, saved[0].Status)
	assert.Equal(t, "invalid payload", saved[0].FailureReason)
	assert.Equal(t, "PROVIDER_REJECTED", saved[0].ErrorCode)
}

func TestProcessInstruction_NilOutcome_MarksFailed(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				StatusCode: 200,
				Duration:   20 * time.Millisecond,
				// Outcome is nil
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusFailed, saved[0].Status)
	assert.Equal(t, "NO_OUTCOME", saved[0].ErrorCode)
}

func TestProcessInstruction_DispatchError_RecordsFailureOnConnection(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()

	var healthUpdated bool
	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
		updateHealth: func(_ context.Context, c *domain.ProviderConnection) error {
			healthUpdated = true
			assert.Equal(t, 1, c.FailureCount)
			return nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{Error: errors.New("timeout"), Duration: 30 * time.Second}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)
	assert.True(t, healthUpdated, "connection health should have been updated")
}

func TestProcessInstruction_SuccessfulDispatch_RecordsSuccessOnConnection(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()
	conn.FailureCount = 2 // Existing failures should be reset on success

	var healthUpdated bool
	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
		updateHealth: func(_ context.Context, c *domain.ProviderConnection) error {
			healthUpdated = true
			assert.Equal(t, 0, c.FailureCount)
			assert.Equal(t, 1, c.SuccessCount)
			return nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				StatusCode: 200,
				Outcome:    &ports.InstructionOutcome{ExternalID: "ext-1", ProviderStatus: "ACCEPTED"},
				Duration:   10 * time.Millisecond,
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)
	assert.True(t, healthUpdated)
}

func TestProcessBatch_ProcessesMultipleInstructions(t *testing.T) {
	instr1 := dispatchingInstruction()
	instr2 := dispatchingInstruction()
	conn := testConnection()

	instrRepo := &mockInstructionRepo{
		fetchDispatchable: func(_ context.Context, _ ports.FetchDispatchableParams) ([]*domain.Instruction, error) {
			return []*domain.Instruction{instr1, instr2}, nil
		},
	}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				StatusCode: 200,
				Outcome:    &ports.InstructionOutcome{ExternalID: "ext-1", ProviderStatus: "ACCEPTED"},
				Duration:   10 * time.Millisecond,
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)
	w.processBatch(context.Background())

	saved := instrRepo.getSavedInstructions()
	assert.Len(t, saved, 2)
	for _, s := range saved {
		assert.Equal(t, domain.InstructionStatusDelivered, s.Status)
	}
}

func TestProcessBatch_FetchError_DoesNotPanic(t *testing.T) {
	instrRepo := &mockInstructionRepo{
		fetchDispatchable: func(_ context.Context, _ ports.FetchDispatchableParams) ([]*domain.Instruction, error) {
			return nil, errors.New("db connection lost")
		},
	}

	w := NewDispatchWorker(instrRepo, &mockConnectionRepo{}, &mockRouteResolver{}, &mockDispatcher{}, DispatchWorkerConfig{}, nil)
	// Should not panic.
	w.processBatch(context.Background())
	assert.Equal(t, 0, instrRepo.getSaveCalls())
}

func TestProcessBatch_EmptyBatch_NoOp(t *testing.T) {
	instrRepo := &mockInstructionRepo{
		fetchDispatchable: func(_ context.Context, _ ports.FetchDispatchableParams) ([]*domain.Instruction, error) {
			return nil, nil
		},
	}

	w := NewDispatchWorker(instrRepo, &mockConnectionRepo{}, &mockRouteResolver{}, &mockDispatcher{}, DispatchWorkerConfig{}, nil)
	w.processBatch(context.Background())
	assert.Equal(t, 0, instrRepo.getSaveCalls())
}

func TestCalculateNextRetry_ExponentialBackoff(t *testing.T) {
	policy := domain.RetryPolicy{
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        1 * time.Minute,
		BackoffMultiplier: 2.0,
	}

	before := time.Now()

	// First retry (attempt 1): 1s * 2^0 = 1s
	retry1 := dispatch.CalculateNextRetry(1, policy)
	assert.InDelta(t, 1*time.Second, retry1.Sub(before), float64(200*time.Millisecond))

	// Second retry (attempt 2): 1s * 2^1 = 2s
	retry2 := dispatch.CalculateNextRetry(2, policy)
	assert.InDelta(t, 2*time.Second, retry2.Sub(before), float64(200*time.Millisecond))

	// Third retry (attempt 3): 1s * 2^2 = 4s
	retry3 := dispatch.CalculateNextRetry(3, policy)
	assert.InDelta(t, 4*time.Second, retry3.Sub(before), float64(200*time.Millisecond))
}

func TestCalculateNextRetry_CapsAtMaxBackoff(t *testing.T) {
	policy := domain.RetryPolicy{
		InitialBackoff:    10 * time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 10.0,
	}

	before := time.Now()

	// attempt 3: 10s * 10^2 = 1000s, but capped at 30s
	retry := dispatch.CalculateNextRetry(3, policy)
	assert.InDelta(t, 30*time.Second, retry.Sub(before), float64(200*time.Millisecond))
}

func TestCalculateNextRetry_FallbackDefaults(t *testing.T) {
	policy := domain.RetryPolicy{} // All zero values

	before := time.Now()

	// Should use defaults: 1s initial, 2.0 multiplier, 5m max
	retry := dispatch.CalculateNextRetry(1, policy)
	assert.InDelta(t, 1*time.Second, retry.Sub(before), float64(200*time.Millisecond))
}

func TestDispatchWorker_PassesBatchSizeToFetch(t *testing.T) {
	var capturedLimit int
	instrRepo := &mockInstructionRepo{
		fetchDispatchable: func(_ context.Context, params ports.FetchDispatchableParams) ([]*domain.Instruction, error) {
			capturedLimit = params.Limit
			return nil, nil
		},
	}

	w := NewDispatchWorker(instrRepo, &mockConnectionRepo{}, &mockRouteResolver{}, &mockDispatcher{},
		DispatchWorkerConfig{BatchSize: 25, PollInterval: 50 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	err := await.AtMost(2 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return instrRepo.getFetchCalls() >= 1
	})
	require.NoError(t, err)

	w.Stop()
	assert.Equal(t, 25, capturedLimit)
}

func TestProcessInstruction_HalfOpenCircuit_AllowsDispatch(t *testing.T) {
	instr := dispatchingInstruction()
	conn := testConnection()
	conn.CircuitState = domain.CircuitStateHalfOpen // IsAvailable() returns true

	instrRepo := &mockInstructionRepo{}
	connRepo := &mockConnectionRepo{
		findByID: func(_ context.Context, _, _ string) (*domain.ProviderConnection, error) {
			return conn, nil
		},
	}
	resolver := &mockRouteResolver{
		resolve: func(_ context.Context, _, _ string) (*ports.InstructionRoute, error) {
			return testRoute(), nil
		},
	}
	dispatcher := &mockDispatcher{
		dispatch: func(_ context.Context, _ *domain.Instruction, _ *domain.ProviderConnection, _ *ports.InstructionRoute) ports.DispatchResult {
			return ports.DispatchResult{
				StatusCode: 200,
				Outcome:    &ports.InstructionOutcome{ExternalID: "ext-1", ProviderStatus: "ACCEPTED"},
				Duration:   10 * time.Millisecond,
			}
		},
	}

	w := NewDispatchWorker(instrRepo, connRepo, resolver, dispatcher, DispatchWorkerConfig{}, nil)

	err := w.processInstruction(context.Background(), instr)
	require.NoError(t, err)

	saved := instrRepo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusDelivered, saved[0].Status)
	// Half-open -> closed on success
	assert.Equal(t, domain.CircuitStateClosed, conn.CircuitState)
}

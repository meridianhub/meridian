package persistence

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertTestConnection inserts a minimal provider connection to satisfy FK constraints.
func insertTestConnection(t *testing.T, db *gorm.DB, tenantID, connectionID uuid.UUID) {
	t.Helper()
	require.NoError(t, db.Exec(`
        INSERT INTO provider_connections
            (tenant_id, connection_id, provider_name, provider_type, protocol, base_url, auth_config)
        VALUES (?, ?, 'test-provider', 'bank', 'HTTPS', 'https://example.com', '{"auth_type":"api_key","header_name":"X-API-Key","secret_ref":"ref1"}')
    `, tenantID, connectionID).Error)
}

// makeInstruction creates a domain instruction fixture.
func makeInstruction(t *testing.T, tenantID uuid.UUID, connID string, priority domain.Priority) *domain.Instruction {
	t.Helper()
	inst, err := domain.NewInstruction(
		tenantID,
		"payment.initiate",
		connID,
		map[string]any{"amount": "100.00"},
		domain.WithPriority(priority),
	)
	require.NoError(t, err)
	return inst
}

// TestInstructionRepository_Save_Create verifies new instructions can be inserted.
func TestInstructionRepository_Save_Create(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)
	inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)

	err := repo.Save(ctx, inst, fmt.Sprintf("idem-%s", inst.ID))
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, inst.ID)
	require.NoError(t, err)
	assert.Equal(t, inst.ID, found.ID)
	assert.Equal(t, domain.InstructionStatusPending, found.Status)
	assert.Equal(t, domain.PriorityNormal, found.Priority)
}

// TestInstructionRepository_Save_Update_OptimisticLock verifies that concurrent updates
// on the same instruction return ErrInstructionConflict when versions conflict.
func TestInstructionRepository_Save_Update_OptimisticLock(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)
	inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityHigh)
	idemKey := fmt.Sprintf("idem-%s", inst.ID)

	// Create - after this inst.Version is still 0 (we only track it on load).
	require.NoError(t, repo.Save(ctx, inst, idemKey))

	// Load the saved instruction so inst.Version is populated from DB (version=1).
	loaded, err := repo.FindByID(ctx, inst.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), loaded.Version)

	// Simulate two concurrent readers loading the same version=1 copy.
	writerA := *loaded // copy
	writerB := *loaded // stale copy

	// Writer A succeeds first: marks dispatching and saves → DB version becomes 2.
	require.NoError(t, writerA.MarkDispatching())
	require.NoError(t, repo.Save(ctx, &writerA, idemKey))

	// Writer B (stale copy at version=1) tries to save → version=1 no longer matches DB → conflict.
	require.NoError(t, writerB.MarkDispatching())
	err = repo.Save(ctx, &writerB, idemKey)
	require.ErrorIs(t, err, ports.ErrInstructionConflict)
}

// TestInstructionRepository_Save_DuplicateIdempotency verifies idempotency key uniqueness.
func TestInstructionRepository_Save_DuplicateIdempotency(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	inst1 := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, inst1, "duplicate-key"))

	inst2 := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	err := repo.Save(ctx, inst2, "duplicate-key") // same idempotency key, different tenant scope
	require.ErrorIs(t, err, ports.ErrDuplicateIdempotency)
}

// TestInstructionRepository_FindByID_NotFound verifies correct error on missing record.
func TestInstructionRepository_FindByID_NotFound(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewInstructionRepository(db)
	_, err := repo.FindByID(ctx, uuid.New())
	require.ErrorIs(t, err, ports.ErrInstructionNotFound)
}

// TestInstructionRepository_FetchDispatchable_PriorityOrdering verifies that CRITICAL
// instructions are returned before NORMAL ones.
func TestInstructionRepository_FetchDispatchable_PriorityOrdering(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	normal := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	critical := makeInstruction(t, tenantID, connID.String(), domain.PriorityCritical)
	low := makeInstruction(t, tenantID, connID.String(), domain.PriorityLow)

	require.NoError(t, repo.Save(ctx, normal, fmt.Sprintf("idem-%s", normal.ID)))
	require.NoError(t, repo.Save(ctx, critical, fmt.Sprintf("idem-%s", critical.ID)))
	require.NoError(t, repo.Save(ctx, low, fmt.Sprintf("idem-%s", low.ID)))

	results, err := repo.FetchDispatchable(ctx, ports.FetchDispatchableParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, results, 3)

	// CRITICAL=4 should come first, LOW=1 last
	assert.Equal(t, domain.PriorityCritical, results[0].Priority)
	assert.Equal(t, domain.PriorityNormal, results[1].Priority)
	assert.Equal(t, domain.PriorityLow, results[2].Priority)
}

// TestInstructionRepository_FetchDispatchable_SkipsNonPendingRetrying verifies that only
// PENDING and RETRYING instructions are returned (DISPATCHING is excluded).
func TestInstructionRepository_FetchDispatchable_SkipsNonPendingRetrying(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	pending := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, pending, fmt.Sprintf("idem-%s", pending.ID)))

	dispatching := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, dispatching, fmt.Sprintf("idem-%s", dispatching.ID)))
	// Reload to get version populated before updating
	dispatchingLoaded, err := repo.FindByID(ctx, dispatching.ID)
	require.NoError(t, err)
	require.NoError(t, dispatchingLoaded.MarkDispatching())
	require.NoError(t, repo.Save(ctx, dispatchingLoaded, fmt.Sprintf("idem-%s", dispatching.ID)))

	results, err := repo.FetchDispatchable(ctx, ports.FetchDispatchableParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, pending.ID, results[0].ID)
}

// TestInstructionRepository_FetchDispatchable_RespectsScheduledAt verifies that
// instructions scheduled in the future are not fetched.
func TestInstructionRepository_FetchDispatchable_RespectsScheduledAt(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	future := time.Now().Add(10 * time.Minute)
	scheduledFuture, err := domain.NewInstruction(
		tenantID, "payment.initiate", connID.String(),
		map[string]any{"amount": "50.00"},
		domain.WithScheduledAt(future),
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, scheduledFuture, fmt.Sprintf("idem-%s", scheduledFuture.ID)))

	ready := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, ready, fmt.Sprintf("idem-%s", ready.ID)))

	results, err := repo.FetchDispatchable(ctx, ports.FetchDispatchableParams{
		Limit: 10,
		AsOf:  time.Now(),
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, ready.ID, results[0].ID)
}

// TestInstructionRepository_FetchDispatchable_LimitEnforced verifies the Limit parameter.
func TestInstructionRepository_FetchDispatchable_LimitEnforced(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	for i := 0; i < 5; i++ {
		inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
		require.NoError(t, repo.Save(ctx, inst, fmt.Sprintf("idem-%d-%s", i, inst.ID)))
	}

	results, err := repo.FetchDispatchable(ctx, ports.FetchDispatchableParams{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

// TestInstructionRepository_RoundTrip_Metadata verifies metadata is persisted and restored.
func TestInstructionRepository_RoundTrip_Metadata(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	inst, err := domain.NewInstruction(
		tenantID,
		"payment.initiate",
		connID.String(),
		map[string]any{"amount": "200.00"},
		domain.WithMetadata(map[string]string{
			"source":  "saga-123",
			"account": "acc-456",
		}),
		domain.WithCorrelationID("corr-abc"),
		domain.WithCausationID("cause-xyz"),
	)
	require.NoError(t, err)

	require.NoError(t, repo.Save(ctx, inst, fmt.Sprintf("idem-%s", inst.ID)))

	found, err := repo.FindByID(ctx, inst.ID)
	require.NoError(t, err)
	assert.Equal(t, "corr-abc", found.CorrelationID)
	assert.Equal(t, "cause-xyz", found.CausationID)
	assert.Equal(t, "saga-123", found.Metadata["source"])
	assert.Equal(t, "acc-456", found.Metadata["account"])
}

// TestInstructionRepository_RoundTrip_AllPriorities verifies priority mapping is bidirectional.
func TestInstructionRepository_RoundTrip_AllPriorities(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	cases := []domain.Priority{
		domain.PriorityLow,
		domain.PriorityNormal,
		domain.PriorityHigh,
		domain.PriorityCritical,
	}

	for _, p := range cases {
		inst := makeInstruction(t, tenantID, connID.String(), p)
		require.NoError(t, repo.Save(ctx, inst, fmt.Sprintf("idem-%s-%s", p, inst.ID)))

		found, err := repo.FindByID(ctx, inst.ID)
		require.NoError(t, err)
		assert.Equal(t, p, found.Priority, "priority %q did not round-trip", p)
	}
}

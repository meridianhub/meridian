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

// makeTypedInstruction builds a domain instruction with an explicit instruction type,
// allowing ListByTenant type-filter tests to exercise distinct values.
func makeTypedInstruction(t *testing.T, tenantID uuid.UUID, connID, instructionType string, priority domain.Priority) *domain.Instruction {
	t.Helper()
	inst, err := domain.NewInstruction(
		tenantID,
		instructionType,
		connID,
		map[string]any{"amount": "100.00"},
		domain.WithPriority(priority),
	)
	require.NoError(t, err)
	return inst
}

// TestInstructionRepository_WithTx_SaveInTx verifies that WithTx returns a repository
// bound to the supplied transaction and that SaveInTx persists within that transaction,
// committing atomically when the transaction succeeds.
func TestInstructionRepository_WithTx_SaveInTx(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)
	inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	idemKey := fmt.Sprintf("idem-%s", inst.ID)

	// Drive the insert through a caller-managed transaction using WithTx + SaveInTx.
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := repo.WithTx(tx)
		return txRepo.SaveInTx(ctx, tx, inst, idemKey)
	})
	require.NoError(t, err)

	// Version is propagated back to the caller on insert.
	assert.Equal(t, int64(1), inst.Version)

	// The committed row is visible via a fresh (non-tx) read.
	found, err := repo.FindByID(ctx, inst.ID)
	require.NoError(t, err)
	assert.Equal(t, inst.ID, found.ID)
	assert.Equal(t, domain.InstructionStatusPending, found.Status)
}

// TestInstructionRepository_SaveInTx_Update verifies SaveInTx handles the existing-row
// UPDATE path (optimistic-lock increment) within a caller-managed transaction.
func TestInstructionRepository_SaveInTx_Update(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)
	inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	idemKey := fmt.Sprintf("idem-%s", inst.ID)

	require.NoError(t, repo.Save(ctx, inst, idemKey))
	require.Equal(t, int64(1), inst.Version)

	// Transition state and persist the update through SaveInTx.
	require.NoError(t, inst.MarkDispatching())
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return repo.WithTx(tx).SaveInTx(ctx, tx, inst, idemKey)
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), inst.Version)

	found, err := repo.FindByID(ctx, inst.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.InstructionStatusDispatching, found.Status)
	assert.Equal(t, int64(2), found.Version)
}

// TestInstructionRepository_SaveInTx_RollbackOnError verifies that when the caller's
// transaction returns an error after SaveInTx, the insert is rolled back (not persisted).
func TestInstructionRepository_SaveInTx_RollbackOnError(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)
	inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	idemKey := fmt.Sprintf("idem-%s", inst.ID)

	sentinel := fmt.Errorf("caller-aborted")
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if saveErr := repo.WithTx(tx).SaveInTx(ctx, tx, inst, idemKey); saveErr != nil {
			return saveErr
		}
		return sentinel // force rollback
	})
	require.ErrorIs(t, err, sentinel)

	// The instruction must not exist - the transaction was rolled back.
	_, findErr := repo.FindByID(ctx, inst.ID)
	require.ErrorIs(t, findErr, ports.ErrInstructionNotFound)
}

// TestInstructionRepository_ListByTenant_TenantScoping verifies ListByTenant returns only
// instructions for the requested tenant and excludes other tenants' rows.
func TestInstructionRepository_ListByTenant_TenantScoping(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantA := uuid.New()
	tenantB := uuid.New()
	connA := uuid.New()
	connB := uuid.New()
	insertTestConnection(t, db, tenantA, connA)
	insertTestConnection(t, db, tenantB, connB)

	repo := NewInstructionRepository(db)

	for i := 0; i < 2; i++ {
		inst := makeInstruction(t, tenantA, connA.String(), domain.PriorityNormal)
		require.NoError(t, repo.Save(ctx, inst, fmt.Sprintf("a-%d-%s", i, inst.ID)))
	}
	instB := makeInstruction(t, tenantB, connB.String(), domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, instB, fmt.Sprintf("b-%s", instB.ID)))

	results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
		TenantID: tenantA.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, tenantA, r.TenantID)
	}
}

// TestInstructionRepository_ListByTenant_Filters exercises every optional filter on
// ListByTenant: instruction type, provider connection, status set, and the created-at window.
func TestInstructionRepository_ListByTenant_Filters(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	otherConn := uuid.New()
	insertTestConnection(t, db, tenantID, connID)
	insertTestConnection(t, db, tenantID, otherConn)

	repo := NewInstructionRepository(db)

	// payment.initiate on connID, left PENDING.
	payment := makeTypedInstruction(t, tenantID, connID.String(), "payment.initiate", domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, payment, fmt.Sprintf("pay-%s", payment.ID)))

	// refund.initiate on connID, transitioned to DISPATCHING.
	refund := makeTypedInstruction(t, tenantID, connID.String(), "refund.initiate", domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, refund, fmt.Sprintf("ref-%s", refund.ID)))
	require.NoError(t, refund.MarkDispatching())
	require.NoError(t, repo.Save(ctx, refund, fmt.Sprintf("ref-%s", refund.ID)))

	// payment.initiate on otherConn, left PENDING.
	otherConnInst := makeTypedInstruction(t, tenantID, otherConn.String(), "payment.initiate", domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, otherConnInst, fmt.Sprintf("oth-%s", otherConnInst.ID)))

	t.Run("instruction type filter", func(t *testing.T) {
		results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
			TenantID:        tenantID.String(),
			InstructionType: "refund.initiate",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, results, 1)
		assert.Equal(t, refund.ID, results[0].ID)
	})

	t.Run("provider connection filter", func(t *testing.T) {
		results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
			TenantID:             tenantID.String(),
			ProviderConnectionID: otherConn.String(),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, results, 1)
		assert.Equal(t, otherConnInst.ID, results[0].ID)
	})

	t.Run("status filter", func(t *testing.T) {
		results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
			TenantID: tenantID.String(),
			Statuses: []domain.InstructionStatus{domain.InstructionStatusDispatching},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, results, 1)
		assert.Equal(t, refund.ID, results[0].ID)
	})

	t.Run("multiple statuses", func(t *testing.T) {
		results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
			TenantID: tenantID.String(),
			Statuses: []domain.InstructionStatus{
				domain.InstructionStatusPending,
				domain.InstructionStatusDispatching,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(3), total)
		assert.Len(t, results, 3)
	})

	t.Run("created-at window", func(t *testing.T) {
		// All three rows were created moments ago; a wide window captures all,
		// a future lower bound captures none.
		now := time.Now()
		results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
			TenantID:      tenantID.String(),
			CreatedAfter:  now.Add(-1 * time.Hour),
			CreatedBefore: now.Add(1 * time.Hour),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(3), total)
		assert.Len(t, results, 3)

		none, noneTotal, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
			TenantID:     tenantID.String(),
			CreatedAfter: now.Add(1 * time.Hour),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(0), noneTotal)
		assert.Empty(t, none)
	})
}

// TestInstructionRepository_ListByTenant_Pagination verifies Limit/Offset pagination and
// that the total count reflects all matching rows independent of the page window.
func TestInstructionRepository_ListByTenant_Pagination(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	const totalRows = 5
	for i := 0; i < totalRows; i++ {
		inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
		require.NoError(t, repo.Save(ctx, inst, fmt.Sprintf("page-%d-%s", i, inst.ID)))
	}

	// First page of 2.
	page1, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
		TenantID: tenantID.String(),
		Limit:    2,
		Offset:   0,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(totalRows), total)
	require.Len(t, page1, 2)

	// Second page of 2.
	page2, _, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
		TenantID: tenantID.String(),
		Limit:    2,
		Offset:   2,
	})
	require.NoError(t, err)
	require.Len(t, page2, 2)

	// Final page (remainder).
	page3, _, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
		TenantID: tenantID.String(),
		Limit:    2,
		Offset:   4,
	})
	require.NoError(t, err)
	require.Len(t, page3, 1)

	// Pages must not overlap (stable created_at DESC, id DESC ordering).
	seen := map[uuid.UUID]bool{}
	for _, r := range append(append(page1, page2...), page3...) {
		assert.False(t, seen[r.ID], "instruction %s appeared on more than one page", r.ID)
		seen[r.ID] = true
	}
	assert.Len(t, seen, totalRows)
}

// TestInstructionRepository_ListByTenant_DefaultLimit verifies that a zero Limit falls back
// to the repository default rather than returning an unbounded or empty result set.
func TestInstructionRepository_ListByTenant_DefaultLimit(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, inst, fmt.Sprintf("idem-%s", inst.ID)))

	// Limit omitted (0) -> default applies, the single row is returned.
	results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
		TenantID: tenantID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, results, 1)
	assert.Equal(t, inst.ID, results[0].ID)
}

// TestInstructionRepository_ListByTenant_Empty verifies a tenant with no instructions
// yields an empty (non-nil-error) result and a zero total.
func TestInstructionRepository_ListByTenant_Empty(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewInstructionRepository(db)

	results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
		TenantID: uuid.New().String(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, results)
}

// TestInstructionRepository_ListByTenant_LoadsAttempts verifies that ListByTenant hydrates
// each instruction's attempts (exercising the per-row fetchAttempts path).
func TestInstructionRepository_ListByTenant_LoadsAttempts(t *testing.T) {
	db, ctx := setupTestDB(t)

	tenantID := uuid.New()
	connID := uuid.New()
	insertTestConnection(t, db, tenantID, connID)

	repo := NewInstructionRepository(db)

	inst := makeInstruction(t, tenantID, connID.String(), domain.PriorityNormal)
	require.NoError(t, repo.Save(ctx, inst, fmt.Sprintf("idem-%s", inst.ID)))

	// Insert two attempt rows directly for this instruction.
	for n := 1; n <= 2; n++ {
		require.NoError(t, db.Exec(`
			INSERT INTO instruction_attempts
				(instruction_id, attempt_number, dispatched_at, response_status_code)
			VALUES (?, ?, ?, ?)
		`, inst.ID, n, time.Now(), 200).Error)
	}

	results, total, err := repo.ListByTenant(ctx, ports.ListInstructionsParams{
		TenantID: tenantID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, results, 1)
	assert.Len(t, results[0].Attempts, 2)
}

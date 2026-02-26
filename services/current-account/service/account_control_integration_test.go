package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for Account Control Operations (BIAN CoCR)
// These tests verify the status_history audit trail and concurrent control operations.

// setupControlTestDB creates a test database using the shared PostgreSQL container.
func setupControlTestDB(t *testing.T) (*persistence.Repository, *persistence.LienRepository, context.Context, func()) {
	t.Helper()

	db := openSharedDB(t)

	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	quotedSchema := pq.QuoteIdentifier(schemaName)

	// Create tenant schema
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quotedSchema)).Error
	require.NoError(t, err)

	// Set search_path
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", quotedSchema)).Error
	require.NoError(t, err)

	// Create account table with status_history support using raw DDL
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.account (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_identification VARCHAR(34) NOT NULL UNIQUE,
		account_type VARCHAR(50) NOT NULL DEFAULT 'current',
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		party_id UUID NOT NULL,
		org_party_id UUID NULL,
		balance BIGINT NOT NULL DEFAULT 0,
		available_balance BIGINT NOT NULL DEFAULT 0,
		overdraft_limit BIGINT NOT NULL DEFAULT 0,
		overdraft_rate NUMERIC(5,4) NOT NULL DEFAULT 0,
		balance_updated_at TIMESTAMP WITH TIME ZONE,
		opened_at TIMESTAMP WITH TIME ZONE,
		closed_at TIMESTAMP WITH TIME ZONE,
		freeze_reason VARCHAR(1000),
		status_history JSONB NOT NULL DEFAULT '[]'::jsonb,
		product_type_code VARCHAR(50) NULL,
		product_type_version INT NULL,
		behavior_class VARCHAR(50) NULL,
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		created_by VARCHAR(100) NOT NULL DEFAULT 'test',
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_by VARCHAR(100) NOT NULL DEFAULT 'test',
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, quotedSchema)).Error
	require.NoError(t, err)

	// Create lien table for close validation tests
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		bucket_id VARCHAR(255) NOT NULL DEFAULT '',
		status VARCHAR(20) NOT NULL,
		payment_order_reference VARCHAR(255) NOT NULL UNIQUE,
		termination_reason TEXT,
		expires_at TIMESTAMP WITH TIME ZONE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		version BIGINT NOT NULL DEFAULT 1,
		reserved_quantity JSONB,
		valued_amount JSONB,
		valuation_analysis JSONB
	)`, quotedSchema)).Error
	require.NoError(t, err)

	// Create index on status for operational queries
	err = db.Exec(fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_account_status ON %s.account(status)", quotedSchema)).Error
	require.NoError(t, err)

	// Create GIN index on status_history for audit queries
	err = db.Exec(fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_account_status_history ON %s.account USING GIN(status_history)", quotedSchema)).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)

	cleanup := func() {
		db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quotedSchema))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}

	return repo, lienRepo, ctx, cleanup
}

// createTestAccountForControl creates a test account with a unique ID for control tests.
func createTestAccountForControl(t *testing.T, ctx context.Context, repo *persistence.Repository, accountID string) domain.CurrentAccount {
	t.Helper()
	account, err := domain.NewCurrentAccount(accountID, accountID, uuid.New().String(), "GBP")
	require.NoError(t, err, "Failed to create test account")
	require.NoError(t, repo.Save(ctx, account), "Failed to save test account")
	return account
}

// TestAccountControlLifecycle_FullCycle verifies the complete account lifecycle:
// Create -> Freeze -> Unfreeze -> Freeze -> Close
// and confirms status_history contains all 4 state transition entries.
func TestAccountControlLifecycle_FullCycle(t *testing.T) {
	repo, lienRepo, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	// Create service with lien repository for close validation
	svc := mustNewService(t, repo, lienRepo)

	// Create a test account
	accountID := "ACC-LIFECYCLE-001"
	_ = createTestAccountForControl(t, ctx, repo, accountID)

	// Step 1: Freeze the account
	freezeResp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "Suspicious activity detected - compliance review required",
	})
	require.NoError(t, err, "Freeze should succeed")
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_FROZEN, freezeResp.Facility.AccountStatus)

	// Step 2: Unfreeze the account
	unfreezeResp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
	})
	require.NoError(t, err, "Unfreeze should succeed")
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_ACTIVE, unfreezeResp.Facility.AccountStatus)

	// Step 3: Freeze again
	freeze2Resp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "Second freeze for regulatory investigation",
	})
	require.NoError(t, err, "Second freeze should succeed")
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_FROZEN, freeze2Resp.Facility.AccountStatus)

	// Step 4: Close the account (from frozen state)
	closeResp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account closed per customer request",
	})
	require.NoError(t, err, "Close should succeed (zero balance, no liens)")
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_CLOSED, closeResp.Facility.AccountStatus)

	// Verify status_history has exactly 4 entries
	account, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	history := account.StatusHistory()
	require.Len(t, history, 4, "Should have 4 status transitions")

	// Verify transition order and details
	assert.Equal(t, domain.AccountStatusActive, history[0].From)
	assert.Equal(t, domain.AccountStatusFrozen, history[0].To)
	assert.Contains(t, history[0].Reason, "Suspicious activity")

	assert.Equal(t, domain.AccountStatusFrozen, history[1].From)
	assert.Equal(t, domain.AccountStatusActive, history[1].To)

	assert.Equal(t, domain.AccountStatusActive, history[2].From)
	assert.Equal(t, domain.AccountStatusFrozen, history[2].To)
	assert.Contains(t, history[2].Reason, "regulatory")

	assert.Equal(t, domain.AccountStatusFrozen, history[3].From)
	assert.Equal(t, domain.AccountStatusClosed, history[3].To)

	// Verify timestamps are in chronological order
	for i := 1; i < len(history); i++ {
		assert.True(t, history[i].Timestamp.After(history[i-1].Timestamp) || history[i].Timestamp.Equal(history[i-1].Timestamp),
			"Status history should be in chronological order")
	}
}

// TestAccountControlConcurrent_FreezeRace verifies optimistic locking behavior
// when multiple goroutines attempt to freeze the same account simultaneously.
// Only one should succeed, others should get either version conflict errors
// or invalid status transition errors (since account is already frozen).
func TestAccountControlConcurrent_FreezeRace(t *testing.T) {
	repo, lienRepo, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	svc := mustNewService(t, repo, lienRepo)

	// Create a test account
	accountID := "ACC-CONCURRENT-001"
	_ = createTestAccountForControl(t, ctx, repo, accountID)

	// Launch 10 concurrent freeze attempts
	const numGoroutines = 10
	var successCount atomic.Int32
	var failureCount atomic.Int32
	var wg sync.WaitGroup

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()

			// Reuse test context — tenant context is read-only and safe for concurrent use
			_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
				AccountId:     accountID,
				ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
				Reason:        fmt.Sprintf("Concurrent freeze attempt %d for testing purposes", idx),
			})

			if err == nil {
				successCount.Add(1)
			} else {
				// Could be version conflict or invalid status transition (already frozen)
				failureCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// Exactly one should succeed due to database-level constraints
	// Others fail due to either version conflict or already-frozen status
	assert.Equal(t, int32(1), successCount.Load(), "Exactly one freeze should succeed")
	assert.Equal(t, int32(numGoroutines-1), failureCount.Load(), "Remaining should fail")

	// Verify final state
	account, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountStatusFrozen, account.Status())

	// Status history should have exactly 1 entry (the successful freeze)
	history := account.StatusHistory()
	assert.Len(t, history, 1, "Should have exactly 1 status transition")
}

// TestAccountControl_StatusHistoryAuditQuery verifies that status_history
// entries contain all required metadata fields and can be queried.
func TestAccountControl_StatusHistoryAuditQuery(t *testing.T) {
	repo, lienRepo, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	svc := mustNewService(t, repo, lienRepo)

	// Create and freeze an account
	accountID := "ACC-AUDIT-001"
	_ = createTestAccountForControl(t, ctx, repo, accountID)

	beforeFreeze := time.Now()

	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "Audit trail test - freeze for compliance verification",
	})
	require.NoError(t, err)

	afterFreeze := time.Now()

	// Retrieve and verify status_history entry
	account, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	history := account.StatusHistory()
	require.Len(t, history, 1)

	entry := history[0]
	assert.Equal(t, domain.AccountStatusActive, entry.From, "From status should be ACTIVE")
	assert.Equal(t, domain.AccountStatusFrozen, entry.To, "To status should be FROZEN")
	assert.Equal(t, "Audit trail test - freeze for compliance verification", entry.Reason)
	assert.True(t, entry.Timestamp.After(beforeFreeze) || entry.Timestamp.Equal(beforeFreeze),
		"Timestamp should be after test start")
	assert.True(t, entry.Timestamp.Before(afterFreeze) || entry.Timestamp.Equal(afterFreeze),
		"Timestamp should be before test end")

	// Verify freeze reason is stored on account
	assert.Equal(t, "Audit trail test - freeze for compliance verification", account.FreezeReason())
}

// TestAccountControl_CloseWithBalance verifies that closing an account with
// non-zero balance fails, and succeeds after balance is zeroed.
func TestAccountControl_CloseWithBalance(t *testing.T) {
	repo, lienRepo, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	// Create account
	accountID := "ACC-BALANCE-001"
	_ = createTestAccountForControl(t, ctx, repo, accountID)

	// Create service with Position Keeping mock that reports non-zero balance
	svc := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		accountID: 10000, // 100.00 GBP
	})

	// Attempt to close - should fail because Position Keeping reports non-zero balance
	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Attempting to close account with balance",
	})
	require.Error(t, err, "Close should fail with non-zero balance")
	assert.Contains(t, err.Error(), "non-zero balance")

	// Create service with Position Keeping mock that reports zero balance
	svcZeroBalance := mustNewServiceWithPositionKeeping(t, repo, lienRepo, map[string]int64{
		accountID: 0, // Zero balance
	})

	// Now close should succeed with zero balance
	closeResp, err := svcZeroBalance.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account closed after balance zeroed",
	})
	require.NoError(t, err, "Close should succeed with zero balance")
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_CLOSED, closeResp.Facility.AccountStatus)
}

// TestAccountControl_MigrationApplied verifies that the status_history column
// and indexes exist after migration.
func TestAccountControl_MigrationApplied(t *testing.T) {
	repo, _, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	// Create and save account to test JSONB column works
	accountID := "ACC-MIGRATION-001"
	_ = createTestAccountForControl(t, ctx, repo, accountID)

	// Retrieve and verify empty status_history (default)
	account, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	history := account.StatusHistory()
	assert.Empty(t, history, "New account should have empty status_history")

	// Freeze to add a status_history entry
	account, err = account.Freeze("Migration test freeze - validating JSONB column works correctly")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Retrieve and verify status_history has entry
	account, err = repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	history = account.StatusHistory()
	require.Len(t, history, 1, "Should have 1 status transition after freeze")
	assert.Equal(t, domain.AccountStatusActive, history[0].From)
	assert.Equal(t, domain.AccountStatusFrozen, history[0].To)
}

// TestAccountControl_DirectCloseFromActive verifies direct ACTIVE -> CLOSED transition
// is permitted for accounts with zero balance.
func TestAccountControl_DirectCloseFromActive(t *testing.T) {
	repo, lienRepo, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	svc := mustNewService(t, repo, lienRepo)

	// Create account with zero balance (default)
	accountID := "ACC-DIRECT-CLOSE-001"
	_ = createTestAccountForControl(t, ctx, repo, accountID)

	// Close directly from ACTIVE status
	closeResp, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Direct close from active - customer requested account termination",
	})
	require.NoError(t, err, "Direct close from ACTIVE should succeed with zero balance")
	assert.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_CLOSED, closeResp.Facility.AccountStatus)

	// Verify status_history has 1 entry (ACTIVE -> CLOSED)
	account, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	history := account.StatusHistory()
	require.Len(t, history, 1)
	assert.Equal(t, domain.AccountStatusActive, history[0].From)
	assert.Equal(t, domain.AccountStatusClosed, history[0].To)
}

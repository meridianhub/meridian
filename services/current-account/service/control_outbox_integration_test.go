package service

import (
	"fmt"
	"testing"

	"github.com/lib/pq"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupControlOutboxDB creates a test database with both account and event_outbox tables.
func setupControlOutboxDB(t *testing.T) (*gorm.DB, *persistence.Repository, *persistence.LienRepository, tenant.TenantID, func()) {
	t.Helper()

	db := openSharedDB(t)

	// Pin to a single connection so that SET search_path (which is connection-scoped)
	// is reliably applied to every subsequent query on this *gorm.DB instance.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	quotedSchema := pq.QuoteIdentifier(schemaName)

	// Create tenant schema
	err = db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quotedSchema)).Error
	require.NoError(t, err)

	// Set search_path (applies to the pinned connection for all subsequent queries)
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", quotedSchema)).Error
	require.NoError(t, err)

	// Create account table (same DDL as setupControlTestDB)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.account (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_identification VARCHAR(34) NOT NULL UNIQUE,
		account_type VARCHAR(50) NOT NULL DEFAULT 'current',
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		precision INT NOT NULL DEFAULT 2,
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

	// Create lien table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		instrument_code VARCHAR(32) NOT NULL DEFAULT '',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		precision INT NOT NULL DEFAULT 2,
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

	// Create event_outbox table in public schema (shared across tenants)
	err = db.AutoMigrate(&events.EventOutbox{})
	require.NoError(t, err, "failed to create event_outbox table")

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)

	cleanup := func() {
		_ = db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quotedSchema))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return db, repo, lienRepo, tid, cleanup
}

// TestControlCurrentAccount_OutboxEvents verifies that control actions write events to the
// event outbox table atomically with the account state change.
func TestControlCurrentAccount_OutboxEvents(t *testing.T) {
	db, repo, lienRepo, tid, cleanup := setupControlOutboxDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tid)

	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Use a unique account ID per test run to avoid cross-test pollution on the shared
	// public.event_outbox table (which is not scoped to the tenant schema).
	accountID := fmt.Sprintf("ACC-OUTBOX-%s", tid)

	// Mock position keeping returns zero balance (needed for CLOSE validation)
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			accountID: 0,
		},
	}

	// Build a service wired with outboxPublisher and db
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		outboxRepo:       outboxRepo,
		outboxPublisher:  events.NewOutboxPublisher("current-account"),
		db:               db,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	_ = createTestAccountForControl(t, ctx, repo, accountID)

	t.Run("FreezeWritesOutboxEntry", func(t *testing.T) {
		_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
			AccountId:     accountID,
			ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
			Reason:        "Compliance hold for regulatory review",
		})
		require.NoError(t, err)

		// Verify exactly one outbox entry was created for the freeze event.
		// Filter by topic + aggregate_id + service_name to avoid matching rows from other runs.
		var entries []events.EventOutbox
		err = db.Where(
			"topic = ? AND aggregate_id = ? AND service_name = ?",
			topics.CurrentAccountAccountFrozenV1, accountID, "current-account",
		).Find(&entries).Error
		require.NoError(t, err)
		require.Len(t, entries, 1, "exactly one frozen event should be in outbox")

		e := entries[0]
		assert.Equal(t, topics.CurrentAccountAccountFrozenV1, e.Topic)
		assert.Equal(t, "current_account.account_frozen.v1", e.EventType)
		assert.Equal(t, "CurrentAccount", e.AggregateType)
		assert.Equal(t, accountID, e.AggregateID)
		assert.Equal(t, "current-account", e.ServiceName)
		assert.Equal(t, events.StatusPending, e.Status)
		assert.NotEmpty(t, e.EventPayload)
	})

	t.Run("UnfreezeWritesOutboxEntry", func(t *testing.T) {
		_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
			AccountId:     accountID,
			ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
		})
		require.NoError(t, err)

		var entries []events.EventOutbox
		err = db.Where(
			"topic = ? AND aggregate_id = ? AND service_name = ?",
			topics.CurrentAccountAccountUnfrozenV1, accountID, "current-account",
		).Find(&entries).Error
		require.NoError(t, err)
		require.Len(t, entries, 1, "exactly one unfrozen event should be in outbox")

		e := entries[0]
		assert.Equal(t, topics.CurrentAccountAccountUnfrozenV1, e.Topic)
		assert.Equal(t, "current_account.account_unfrozen.v1", e.EventType)
		assert.Equal(t, "CurrentAccount", e.AggregateType)
		assert.Equal(t, accountID, e.AggregateID)
		assert.Equal(t, events.StatusPending, e.Status)
	})

	t.Run("CloseWritesOutboxEntry", func(t *testing.T) {
		_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
			AccountId:     accountID,
			ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
			Reason:        "Customer requested account closure",
		})
		require.NoError(t, err)

		var entries []events.EventOutbox
		err = db.Where(
			"topic = ? AND aggregate_id = ? AND service_name = ?",
			topics.CurrentAccountAccountClosedV1, accountID, "current-account",
		).Find(&entries).Error
		require.NoError(t, err)
		require.Len(t, entries, 1, "exactly one closed event should be in outbox")

		e := entries[0]
		assert.Equal(t, topics.CurrentAccountAccountClosedV1, e.Topic)
		assert.Equal(t, "current_account.account_closed.v1", e.EventType)
		assert.Equal(t, "CurrentAccount", e.AggregateType)
		assert.Equal(t, accountID, e.AggregateID)
		assert.Equal(t, events.StatusPending, e.Status)
	})
}

// TestControlCurrentAccount_FallbackWithoutOutbox verifies that control actions succeed
// via the fallback path (direct repo.Save) when the outbox publisher is not configured.
// This ensures backward compatibility for test environments and services that start without
// a database connection available at construction time.
func TestControlCurrentAccount_FallbackWithoutOutbox(t *testing.T) {
	db, repo, lienRepo, tid, cleanup := setupControlOutboxDB(t)
	defer cleanup()

	ctx := tenant.WithTenant(t.Context(), tid)

	accountID := fmt.Sprintf("ACC-FALLBACK-%s", tid)

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			accountID: 0, // zero balance required for CLOSE validation
		},
	}

	// Service without outboxPublisher or db — exercises fallback path
	svc := &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: mockPosKeeping,
		logger:           testLogger(),
	}

	_ = createTestAccountForControl(t, ctx, repo, accountID)

	// All three control actions should succeed via fallback (repo.Save only)
	_, err := svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_FREEZE,
		Reason:        "Testing fallback path without outbox",
	})
	require.NoError(t, err, "freeze should succeed without outbox publisher")

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNFREEZE,
	})
	require.NoError(t, err, "unfreeze should succeed without outbox publisher")

	_, err = svc.ControlCurrentAccount(ctx, &pb.ControlCurrentAccountRequest{
		AccountId:     accountID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Close via fallback path",
	})
	require.NoError(t, err, "close should succeed without outbox publisher")

	// Confirm no outbox rows were written — the fallback path must not silently emit events.
	var outboxCount int64
	err = db.Model(&events.EventOutbox{}).
		Where("aggregate_id = ? AND service_name = ?", accountID, "current-account").
		Count(&outboxCount).Error
	require.NoError(t, err)
	assert.Zero(t, outboxCount, "fallback path should not write to event_outbox")
}

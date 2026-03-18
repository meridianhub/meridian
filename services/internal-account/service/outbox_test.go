package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/require"
)

// TestSaveAccountWithEvent_OutboxPath tests that saveAccountWithEvent uses the
// transactional outbox path when outboxPublisher and db are configured.
// This covers lines 873-892 in server.go.
func TestSaveAccountWithEvent_OutboxPath(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	tid := tenant.TenantID("outbox_test_tenant")
	schemaName := tid.SchemaName()

	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create internal_account table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.internal_account (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_code VARCHAR(50) NOT NULL,
		name VARCHAR(255) NOT NULL,
		account_type VARCHAR(20) NOT NULL DEFAULT 'CLEARING',
		clearing_purpose VARCHAR(32) NULL,
		org_party_id UUID NULL,
		product_type_code VARCHAR(100) NULL,
		product_type_version INTEGER NULL,
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(32) NOT NULL DEFAULT 'CURRENCY',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		counterparty_id VARCHAR(50),
		counterparty_name VARCHAR(255),
		counterparty_external_ref VARCHAR(100),
		attributes JSONB NOT NULL DEFAULT '{}',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		created_by VARCHAR(100) NOT NULL DEFAULT 'system',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_by VARCHAR(100) NOT NULL DEFAULT 'system',
		deleted_at TIMESTAMPTZ,
		version BIGINT NOT NULL DEFAULT 1
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create event_outbox table (required by OutboxPublisher.Publish)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.event_outbox (
		id UUID PRIMARY KEY,
		event_type VARCHAR(200) NOT NULL,
		aggregate_id VARCHAR(100) NOT NULL,
		aggregate_type VARCHAR(100) NOT NULL,
		event_payload BYTEA NOT NULL,
		correlation_id VARCHAR(100),
		causation_id VARCHAR(100),
		status VARCHAR(20) NOT NULL,
		topic VARCHAR(200) NOT NULL,
		partition_key VARCHAR(200),
		created_at TIMESTAMPTZ NOT NULL,
		processed_at TIMESTAMPTZ,
		retry_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT,
		service_name VARCHAR(100) NOT NULL,
		tenant_id VARCHAR(100) NOT NULL
	)`, schemaName)).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	publisher := events.NewOutboxPublisher("internal-account")

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithOutboxPublisher(publisher, db),
	)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		fmt.Sprintf("IBA-OUTBOX-%s", uuid.New().String()[:8]),
		fmt.Sprintf("OUTBOX-%s", uuid.New().String()[:6]),
		"Outbox Test Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)

	// saveAccountWithEvent takes the outbox transaction path when publisher+db are set
	err = svc.saveAccountWithEvent(ctx, account)
	require.NoError(t, err)
}

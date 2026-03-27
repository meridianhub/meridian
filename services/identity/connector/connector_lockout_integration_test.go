//go:build integration

package connector_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/services/identity/connector"
	emailpkg "github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupEmailOutboxTable creates the email_outbox table in the public schema.
// The outbox is not tenant-schema-based; it lives in public with a tenant_id column.
func setupEmailOutboxTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	ddl := `
		CREATE TABLE IF NOT EXISTS email_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id VARCHAR NOT NULL,
			idempotency_key VARCHAR(255) NOT NULL,
			to_addresses TEXT[] NOT NULL,
			from_address VARCHAR(255) NOT NULL DEFAULT 'noreply@meridianhub.cloud',
			subject VARCHAR(500) NOT NULL DEFAULT '',
			template_name VARCHAR(100) NOT NULL DEFAULT '',
			template_data JSONB NOT NULL DEFAULT '{}',
			status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
			attempts INT NOT NULL DEFAULT 0,
			max_attempts INT NOT NULL DEFAULT 5,
			next_attempt_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			last_error TEXT,
			cancelled_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			UNIQUE (tenant_id, idempotency_key)
		)
	`
	require.NoError(t, db.Exec(ddl).Error)
}

func TestLockout_Integration_5FailuresQueuesEmailInOutbox(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// Set up identity schema for a single tenant.
	const lockoutTenant = "lockout_test_tenant"
	ctx := setupIdentitySchema(t, db, lockoutTenant)

	// Set up email outbox table in public schema.
	setupEmailOutboxTable(t, db)

	repo := persistence.NewRepository(db)
	outboxRepo := emailpkg.NewPostgresOutboxRepository(db)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn, err := connector.New(repo, logger, connector.WithEmailOutbox(outboxRepo))
	require.NoError(t, err)

	// Create active identity.
	const lockoutEmail = "lockout-integ@example.com"
	const lockoutPass = "LockoutPass123!"
	infra := &multiTenantInfra{db: db, repo: repo, conn: conn}
	infra.createActiveIdentity(t, ctx, lockoutEmail, lockoutPass)

	// Fail login 5 times.
	for i := range 5 {
		_, valid, loginErr := conn.Login(ctx, nil, lockoutEmail, "WrongPassword999!")
		require.NoError(t, loginErr, "attempt %d should not return an error", i+1)
		assert.False(t, valid, "attempt %d should return false", i+1)
	}

	// Verify exactly one outbox entry was created.
	var count int64
	err = db.Table("email_outbox").
		Where("tenant_id = ? AND template_name = ?", lockoutTenant, "account-lockout").
		Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "expected one lockout email in outbox")

	// Verify the email target.
	var entry emailpkg.OutboxEntity
	err = db.Table("email_outbox").
		Where("tenant_id = ? AND template_name = ?", lockoutTenant, "account-lockout").
		First(&entry).Error
	require.NoError(t, err)
	require.Len(t, []string(entry.ToAddresses), 1)
	assert.Equal(t, lockoutEmail, []string(entry.ToAddresses)[0])
	assert.Equal(t, "PENDING", entry.Status)
}

func TestLockout_Integration_IdempotentConcurrentLockout(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	const idempTenant = "idemp_lockout_tenant"
	ctx := setupIdentitySchema(t, db, idempTenant)
	setupEmailOutboxTable(t, db)

	repo := persistence.NewRepository(db)
	outboxRepo := emailpkg.NewPostgresOutboxRepository(db)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn, err := connector.New(repo, logger, connector.WithEmailOutbox(outboxRepo))
	require.NoError(t, err)

	infra := &multiTenantInfra{db: db, repo: repo, conn: conn}
	infra.createActiveIdentity(t, ctx, "idemp@example.com", "IdempPass123!")

	// Fail login 4 times to prime the counter.
	for i := range 4 {
		_, valid, loginErr := conn.Login(ctx, nil, "idemp@example.com", "WrongPassword999!")
		require.NoError(t, loginErr, "prime attempt %d should not return an error", i+1)
		assert.False(t, valid, "prime attempt %d should return false", i+1)
	}

	// Two concurrent 5th failures: simulate by calling sequentially.
	// First call locks and queues email; second call is rejected at status check (already locked).
	_, valid, loginErr := conn.Login(ctx, nil, "idemp@example.com", "WrongPassword999!")
	require.NoError(t, loginErr)
	assert.False(t, valid)
	_, valid, loginErr = conn.Login(ctx, nil, "idemp@example.com", "WrongPassword999!")
	require.NoError(t, loginErr)
	assert.False(t, valid)

	// Exactly one email should be in the outbox regardless.
	var count int64
	err = db.Table("email_outbox").
		Where("tenant_id = ? AND template_name = ?", idempTenant, "account-lockout").
		Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "concurrent lockout must produce exactly one outbox entry")
}

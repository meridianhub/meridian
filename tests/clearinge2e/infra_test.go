//go:build integration
// +build integration

package clearinge2e

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// =============================================================================
// Test Infrastructure Types
// =============================================================================

// serviceDB holds a PostgreSQL database for a service's bounded context.
type serviceDB struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
	connStr   string
	name      string
}

// serviceGRPC holds a gRPC server and client for a service.
type serviceGRPC struct {
	server   *grpc.Server
	listener net.Listener
}

// e2eTestInfra holds all test infrastructure for cross-service E2E tests.
type e2eTestInfra struct {
	// Databases (one per bounded context)
	currentAccountDB      *serviceDB
	internalAccountDB     *serviceDB
	positionKeepingDB     *serviceDB
	financialAccountingDB *serviceDB

	// gRPC services (if needed for real service testing)
	internalAccountGRPC *serviceGRPC
}

// =============================================================================
// Infrastructure Setup
// =============================================================================

// setupE2EInfra creates all test infrastructure including databases and services.
func setupE2EInfra(t *testing.T) *e2eTestInfra {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	infra := &e2eTestInfra{}

	// Set up databases for each bounded context in parallel
	dbSetupDone := make(chan struct{})
	go func() {
		defer close(dbSetupDone)

		// Current Account database
		infra.currentAccountDB = setupServiceDB(ctx, t, "meridian_current_account")

		// Internal Account database
		infra.internalAccountDB = setupServiceDB(ctx, t, "meridian_internal_account")

		// Position Keeping database
		infra.positionKeepingDB = setupServiceDB(ctx, t, "meridian_position_keeping")

		// Financial Accounting database
		infra.financialAccountingDB = setupServiceDB(ctx, t, "meridian_financial_accounting")
	}()

	select {
	case <-dbSetupDone:
		// Databases are ready
	case <-ctx.Done():
		t.Fatal("timeout waiting for database setup")
	}

	// Register cleanup
	t.Cleanup(func() {
		infra.cleanup()
	})

	return infra
}

// setupServiceDB creates a PostgreSQL database container for a service.
func setupServiceDB(ctx context.Context, t *testing.T, dbName string) *serviceDB {
	t.Helper()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second),
		),
	)
	require.NoError(t, err, "failed to start postgres container for %s", dbName)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string for %s", dbName)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err, "failed to create connection pool for %s", dbName)

	return &serviceDB{
		container: pgContainer,
		pool:      pool,
		connStr:   connStr,
		name:      dbName,
	}
}

// cleanup releases all test infrastructure.
func (infra *e2eTestInfra) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Close gRPC services
	if infra.internalAccountGRPC != nil {
		if infra.internalAccountGRPC.server != nil {
			infra.internalAccountGRPC.server.GracefulStop()
		}
		if infra.internalAccountGRPC.listener != nil {
			_ = infra.internalAccountGRPC.listener.Close()
		}
	}

	// Close database connections
	for _, db := range []*serviceDB{
		infra.currentAccountDB,
		infra.internalAccountDB,
		infra.positionKeepingDB,
		infra.financialAccountingDB,
	} {
		if db != nil {
			if db.pool != nil {
				db.pool.Close()
			}
			if db.container != nil {
				_ = db.container.Terminate(ctx)
			}
		}
	}
}

// =============================================================================
// Tenant Schema Setup
// =============================================================================

// createTenantSchema creates tenant-specific schema with required tables.
func createTenantSchema(t *testing.T, db *serviceDB, tenantID tenant.TenantID) {
	t.Helper()

	ctx := context.Background()
	schemaName := tenantID.SchemaName()

	// Create schema
	_, err := db.pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s",
		pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "failed to create schema %s", pq.QuoteIdentifier(schemaName))
}

// setupCurrentAccountSchema applies Current Account service schema.
func setupCurrentAccountSchema(t *testing.T, db *serviceDB, schemaName string) {
	t.Helper()

	ctx := context.Background()

	// Create accounts table
	accountsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.accounts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_number VARCHAR(50) NOT NULL UNIQUE,
			party_id UUID NOT NULL,
			instrument_code VARCHAR(20) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			version INTEGER NOT NULL DEFAULT 1
		)
	`, pq.QuoteIdentifier(schemaName))

	_, err := db.pool.Exec(ctx, accountsSQL)
	require.NoError(t, err, "failed to create accounts table")

	// Create liens table
	liensSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.liens (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id UUID NOT NULL REFERENCES %s.accounts(id),
			amount DECIMAL(18, 8) NOT NULL,
			reason VARCHAR(100) NOT NULL,
			reference_id VARCHAR(100),
			status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			released_at TIMESTAMP
		)
	`, pq.QuoteIdentifier(schemaName), pq.QuoteIdentifier(schemaName))

	_, err = db.pool.Exec(ctx, liensSQL)
	require.NoError(t, err, "failed to create liens table")
}

// setupInternalAccountSchema applies Internal Account service schema.
func setupInternalAccountSchema(t *testing.T, db *serviceDB, schemaName string) {
	t.Helper()

	ctx := context.Background()

	// Create internal_accounts table
	accountsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.internal_accounts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_code VARCHAR(50) NOT NULL UNIQUE,
			account_type VARCHAR(50) NOT NULL,
			instrument_code VARCHAR(20) NOT NULL,
			clearing_purpose VARCHAR(50),
			status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
			description TEXT,
			position_keeping_account_id UUID,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			version INTEGER NOT NULL DEFAULT 1
		)
	`, pq.QuoteIdentifier(schemaName))

	_, err := db.pool.Exec(ctx, accountsSQL)
	require.NoError(t, err, "failed to create internal_accounts table")

	// Create index on clearing_purpose for efficient lookups
	indexSQL := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_iba_clearing_purpose
		ON %s.internal_accounts(clearing_purpose, instrument_code)
		WHERE clearing_purpose IS NOT NULL
	`, pq.QuoteIdentifier(schemaName))

	_, err = db.pool.Exec(ctx, indexSQL)
	require.NoError(t, err, "failed to create clearing_purpose index")
}

// setupPositionKeepingSchema applies Position Keeping service schema.
func setupPositionKeepingSchema(t *testing.T, db *serviceDB, schemaName string) {
	t.Helper()

	ctx := context.Background()

	// Create position_logs table
	positionLogsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.position_logs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id UUID NOT NULL,
			instrument_code VARCHAR(20) NOT NULL,
			bucket_key VARCHAR(100) NOT NULL,
			amount DECIMAL(18, 8) NOT NULL,
			dimension VARCHAR(50) NOT NULL DEFAULT 'QUANTITY',
			attributes JSONB,
			reference_id VARCHAR(100),
			reference_type VARCHAR(50),
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`, pq.QuoteIdentifier(schemaName))

	_, err := db.pool.Exec(ctx, positionLogsSQL)
	require.NoError(t, err, "failed to create position_logs table")

	// Create index for balance queries
	indexSQL := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_position_account_instrument
		ON %s.position_logs(account_id, instrument_code, bucket_key)
	`, pq.QuoteIdentifier(schemaName))

	_, err = db.pool.Exec(ctx, indexSQL)
	require.NoError(t, err, "failed to create position_logs index")
}

// setupFinancialAccountingSchema applies Financial Accounting service schema.
func setupFinancialAccountingSchema(t *testing.T, db *serviceDB, schemaName string) {
	t.Helper()

	ctx := context.Background()

	// Create ledger_postings table
	postingsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.ledger_postings (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			posting_reference VARCHAR(100) NOT NULL UNIQUE,
			debit_account_id UUID NOT NULL,
			credit_account_id UUID NOT NULL,
			instrument_code VARCHAR(20) NOT NULL,
			amount DECIMAL(18, 8) NOT NULL,
			posting_date DATE NOT NULL,
			value_date DATE NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
			narrative TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP
		)
	`, pq.QuoteIdentifier(schemaName))

	_, err := db.pool.Exec(ctx, postingsSQL)
	require.NoError(t, err, "failed to create ledger_postings table")
}

// =============================================================================
// Test Tenant Setup
// =============================================================================

// setupTestTenant creates a tenant with all required schemas across services.
func setupTestTenant(t *testing.T, infra *e2eTestInfra, tenantIDStr string) (context.Context, tenant.TenantID) {
	t.Helper()

	tenantID, err := tenant.NewTenantID(tenantIDStr)
	require.NoError(t, err)

	schemaName := tenantID.SchemaName()

	// Create tenant schema in each service database
	createTenantSchema(t, infra.currentAccountDB, tenantID)
	createTenantSchema(t, infra.internalAccountDB, tenantID)
	createTenantSchema(t, infra.positionKeepingDB, tenantID)
	createTenantSchema(t, infra.financialAccountingDB, tenantID)

	// Apply service-specific schemas
	setupCurrentAccountSchema(t, infra.currentAccountDB, schemaName)
	setupInternalAccountSchema(t, infra.internalAccountDB, schemaName)
	setupPositionKeepingSchema(t, infra.positionKeepingDB, schemaName)
	setupFinancialAccountingSchema(t, infra.financialAccountingDB, schemaName)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tenantID)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, "e2e-test-user")

	// Register schema cleanup
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, db := range []*serviceDB{
			infra.currentAccountDB,
			infra.internalAccountDB,
			infra.positionKeepingDB,
			infra.financialAccountingDB,
		} {
			if db != nil && db.pool != nil {
				_, _ = db.pool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE",
					pq.QuoteIdentifier(schemaName)))
			}
		}
	})

	return ctx, tenantID
}

// =============================================================================
// Test Data Helpers
// =============================================================================

// createClearingAccount creates a clearing account with the specified purpose.
func createClearingAccount(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	accountCode string,
	instrumentCode string,
	clearingPurpose string,
) string {
	t.Helper()

	var accountID string
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s.internal_accounts
		(account_code, account_type, instrument_code, clearing_purpose, status, description)
		VALUES ($1, 'CLEARING', $2, $3, 'ACTIVE', $4)
		RETURNING id
	`, pq.QuoteIdentifier(schemaName))

	description := fmt.Sprintf("Clearing account for %s %s", instrumentCode, clearingPurpose)
	err := db.pool.QueryRow(ctx, insertSQL, accountCode, instrumentCode, clearingPurpose, description).Scan(&accountID)
	require.NoError(t, err, "failed to create clearing account %s", accountCode)

	return accountID
}

// createCustomerAccount creates a customer account for testing.
func createCustomerAccount(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	accountNumber string,
	partyID string,
	instrumentCode string,
) string {
	t.Helper()

	var accountID string
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s.accounts
		(account_number, party_id, instrument_code, status)
		VALUES ($1, $2, $3, 'ACTIVE')
		RETURNING id
	`, pq.QuoteIdentifier(schemaName))

	err := db.pool.QueryRow(ctx, insertSQL, accountNumber, partyID, instrumentCode).Scan(&accountID)
	require.NoError(t, err, "failed to create customer account %s", accountNumber)

	return accountID
}

// recordPosition records a position log entry.
func recordPosition(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	accountID string,
	instrumentCode string,
	bucketKey string,
	amount string,
	referenceID string,
	referenceType string,
) string {
	t.Helper()

	var positionID string
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s.position_logs
		(account_id, instrument_code, bucket_key, amount, reference_id, reference_type)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, pq.QuoteIdentifier(schemaName))

	err := db.pool.QueryRow(ctx, insertSQL, accountID, instrumentCode, bucketKey, amount, referenceID, referenceType).Scan(&positionID)
	require.NoError(t, err, "failed to record position")

	return positionID
}

// createLedgerPosting creates a ledger posting entry.
func createLedgerPosting(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	postingReference string,
	debitAccountID string,
	creditAccountID string,
	instrumentCode string,
	amount string,
	narrative string,
) string {
	t.Helper()

	var postingID string
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s.ledger_postings
		(posting_reference, debit_account_id, credit_account_id, instrument_code, amount, posting_date, value_date, status, narrative)
		VALUES ($1, $2, $3, $4, $5, CURRENT_DATE, CURRENT_DATE, 'COMPLETED', $6)
		RETURNING id
	`, pq.QuoteIdentifier(schemaName))

	err := db.pool.QueryRow(ctx, insertSQL, postingReference, debitAccountID, creditAccountID, instrumentCode, amount, narrative).Scan(&postingID)
	require.NoError(t, err, "failed to create ledger posting")

	return postingID
}

// tryCreateLedgerPosting attempts to create a ledger posting and returns whether it succeeded.
// This is used for testing idempotency where duplicate references should be rejected.
func tryCreateLedgerPosting(
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	postingReference string,
	debitAccountID string,
	creditAccountID string,
	instrumentCode string,
	amount string,
	narrative string,
) (postingID string, err error) {
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s.ledger_postings
		(posting_reference, debit_account_id, credit_account_id, instrument_code, amount, posting_date, value_date, status, narrative)
		VALUES ($1, $2, $3, $4, $5, CURRENT_DATE, CURRENT_DATE, 'COMPLETED', $6)
		RETURNING id
	`, pq.QuoteIdentifier(schemaName))

	err = db.pool.QueryRow(ctx, insertSQL, postingReference, debitAccountID, creditAccountID, instrumentCode, amount, narrative).Scan(&postingID)
	return postingID, err
}

// =============================================================================
// Query Helpers
// =============================================================================

// getClearingAccountByPurpose retrieves a clearing account by purpose and instrument.
func getClearingAccountByPurpose(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	instrumentCode string,
	clearingPurpose string,
) (accountID string, accountCode string, found bool) {
	t.Helper()

	querySQL := fmt.Sprintf(`
		SELECT id, account_code FROM %s.internal_accounts
		WHERE instrument_code = $1 AND clearing_purpose = $2 AND status = 'ACTIVE'
		LIMIT 1
	`, pq.QuoteIdentifier(schemaName))

	err := db.pool.QueryRow(ctx, querySQL, instrumentCode, clearingPurpose).Scan(&accountID, &accountCode)
	if err != nil {
		return "", "", false
	}
	return accountID, accountCode, true
}

// getPositionBalance calculates the current balance for an account and instrument.
func getPositionBalance(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	accountID string,
	instrumentCode string,
) string {
	t.Helper()

	querySQL := fmt.Sprintf(`
		SELECT COALESCE(SUM(amount), 0) FROM %s.position_logs
		WHERE account_id = $1 AND instrument_code = $2
	`, pq.QuoteIdentifier(schemaName))

	var balance string
	err := db.pool.QueryRow(ctx, querySQL, accountID, instrumentCode).Scan(&balance)
	require.NoError(t, err, "failed to get position balance")

	return balance
}

// getLedgerPostingByReference retrieves a ledger posting by reference.
func getLedgerPostingByReference(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	reference string,
) (postingID string, debitAccountID string, creditAccountID string, amount string, found bool) {
	t.Helper()

	querySQL := fmt.Sprintf(`
		SELECT id, debit_account_id, credit_account_id, amount::text
		FROM %s.ledger_postings
		WHERE posting_reference = $1
		LIMIT 1
	`, pq.QuoteIdentifier(schemaName))

	err := db.pool.QueryRow(ctx, querySQL, reference).Scan(&postingID, &debitAccountID, &creditAccountID, &amount)
	if err != nil {
		return "", "", "", "", false
	}
	return postingID, debitAccountID, creditAccountID, amount, true
}

// countPositionLogs counts position logs for an account.
func countPositionLogs(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	accountID string,
) int {
	t.Helper()

	querySQL := fmt.Sprintf(`
		SELECT COUNT(*) FROM %s.position_logs WHERE account_id = $1
	`, pq.QuoteIdentifier(schemaName))

	var count int
	err := db.pool.QueryRow(ctx, querySQL, accountID).Scan(&count)
	require.NoError(t, err, "failed to count position logs")

	return count
}

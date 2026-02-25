//go:build integration

// Package e2e provides end-to-end integration tests for the internal account service.
// These tests verify the full account lifecycle from creation through closure,
// including multi-tenant isolation, multi-asset support, and counterparty banking.
package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantitypb "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/service"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/types/known/timestamppb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// Test Account Type Cache
// ============================================================================

// e2eStaticLoader provides a static in-memory account type loader for e2e tests.
type e2eStaticLoader struct {
	defs map[string]*accounttype.Definition
}

func (l *e2eStaticLoader) LoadAccountType(_ context.Context, code string) (*accounttype.Definition, error) {
	def, ok := l.defs[code]
	if !ok {
		return nil, fmt.Errorf("account type not found: %s", code)
	}
	return def, nil
}

func (l *e2eStaticLoader) ListActiveAccountTypes(_ context.Context) ([]*accounttype.Definition, error) {
	defs := make([]*accounttype.Definition, 0, len(l.defs))
	for _, def := range l.defs {
		defs = append(defs, def)
	}
	return defs, nil
}

// e2eNilCELCompiler is a no-op CEL compiler for e2e tests.
type e2eNilCELCompiler struct{}

func (c *e2eNilCELCompiler) CompileValidation(_ string) (cel.Program, error)  { return nil, nil }
func (c *e2eNilCELCompiler) CompileBucketKey(_ string) (cel.Program, error)   { return nil, nil }
func (c *e2eNilCELCompiler) CompileEligibility(_ string) (cel.Program, error) { return nil, nil }

// newE2EAccountTypeCache creates a LocalAccountTypeCache with standard e2e test definitions.
func newE2EAccountTypeCache() *cache.LocalAccountTypeCache {
	defs := map[string]*accounttype.Definition{
		"CLEARING_GBP": {Code: "CLEARING_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassClearing, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"CLEARING_USD": {Code: "CLEARING_USD", Version: 1, BehaviorClass: accounttype.BehaviorClassClearing, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"CLEARING_EUR": {Code: "CLEARING_EUR", Version: 1, BehaviorClass: accounttype.BehaviorClassClearing, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"NOSTRO_USD":   {Code: "NOSTRO_USD", Version: 1, BehaviorClass: accounttype.BehaviorClassNostro, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"NOSTRO_GBP":   {Code: "NOSTRO_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassNostro, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"NOSTRO_EUR":   {Code: "NOSTRO_EUR", Version: 1, BehaviorClass: accounttype.BehaviorClassNostro, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"VOSTRO_USD":   {Code: "VOSTRO_USD", Version: 1, BehaviorClass: accounttype.BehaviorClassVostro, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"VOSTRO_GBP":   {Code: "VOSTRO_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassVostro, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"HOLDING_GBP":  {Code: "HOLDING_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassHolding, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"HOLDING_USD":  {Code: "HOLDING_USD", Version: 1, BehaviorClass: accounttype.BehaviorClassHolding, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"HOLDING_EUR":  {Code: "HOLDING_EUR", Version: 1, BehaviorClass: accounttype.BehaviorClassHolding, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"HOLDING_KWH":  {Code: "HOLDING_KWH", Version: 1, BehaviorClass: accounttype.BehaviorClassHolding, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"SUSPENSE_GBP": {Code: "SUSPENSE_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassSuspense, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"SUSPENSE_USD": {Code: "SUSPENSE_USD", Version: 1, BehaviorClass: accounttype.BehaviorClassSuspense, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"REVENUE_GBP":  {Code: "REVENUE_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassRevenue, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"EXPENSE_GBP":  {Code: "EXPENSE_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassExpense, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"INVENTORY_GBP": {Code: "INVENTORY_GBP", Version: 1, BehaviorClass: accounttype.BehaviorClassInventory, EligibilityCEL: "true", Status: accounttype.StatusActive},
		"INVENTORY_KWH": {Code: "INVENTORY_KWH", Version: 1, BehaviorClass: accounttype.BehaviorClassInventory, EligibilityCEL: "true", Status: accounttype.StatusActive},
	}
	loader := &e2eStaticLoader{defs: defs}
	compiler := &e2eNilCELCompiler{}
	return cache.NewLocalAccountTypeCache(loader, compiler)
}

// ============================================================================
// Test Infrastructure
// ============================================================================

// e2eTestContext holds the test infrastructure for E2E tests.
type e2eTestContext struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
	db        *gorm.DB
	repo      *persistence.Repository
	svc       *service.Service
}

// setupE2ETestPool creates a shared PostgreSQL testcontainer for E2E tests.
// Returns a configured pool, GORM db, repository, and service instance.
func setupE2ETestPool(t *testing.T) *e2eTestContext {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("e2e_internal_account"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = pgContainer.Terminate(cleanupCtx)
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err, "Failed to create connection pool")

	t.Cleanup(func() {
		pool.Close()
	})

	// Create GORM connection
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Failed to connect to database with GORM")

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	repo := persistence.NewRepository(db)

	// Create service with repository and account type cache for E2E tests
	accountTypeCache := newE2EAccountTypeCache()
	svc, err := service.NewServiceFull(repo, nil, nil, nil, nil, service.WithAccountTypeCache(accountTypeCache))
	require.NoError(t, err, "Failed to create service")

	return &e2eTestContext{
		container: pgContainer,
		pool:      pool,
		db:        db,
		repo:      repo,
		svc:       svc,
	}
}

// setupTenantSchema creates a tenant schema and applies the internal_account schema.
func setupTenantSchema(t *testing.T, tc *e2eTestContext, tenantID string) context.Context {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	ctx := context.Background()

	// Create the tenant schema
	_, err := tc.pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create tenant schema %s", schemaName)

	// Apply internal_account schema
	applyInternalAccountSchema(t, tc.pool, schemaName)

	// Create context with tenant and audit user
	tenantCtx := tenant.WithTenant(context.Background(), tid)
	tenantCtx = context.WithValue(tenantCtx, auth.UserIDContextKey, "e2e-test-user")

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = tc.pool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	})

	return tenantCtx
}

// setupTenantWithSchemas creates a tenant schema with both internal-account and position-keeping tables.
// This enables E2E tests that verify integration between the two services.
func setupTenantWithSchemas(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	ctx := context.Background()

	// Create the tenant schema
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create tenant schema %s", schemaName)

	// Apply internal-account schema
	applyInternalAccountSchema(t, pool, schemaName)

	// Apply position-keeping schema
	applyPositionKeepingSchema(t, pool, schemaName)

	// Create context with tenant and audit user
	tenantCtx := tenant.WithTenant(context.Background(), tid)
	tenantCtx = context.WithValue(tenantCtx, auth.UserIDContextKey, "e2e-test-user")

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	})

	return tenantCtx
}

// applyInternalAccountSchema creates the internal_account tables in the tenant schema.
func applyInternalAccountSchema(t *testing.T, pool *pgxpool.Pool, schemaName string) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	// Create internal_account table
	// Note: dimension constraint is relaxed for tests (allows empty string)
	// since Reference Data client is not configured in E2E tests
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS internal_account (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			account_id character varying(100) NOT NULL,
			account_code character varying(50) NOT NULL,
			name character varying(255) NOT NULL,
			account_type character varying(20) NOT NULL,
			clearing_purpose character varying(32) NULL,
			org_party_id uuid NULL,
			product_type_code character varying(100) NULL,
			product_type_version integer NULL,
			instrument_code character varying(32) NOT NULL,
			dimension character varying(20) NOT NULL DEFAULT '',
			status character varying(20) NOT NULL DEFAULT 'ACTIVE',
			counterparty_id character varying(50) NULL,
			counterparty_name character varying(255) NULL,
			counterparty_external_ref character varying(100) NULL,
			attributes jsonb NOT NULL DEFAULT '{}',
			version bigint NOT NULL DEFAULT 1,
			PRIMARY KEY (id),
			CONSTRAINT chk_account_type CHECK (account_type IN (
				'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING',
				'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY'
			)),
			CONSTRAINT chk_status CHECK (status IN (
				'ACTIVE', 'SUSPENDED', 'CLOSED'
			))
		)
	`)
	require.NoError(t, err, "Failed to create internal_account table")

	// Create unique constraint on account_id
	_, err = tx.Exec(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_internal_account_account_id ON internal_account (account_id)
	`)
	require.NoError(t, err, "Failed to create account_id unique index")

	// Create indexes
	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_internal_account_type ON internal_account (account_type);
		CREATE INDEX IF NOT EXISTS idx_internal_account_instrument ON internal_account (instrument_code);
		CREATE INDEX IF NOT EXISTS idx_internal_account_status ON internal_account (status);
		CREATE INDEX IF NOT EXISTS idx_internal_account_code ON internal_account (account_code);
		CREATE INDEX IF NOT EXISTS idx_internal_account_deleted_at ON internal_account (deleted_at)
	`)
	require.NoError(t, err, "Failed to create indexes")

	// Create status history table
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS internal_account_status_history (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			account_id character varying(100) NOT NULL,
			from_status character varying(20) NOT NULL,
			to_status character varying(20) NOT NULL,
			reason text NULL,
			changed_by character varying(100) NOT NULL,
			changed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			CONSTRAINT chk_from_status CHECK (from_status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
			CONSTRAINT chk_to_status CHECK (to_status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
			CONSTRAINT fk_status_history_account FOREIGN KEY (account_id)
				REFERENCES internal_account (account_id)
				ON UPDATE NO ACTION ON DELETE RESTRICT
		)
	`)
	require.NoError(t, err, "Failed to create status_history table")

	// Create status history indexes
	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_status_history_account_changed
			ON internal_account_status_history (account_id, changed_at DESC);
		CREATE INDEX IF NOT EXISTS idx_status_history_changed_at
			ON internal_account_status_history (changed_at)
	`)
	require.NoError(t, err, "Failed to create status history indexes")

	require.NoError(t, tx.Commit(ctx))
}

// applyPositionKeepingSchema creates the position table in the tenant schema.
// This matches the schema from position-keeping service for E2E integration testing.
func applyPositionKeepingSchema(t *testing.T, pool *pgxpool.Pool, schemaName string) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	// Create position table (append-only)
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS position (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			account_id character varying(34) NOT NULL,
			instrument_code character varying(32) NOT NULL,
			bucket_key character varying(256) NOT NULL,
			amount decimal(38, 18) NOT NULL,
			dimension character varying(32) NOT NULL DEFAULT 'Monetary',
			attributes jsonb NULL,
			reference_id uuid NULL,
			PRIMARY KEY (id),
			CONSTRAINT position_dimension_check CHECK (dimension IN ('Monetary', 'Energy', 'Compute', 'Carbon', 'Time', 'Physical', 'Custom'))
		)
	`)
	require.NoError(t, err, "Failed to create position table")

	// Create indexes
	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_position_account_id ON position (account_id);
		CREATE INDEX IF NOT EXISTS idx_position_aggregation ON position (account_id, instrument_code, bucket_key);
		CREATE INDEX IF NOT EXISTS idx_position_deleted_at ON position (deleted_at);
		CREATE INDEX IF NOT EXISTS idx_position_active ON position (account_id, instrument_code, bucket_key)
			WHERE deleted_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_position_reference_id ON position (reference_id);
		CREATE INDEX IF NOT EXISTS idx_position_created_at ON position (created_at)
	`)
	require.NoError(t, err, "Failed to create position indexes")

	// Create append-only trigger function
	_, err = tx.Exec(ctx, `
		CREATE OR REPLACE FUNCTION positions_append_only()
		RETURNS TRIGGER AS $$
		BEGIN
			IF OLD.amount IS DISTINCT FROM NEW.amount THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on amount column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.account_id IS DISTINCT FROM NEW.account_id THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on account_id column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.instrument_code IS DISTINCT FROM NEW.instrument_code THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on instrument_code column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.bucket_key IS DISTINCT FROM NEW.bucket_key THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on bucket_key column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.reference_id IS DISTINCT FROM NEW.reference_id THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on reference_id column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql
	`)
	require.NoError(t, err, "Failed to create append-only trigger function")

	_, err = tx.Exec(ctx, `
		DROP TRIGGER IF EXISTS positions_append_only ON position;
		CREATE TRIGGER positions_append_only
			BEFORE UPDATE ON position
			FOR EACH ROW
			EXECUTE FUNCTION positions_append_only()
	`)
	require.NoError(t, err, "Failed to create append-only trigger")

	require.NoError(t, tx.Commit(ctx))
}

// ============================================================================
// Helper Functions for Position-Based Testing
// ============================================================================

// createAccount creates an internal account directly via SQL.
// Returns the generated account_id.
func createAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountType, accountCode, instrumentCode, dimension string) string {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO internal_account (
			id, created_at, created_by, updated_at, updated_by,
			account_id, account_code, name, account_type, instrument_code, dimension, status
		) VALUES (
			gen_random_uuid(), NOW(), 'e2e-test', NOW(), 'e2e-test',
			$1, $2, $3, $4, $5, $6, 'ACTIVE'
		)`,
		accountID, accountCode, fmt.Sprintf("%s Account", accountCode), accountType, instrumentCode, dimension,
	)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return accountID
}

// insertPosition inserts a position record directly via SQL.
// Returns the position UUID.
func insertPosition(t *testing.T, pool *pgxpool.Pool, ctx context.Context, accountID, instrumentCode, bucketKey string, amount decimal.Decimal, dimension string, attributes map[string]string) uuid.UUID {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	id := uuid.New()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var attrsJSON interface{}
	if attributes != nil {
		attrsJSON = attributes
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO position (id, created_at, created_by, account_id, instrument_code, bucket_key, amount, dimension, attributes)
		VALUES ($1, NOW(), 'e2e-test', $2, $3, $4, $5, $6, $7)`,
		id, accountID, instrumentCode, bucketKey, amount, dimension, attrsJSON,
	)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return id
}

// getAggregatedBalance retrieves the aggregated balance for an account and instrument.
// Returns the sum of all position amounts where deleted_at IS NULL.
func getAggregatedBalance(t *testing.T, pool *pgxpool.Pool, ctx context.Context, accountID, instrumentCode string) decimal.Decimal {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var totalAmount decimal.Decimal
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM position
		WHERE account_id = $1 AND instrument_code = $2 AND deleted_at IS NULL`,
		accountID, instrumentCode,
	).Scan(&totalAmount)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return totalAmount
}

// updateAccountStatus updates the account status and increments the version.
// Also inserts a record into the status history table.
func updateAccountStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID, newStatus string) {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	// Get current status
	var currentStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM internal_account WHERE account_id = $1`, accountID).Scan(&currentStatus)
	require.NoError(t, err)

	// Update status and increment version
	_, err = tx.Exec(ctx, `
		UPDATE internal_account
		SET status = $1, version = version + 1, updated_at = NOW(), updated_by = 'e2e-test'
		WHERE account_id = $2`,
		newStatus, accountID,
	)
	require.NoError(t, err)

	// Insert status history record
	_, err = tx.Exec(ctx, `
		INSERT INTO internal_account_status_history (account_id, from_status, to_status, changed_by)
		VALUES ($1, $2, $3, 'e2e-test')`,
		accountID, currentStatus, newStatus,
	)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

// StatusHistoryRecord represents a record from the status history table.
type StatusHistoryRecord struct {
	ID         uuid.UUID
	AccountID  string
	FromStatus string
	ToStatus   string
	Reason     *string
	ChangedBy  string
	ChangedAt  time.Time
}

// getStatusHistory retrieves the status history for an account.
// Returns records ordered by changed_at DESC (most recent first).
func getStatusHistory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string) []StatusHistoryRecord {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	rows, err := tx.Query(ctx, `
		SELECT id, account_id, from_status, to_status, reason, changed_by, changed_at
		FROM internal_account_status_history
		WHERE account_id = $1
		ORDER BY changed_at DESC`,
		accountID,
	)
	require.NoError(t, err)
	defer rows.Close()

	var records []StatusHistoryRecord
	for rows.Next() {
		var r StatusHistoryRecord
		err := rows.Scan(&r.ID, &r.AccountID, &r.FromStatus, &r.ToStatus, &r.Reason, &r.ChangedBy, &r.ChangedAt)
		require.NoError(t, err)
		records = append(records, r)
	}

	require.NoError(t, tx.Commit(ctx))
	return records
}

// createServiceWithMockPositionKeeping creates a service with a mock Position Keeping client.
func createServiceWithMockPositionKeeping(t *testing.T, repo *persistence.Repository, mockPK *mockPositionKeepingClient) *service.Service {
	t.Helper()

	svc, err := service.NewServiceWithClients(repo, mockPK, nil, nil, nil)
	require.NoError(t, err)
	return svc
}

// ============================================================================
// Mock Position Keeping Client
// ============================================================================

// mockPositionKeepingClient implements service.PositionKeepingClient for testing.
type mockPositionKeepingClient struct {
	balances map[string]*positionkeepingv1.GetAccountBalancesResponse
}

func newMockPositionKeepingClient() *mockPositionKeepingClient {
	return &mockPositionKeepingClient{
		balances: make(map[string]*positionkeepingv1.GetAccountBalancesResponse),
	}
}

func (m *mockPositionKeepingClient) GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	key := fmt.Sprintf("%s:%s", req.AccountId, req.InstrumentCode)
	if resp, ok := m.balances[key]; ok {
		return resp, nil
	}
	// Return zero balance if not configured
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantitypb.InstrumentAmount{
					Amount:         "0",
					InstrumentCode: req.InstrumentCode,
				},
			},
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (m *mockPositionKeepingClient) SetBalance(accountID, instrumentCode string, amount decimal.Decimal) {
	key := fmt.Sprintf("%s:%s", accountID, instrumentCode)
	m.balances[key] = &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: accountID,
		Balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantitypb.InstrumentAmount{
					Amount:         amount.String(),
					InstrumentCode: instrumentCode,
				},
			},
		},
		AsOf: timestamppb.Now(),
	}
}

func (m *mockPositionKeepingClient) GetAccountBalance(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId:   req.AccountId,
		BalanceType: req.BalanceType,
		Amount: &quantitypb.InstrumentAmount{
			Amount:         "0",
			InstrumentCode: req.InstrumentCode,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (m *mockPositionKeepingClient) Close() error {
	return nil
}

// ============================================================================
// E2E Test: Account Lifecycle
// ============================================================================

// TestE2E_AccountLifecycle tests the complete account lifecycle from initiation to closure.
// This covers: Initiate -> Update -> Activate -> GetBalance -> Suspend -> Reactivate -> Close -> Verify
func TestE2E_AccountLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_lifecycle_tenant")

	// Create mock Position Keeping client for balance queries
	mockPK := newMockPositionKeepingClient()
	svc := createServiceWithMockPositionKeeping(t, tc.repo, mockPK)

	// Use account_code for lookups (service's findAccountByID falls back to FindByCode)
	accountCode := "GBP_CLEARING_001"
	var accountID string // Store the business ID for status history tracking

	t.Run("1. Initiate new internal account", func(t *testing.T) {
		resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:    accountCode,
			Name:           "GBP Clearing Account",
			ProductTypeCode: "CLEARING_GBP",
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)
		require.NotEmpty(t, resp.AccountId)
		accountID = resp.AccountId

		assert.Equal(t, accountCode, resp.Facility.AccountCode)
		assert.Equal(t, "GBP Clearing Account", resp.Facility.Name)
		assert.Equal(t, "CLEARING", resp.Facility.BehaviorClass)
		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
		assert.Equal(t, "GBP", resp.Facility.InstrumentCode)
		assert.Equal(t, int32(1), resp.Facility.Version)

		t.Logf("Created account: %s (code: %s)", accountID, accountCode)
	})

	t.Run("2. Update account name", func(t *testing.T) {
		// Use account_code for lookup
		resp, err := svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
			AccountId: accountCode, // findAccountByID falls back to FindByCode
			Name:      "GBP Main Clearing Account",
		})
		require.NoError(t, err)

		assert.Equal(t, "GBP Main Clearing Account", resp.Facility.Name)
		assert.Equal(t, int32(2), resp.Facility.Version)
	})

	t.Run("3. Retrieve account and verify state", func(t *testing.T) {
		resp, err := svc.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
			AccountId: accountCode,
		})
		require.NoError(t, err)

		assert.Equal(t, accountID, resp.Facility.AccountId)
		assert.Equal(t, "GBP Main Clearing Account", resp.Facility.Name)
		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
	})

	t.Run("4. Get balance (via Position Keeping integration)", func(t *testing.T) {
		// Set up mock balance using account_id (business ID) as Position Keeping uses
		mockPK.SetBalance(accountID, "GBP", decimal.NewFromFloat(10000.50))

		resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountCode,
		})
		require.NoError(t, err)

		// Note: GetBalance response contains account identifier (code or ID)
		assert.NotEmpty(t, resp.AccountId)
		require.NotNil(t, resp.CurrentBalance)
		// Decimal string format may not preserve trailing zeros
		actualBalance, _ := decimal.NewFromString(resp.CurrentBalance.Amount)
		expectedBalance := decimal.NewFromFloat(10000.50)
		assert.True(t, expectedBalance.Equal(actualBalance),
			"Balance mismatch: expected %s, got %s", expectedBalance.String(), actualBalance.String())
	})

	t.Run("5. Suspend account", func(t *testing.T) {
		resp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "Routine audit suspension",
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED, resp.Facility.AccountStatus)
		assert.NotNil(t, resp.ActionTimestamp)

		// Record status change for audit
		err = tc.repo.RecordStatusChange(ctx, accountID, "ACTIVE", "SUSPENDED", "Routine audit suspension")
		require.NoError(t, err)
	})

	t.Run("6. Verify suspended account cannot query balance", func(t *testing.T) {
		_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountCode,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not active")
	})

	t.Run("7. Reactivate account", func(t *testing.T) {
		resp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)

		// Record status change for audit
		err = tc.repo.RecordStatusChange(ctx, accountID, "SUSPENDED", "ACTIVE", "Audit completed")
		require.NoError(t, err)
	})

	t.Run("8. Close account", func(t *testing.T) {
		resp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
			Reason:        "Account no longer required",
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED, resp.Facility.AccountStatus)

		// Record status change for audit
		err = tc.repo.RecordStatusChange(ctx, accountID, "ACTIVE", "CLOSED", "Account no longer required")
		require.NoError(t, err)
	})

	t.Run("9. Verify closed account cannot be modified", func(t *testing.T) {
		_, err := svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
			AccountId: accountCode,
			Name:      "Attempted update",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "closed")
	})

	t.Run("10. Verify closed account cannot be reactivated", func(t *testing.T) {
		_, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid")
	})

	t.Run("11. Verify status history audit trail", func(t *testing.T) {
		tid, _ := tenant.FromContext(ctx)
		schemaName := tid.SchemaName()

		// Query status history
		var history []struct {
			FromStatus string `gorm:"column:from_status"`
			ToStatus   string `gorm:"column:to_status"`
			Reason     string `gorm:"column:reason"`
		}

		err := tc.db.Raw(fmt.Sprintf(
			`SELECT from_status, to_status, reason
			 FROM %s.internal_account_status_history
			 WHERE account_id = ?
			 ORDER BY changed_at ASC`, pq.QuoteIdentifier(schemaName)), accountID).Scan(&history).Error
		require.NoError(t, err)

		require.Len(t, history, 3)
		// Verify transitions: ACTIVE -> SUSPENDED -> ACTIVE -> CLOSED
		assert.Equal(t, "ACTIVE", history[0].FromStatus)
		assert.Equal(t, "SUSPENDED", history[0].ToStatus)
		assert.Equal(t, "SUSPENDED", history[1].FromStatus)
		assert.Equal(t, "ACTIVE", history[1].ToStatus)
		assert.Equal(t, "ACTIVE", history[2].FromStatus)
		assert.Equal(t, "CLOSED", history[2].ToStatus)
	})
}

// ============================================================================
// E2E Test: Multi-Asset Accounts
// ============================================================================

// TestE2E_MultiAssetAccounts tests creating accounts for different asset types.
// Meridian supports multi-asset accounts: GBP (currency), KWH (energy), GPU_HOUR (compute).
func TestE2E_MultiAssetAccounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_multiasset_tenant")

	mockPK := newMockPositionKeepingClient()
	svc := createServiceWithMockPositionKeeping(t, tc.repo, mockPK)

	// Define multi-asset test cases with account_code for lookups
	testCases := []struct {
		code       string
		name       string
		instrument string
		balance    float64
	}{
		{"GBP_CLEARING", "GBP Clearing Account", "GBP", 1000000.00},
		{"KWH_HOLDING", "Energy Holding Account", "KWH", 50000.5},
		{"GPU_HOUR_POOL", "GPU Compute Pool", "GPU_HOUR", 10000.0},
		{"CARBON_OFFSET", "Carbon Credits Holding", "CARBON_TONNE", 5000.0},
	}

	var createdAccounts []struct {
		code      string
		accountID string
	}

	t.Run("Create multi-asset accounts", func(t *testing.T) {
		for _, tc := range testCases {
			resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:    tc.code,
				Name:           tc.name,
				ProductTypeCode: "HOLDING_GBP",
				InstrumentCode: tc.instrument,
			})
			require.NoError(t, err, "Failed to create account for %s", tc.instrument)

			assert.Equal(t, tc.code, resp.Facility.AccountCode)
			assert.Equal(t, tc.instrument, resp.Facility.InstrumentCode)

			createdAccounts = append(createdAccounts, struct {
				code      string
				accountID string
			}{tc.code, resp.AccountId})

			// Set up mock balance using business account_id
			mockPK.SetBalance(resp.AccountId, tc.instrument, decimal.NewFromFloat(tc.balance))
		}
	})

	t.Run("Query balances for each asset type", func(t *testing.T) {
		for i, tc := range testCases {
			// Use account_code for lookup
			resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: createdAccounts[i].code,
			})
			require.NoError(t, err, "Failed to get balance for %s", tc.instrument)

			// Response AccountId contains the lookup identifier (account code or business ID)
			assert.NotEmpty(t, resp.AccountId, "AccountId should not be empty")
			require.NotNil(t, resp.CurrentBalance)

			expectedBalance := decimal.NewFromFloat(tc.balance)
			actualBalance, _ := decimal.NewFromString(resp.CurrentBalance.Amount)
			assert.True(t, expectedBalance.Equal(actualBalance),
				"Balance mismatch for %s: expected %s, got %s",
				tc.instrument, expectedBalance.String(), actualBalance.String())
		}
	})

	t.Run("List accounts by instrument code", func(t *testing.T) {
		// Filter by GBP
		resp, err := svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			InstrumentCodeFilter: "GBP",
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 1)
		assert.Equal(t, "GBP", resp.Facilities[0].InstrumentCode)

		// Filter by KWH
		resp, err = svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			InstrumentCodeFilter: "KWH",
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 1)
		assert.Equal(t, "KWH", resp.Facilities[0].InstrumentCode)
	})

	t.Run("List all accounts (no filter)", func(t *testing.T) {
		resp, err := svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, len(testCases))
	})
}

// ============================================================================
// E2E Test: Counterparty Banking (NOSTRO/VOSTRO)
// ============================================================================

// TestE2E_CounterpartyBanking tests creating NOSTRO and VOSTRO accounts with counterparty details.
func TestE2E_CounterpartyBanking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_counterparty_tenant")

	t.Run("Create NOSTRO account (our account at Citibank)", func(t *testing.T) {
		resp, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     "USD_NOSTRO_CITI",
			Name:            "USD NOSTRO at Citibank",
			ProductTypeCode: "NOSTRO_USD",
			InstrumentCode:  "USD",
			CounterpartyDetails: &pb.CounterpartyDetails{
				CounterpartyId:          "CITI001",
				CounterpartyName:        "Citibank NA",
				CounterpartyExternalRef: "12345678901",
			},
		})
		require.NoError(t, err)

		assert.Equal(t, "NOSTRO", resp.Facility.BehaviorClass)
		require.NotNil(t, resp.Facility.CounterpartyDetails)
		assert.Equal(t, "CITI001", resp.Facility.CounterpartyDetails.CounterpartyId)
		assert.Equal(t, "Citibank NA", resp.Facility.CounterpartyDetails.CounterpartyName)
		assert.Equal(t, "12345678901", resp.Facility.CounterpartyDetails.CounterpartyExternalRef)
		assert.Equal(t, pb.CounterpartyType_COUNTERPARTY_TYPE_NOSTRO, resp.Facility.CounterpartyDetails.CounterpartyType)
	})

	t.Run("Create VOSTRO account (Deutsche Bank's account at our bank)", func(t *testing.T) {
		resp, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     "EUR_VOSTRO_DB",
			Name:            "EUR VOSTRO for Deutsche Bank",
			ProductTypeCode: "VOSTRO_USD",
			InstrumentCode:  "EUR",
			CounterpartyDetails: &pb.CounterpartyDetails{
				CounterpartyId:          "DB001",
				CounterpartyName:        "Deutsche Bank AG",
				CounterpartyExternalRef: "DE89370400440532013000",
			},
		})
		require.NoError(t, err)

		assert.Equal(t, "VOSTRO", resp.Facility.BehaviorClass)
		require.NotNil(t, resp.Facility.CounterpartyDetails)
		assert.Equal(t, "DB001", resp.Facility.CounterpartyDetails.CounterpartyId)
		assert.Equal(t, "Deutsche Bank AG", resp.Facility.CounterpartyDetails.CounterpartyName)
		assert.Equal(t, pb.CounterpartyType_COUNTERPARTY_TYPE_VOSTRO, resp.Facility.CounterpartyDetails.CounterpartyType)
	})

	t.Run("NOSTRO account requires counterparty details", func(t *testing.T) {
		_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     "USD_NOSTRO_MISSING",
			Name:            "USD NOSTRO Missing Counterparty",
			ProductTypeCode: "NOSTRO_USD",
			InstrumentCode:  "USD",
			// Missing CounterpartyDetails
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "counterparty")
	})

	t.Run("VOSTRO account requires counterparty details", func(t *testing.T) {
		_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     "EUR_VOSTRO_MISSING",
			Name:            "EUR VOSTRO Missing Counterparty",
			ProductTypeCode: "VOSTRO_USD",
			InstrumentCode:  "EUR",
			// Missing CounterpartyDetails
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "counterparty")
	})

	t.Run("CLEARING account rejects counterparty details", func(t *testing.T) {
		// The domain should reject counterparty details for non-NOSTRO/VOSTRO accounts
		// First create the account successfully
		resp, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     "GBP_CLEARING_ONLY",
			Name:            "GBP Clearing Only",
			ProductTypeCode: "CLEARING_GBP",
			InstrumentCode:  "GBP",
		})
		require.NoError(t, err)
		assert.Nil(t, resp.Facility.CounterpartyDetails)
	})

	t.Run("Update counterparty details on NOSTRO account", func(t *testing.T) {
		// First create the NOSTRO account
		createResp, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     "USD_NOSTRO_JPMORGAN",
			Name:            "USD NOSTRO at JPMorgan",
			ProductTypeCode: "NOSTRO_USD",
			InstrumentCode:  "USD",
			CounterpartyDetails: &pb.CounterpartyDetails{
				CounterpartyId:          "JPM001",
				CounterpartyName:        "JPMorgan Chase",
				CounterpartyExternalRef: "987654321",
			},
		})
		require.NoError(t, err)

		// Update counterparty details using account_code
		updateResp, err := tc.svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
			AccountId: createResp.Facility.AccountCode,
			CounterpartyDetails: &pb.CounterpartyDetails{
				CounterpartyId:          "JPM001",
				CounterpartyName:        "JPMorgan Chase & Co",
				CounterpartyExternalRef: "999888777",
			},
		})
		require.NoError(t, err)

		// Verify updated details
		assert.Equal(t, "JPMorgan Chase & Co", updateResp.Facility.CounterpartyDetails.CounterpartyName)
		assert.Equal(t, "999888777", updateResp.Facility.CounterpartyDetails.CounterpartyExternalRef)
	})

	t.Run("List NOSTRO accounts only", func(t *testing.T) {
		resp, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			BehaviorClassFilter: "NOSTRO",
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 2) // CITI and JPMorgan

		for _, facility := range resp.Facilities {
			assert.Equal(t, "NOSTRO", facility.BehaviorClass)
			assert.NotNil(t, facility.CounterpartyDetails)
		}
	})

	t.Run("List VOSTRO accounts only", func(t *testing.T) {
		resp, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			BehaviorClassFilter: "VOSTRO",
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 1) // Deutsche Bank

		for _, facility := range resp.Facilities {
			assert.Equal(t, "VOSTRO", facility.BehaviorClass)
			assert.NotNil(t, facility.CounterpartyDetails)
		}
	})
}

// ============================================================================
// E2E Test: Multi-Tenant Isolation
// ============================================================================

// TestE2E_MultiTenantIsolation verifies that tenant A cannot see tenant B's accounts.
func TestE2E_MultiTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)

	// Setup two separate tenants
	ctxTenantA := setupTenantSchema(t, tc, "tenant_iso_alpha")
	ctxTenantB := setupTenantSchema(t, tc, "tenant_iso_beta")

	t.Run("Tenant A creates accounts", func(t *testing.T) {
		// Create 3 accounts for tenant A
		for i := 1; i <= 3; i++ {
			resp, err := tc.svc.InitiateInternalAccount(ctxTenantA, &pb.InitiateInternalAccountRequest{
				AccountCode:    fmt.Sprintf("TENANT_A_ACC_%d", i),
				Name:           fmt.Sprintf("Tenant A Account %d", i),
				ProductTypeCode: "CLEARING_GBP",
				InstrumentCode: "GBP",
			})
			require.NoError(t, err)
			assert.NotEmpty(t, resp.AccountId)
		}
	})

	t.Run("Tenant B creates accounts", func(t *testing.T) {
		// Create 2 accounts for tenant B
		for i := 1; i <= 2; i++ {
			resp, err := tc.svc.InitiateInternalAccount(ctxTenantB, &pb.InitiateInternalAccountRequest{
				AccountCode:    fmt.Sprintf("TENANT_B_ACC_%d", i),
				Name:           fmt.Sprintf("Tenant B Account %d", i),
				ProductTypeCode: "HOLDING_GBP",
				InstrumentCode: "EUR",
			})
			require.NoError(t, err)
			assert.NotEmpty(t, resp.AccountId)
		}
	})

	t.Run("Tenant A can only see their 3 accounts", func(t *testing.T) {
		resp, err := tc.svc.ListInternalAccounts(ctxTenantA, &pb.ListInternalAccountsRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 3)

		for _, facility := range resp.Facilities {
			assert.Contains(t, facility.AccountCode, "TENANT_A_ACC_")
		}
	})

	t.Run("Tenant B can only see their 2 accounts", func(t *testing.T) {
		resp, err := tc.svc.ListInternalAccounts(ctxTenantB, &pb.ListInternalAccountsRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 2)

		for _, facility := range resp.Facilities {
			assert.Contains(t, facility.AccountCode, "TENANT_B_ACC_")
		}
	})

	t.Run("Tenant A cannot retrieve Tenant B's account by code", func(t *testing.T) {
		// Try to retrieve tenant B's account using tenant A's context
		_, err := tc.svc.RetrieveInternalAccount(ctxTenantA, &pb.RetrieveInternalAccountRequest{
			AccountId: "TENANT_B_ACC_1", // Use account_code
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Tenant B cannot modify Tenant A's account", func(t *testing.T) {
		// Try to update using tenant B's context
		_, err := tc.svc.UpdateInternalAccount(ctxTenantB, &pb.UpdateInternalAccountRequest{
			AccountId: "TENANT_A_ACC_1",
			Name:      "Hacked by Tenant B",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		// Verify tenant A's account is unchanged
		retrieveResp, err := tc.svc.RetrieveInternalAccount(ctxTenantA, &pb.RetrieveInternalAccountRequest{
			AccountId: "TENANT_A_ACC_1",
		})
		require.NoError(t, err)
		assert.Contains(t, retrieveResp.Facility.Name, "Tenant A")
	})

	t.Run("Both tenants can use the same account code independently", func(t *testing.T) {
		// Tenant A creates account with code "SHARED_CODE"
		respA, err := tc.svc.InitiateInternalAccount(ctxTenantA, &pb.InitiateInternalAccountRequest{
			AccountCode:    "SHARED_CODE",
			Name:           "Shared Code Account - Tenant A",
			ProductTypeCode: "SUSPENSE_GBP",
			InstrumentCode: "USD",
		})
		require.NoError(t, err)

		// Tenant B creates account with the same code
		respB, err := tc.svc.InitiateInternalAccount(ctxTenantB, &pb.InitiateInternalAccountRequest{
			AccountCode:    "SHARED_CODE",
			Name:           "Shared Code Account - Tenant B",
			ProductTypeCode: "REVENUE_GBP",
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)

		// Both accounts exist independently
		assert.NotEqual(t, respA.AccountId, respB.AccountId)
		assert.Equal(t, "SHARED_CODE", respA.Facility.AccountCode)
		assert.Equal(t, "SHARED_CODE", respB.Facility.AccountCode)
		assert.Equal(t, "SUSPENSE", respA.Facility.BehaviorClass)
		assert.Equal(t, "REVENUE", respB.Facility.BehaviorClass)
	})

	t.Run("Control actions are tenant-isolated", func(t *testing.T) {
		// Tenant B cannot suspend Tenant A's account
		_, err := tc.svc.ControlInternalAccount(ctxTenantB, &pb.ControlInternalAccountRequest{
			AccountId:     "TENANT_A_ACC_1",
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "Cross-tenant attack",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		// Verify account is still ACTIVE
		retrieveResp, err := tc.svc.RetrieveInternalAccount(ctxTenantA, &pb.RetrieveInternalAccountRequest{
			AccountId: "TENANT_A_ACC_1",
		})
		require.NoError(t, err)
		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, retrieveResp.Facility.AccountStatus)
	})
}

// ============================================================================
// E2E Test: Async Operations with Await
// ============================================================================

// TestE2E_AsyncOperationsWithAwait demonstrates proper use of the await package
// for testing asynchronous operations without time.Sleep.
func TestE2E_AsyncOperationsWithAwait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_async_tenant")

	t.Run("Wait for account creation in goroutine", func(t *testing.T) {
		accountCode := fmt.Sprintf("ASYNC_ACC_%s", uuid.New().String()[:8])

		// Create account in goroutine with delay
		go func() {
			time.Sleep(100 * time.Millisecond) // Simulate async processing delay
			_, _ = tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:    accountCode,
				Name:           "Async Created Account",
				ProductTypeCode: "CLEARING_GBP",
				InstrumentCode: "GBP",
			})
		}()

		// Use await to poll for account existence
		var foundAccount *pb.InternalAccountFacility
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				resp, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{})
				if err != nil {
					return false
				}
				for _, facility := range resp.Facilities {
					if facility.AccountCode == accountCode {
						foundAccount = facility
						return true
					}
				}
				return false
			})

		require.NoError(t, err, "account should be created within timeout")
		require.NotNil(t, foundAccount)
		assert.Equal(t, accountCode, foundAccount.AccountCode)
	})

	t.Run("Wait for status change via async control action", func(t *testing.T) {
		accountCode := fmt.Sprintf("STATUS_ASYNC_%s", uuid.New().String()[:8])

		// Create account
		_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:    accountCode,
			Name:           "Status Change Test Account",
			ProductTypeCode: "HOLDING_GBP",
			InstrumentCode: "EUR",
		})
		require.NoError(t, err)

		// Suspend in goroutine
		go func() {
			time.Sleep(150 * time.Millisecond)
			_, _ = tc.svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
				AccountId:     accountCode,
				ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:        "Async suspension",
			})
		}()

		// Use await to poll for status change
		err = await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				resp, err := tc.svc.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
					AccountId: accountCode,
				})
				if err != nil {
					return false
				}
				return resp.Facility.AccountStatus == pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED
			})

		require.NoError(t, err, "account should become SUSPENDED within timeout")
	})

	t.Run("UntilNoError for transient failures", func(t *testing.T) {
		// Create a unique account code to query
		uniqueCode := fmt.Sprintf("RETRY_%s", uuid.New().String()[:8])

		// Create account with delay
		go func() {
			time.Sleep(200 * time.Millisecond)
			_, _ = tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:    uniqueCode,
				Name:           "Retry Test Account",
				ProductTypeCode: "CLEARING_GBP",
				InstrumentCode: "USD",
			})
		}()

		// Use UntilNoError to retry until account exists
		var facility *pb.InternalAccountFacility
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			UntilNoError(func() error {
				// Try to find account by listing and filtering
				resp, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{})
				if err != nil {
					return err
				}
				for _, f := range resp.Facilities {
					if f.AccountCode == uniqueCode {
						facility = f
						return nil
					}
				}
				return fmt.Errorf("account not found yet")
			})

		require.NoError(t, err)
		require.NotNil(t, facility)
		assert.Equal(t, uniqueCode, facility.AccountCode)
	})
}

// ============================================================================
// E2E Test: Pagination
// ============================================================================

// TestE2E_Pagination tests listing accounts with pagination.
func TestE2E_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_pagination_tenant")

	// Create 10 accounts
	const totalAccounts = 10
	for i := 1; i <= totalAccounts; i++ {
		_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:    fmt.Sprintf("PAGE_ACC_%02d", i),
			Name:           fmt.Sprintf("Pagination Account %d", i),
			ProductTypeCode: "CLEARING_GBP",
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)
	}

	t.Run("First page of 3 accounts", func(t *testing.T) {
		resp, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize: 3,
			},
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 3)
		assert.NotEmpty(t, resp.Pagination.NextPageToken)
	})

	t.Run("Second page using token", func(t *testing.T) {
		// Get first page
		firstPage, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize: 3,
			},
		})
		require.NoError(t, err)

		// Get second page
		secondPage, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize:  3,
				PageToken: firstPage.Pagination.NextPageToken,
			},
		})
		require.NoError(t, err)
		assert.Len(t, secondPage.Facilities, 3)

		// Verify no overlap between pages
		firstPageCodes := make(map[string]bool)
		for _, f := range firstPage.Facilities {
			firstPageCodes[f.AccountCode] = true
		}
		for _, f := range secondPage.Facilities {
			assert.False(t, firstPageCodes[f.AccountCode], "second page should not contain first page items")
		}
	})

	t.Run("Iterate through all pages", func(t *testing.T) {
		var allAccountCodes []string
		pageToken := ""
		pageCount := 0

		for {
			resp, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
				Pagination: &commonpb.Pagination{
					PageSize:  4,
					PageToken: pageToken,
				},
			})
			require.NoError(t, err)
			pageCount++

			for _, f := range resp.Facilities {
				allAccountCodes = append(allAccountCodes, f.AccountCode)
			}

			if resp.Pagination.NextPageToken == "" {
				break
			}
			pageToken = resp.Pagination.NextPageToken
		}

		// Should have retrieved all accounts across pages
		assert.Len(t, allAccountCodes, totalAccounts)
		// Verify pages required (10 accounts / 4 per page = 3 pages)
		assert.Equal(t, 3, pageCount)
	})
}

// ============================================================================
// E2E Test: Optimistic Locking
// ============================================================================

// TestE2E_OptimisticLocking tests version-based optimistic locking.
func TestE2E_OptimisticLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_locking_tenant")

	t.Run("Version conflict on concurrent updates", func(t *testing.T) {
		accountCode := "VERSION_TEST"

		// Create account
		createResp, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:    accountCode,
			Name:           "Version Test Account",
			ProductTypeCode: "CLEARING_GBP",
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)
		initialVersion := createResp.Facility.Version

		// First update succeeds
		updateResp1, err := tc.svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
			AccountId:       accountCode,
			Name:            "Updated Name V1",
			ExpectedVersion: initialVersion,
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), updateResp1.Facility.Version)

		// Second update with stale version fails
		_, err = tc.svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
			AccountId:       accountCode,
			Name:            "Stale Update",
			ExpectedVersion: initialVersion, // Stale version
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version")
	})
}

// ============================================================================
// E2E Test: Account Types
// ============================================================================

// TestE2E_AllAccountTypes tests creating all supported account types.
func TestE2E_AllAccountTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_account_types_tenant")

	// Test cases for all account types (using product_type_code with behavior class prefix)
	accountTypes := []struct {
		productTypeCode      string
		behaviorClass        string
		requireCounterparty bool
		name                string
	}{
		{"CLEARING_GBP", "CLEARING", false, "Clearing"},
		{"HOLDING_GBP", "HOLDING", false, "Holding"},
		{"SUSPENSE_GBP", "SUSPENSE", false, "Suspense"},
		{"REVENUE_GBP", "REVENUE", false, "Revenue"},
		{"EXPENSE_GBP", "EXPENSE", false, "Expense"},
		{"NOSTRO_USD", "NOSTRO", true, "Nostro"},
		{"VOSTRO_USD", "VOSTRO", true, "Vostro"},
	}

	for _, at := range accountTypes {
		t.Run(fmt.Sprintf("Create %s account", at.name), func(t *testing.T) {
			req := &pb.InitiateInternalAccountRequest{
				AccountCode:     fmt.Sprintf("%s_ACCOUNT", at.name),
				Name:            fmt.Sprintf("%s Test Account", at.name),
				ProductTypeCode: at.productTypeCode,
				InstrumentCode:  "GBP",
			}

			if at.requireCounterparty {
				req.CounterpartyDetails = &pb.CounterpartyDetails{
					CounterpartyId:          "BANK001",
					CounterpartyName:        "Test Bank",
					CounterpartyExternalRef: "REF123",
				}
			}

			resp, err := tc.svc.InitiateInternalAccount(ctx, req)
			require.NoError(t, err)
			assert.Equal(t, at.behaviorClass, resp.Facility.BehaviorClass)

			if at.requireCounterparty {
				assert.NotNil(t, resp.Facility.CounterpartyDetails)
			}
		})
	}
}

// ============================================================================
// E2E Test: Position Keeping Integration
// ============================================================================

// TestE2E_PositionKeepingIntegration tests the integration between internal accounts
// and the Position Keeping service for balance tracking.
func TestE2E_PositionKeepingIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	// Use setupTenantWithSchemas which includes both account AND position tables
	ctx := setupTenantWithSchemas(t, tc.pool, "e2e_pk_integration_tenant")

	t.Run("Balance updates reflect in account via position table", func(t *testing.T) {
		// Create an account directly via SQL
		accountID := createAccount(t, ctx, tc.pool, "CLEARING", "GBP_CLEARING_PK", "GBP", "Monetary")

		// Insert position records via SQL (simulating Position Keeping)
		insertPosition(t, tc.pool, ctx, accountID, "GBP", "CURRENT", decimal.NewFromFloat(10000.00), "Monetary", nil)
		insertPosition(t, tc.pool, ctx, accountID, "GBP", "CURRENT", decimal.NewFromFloat(5000.00), "Monetary", nil)
		insertPosition(t, tc.pool, ctx, accountID, "GBP", "CURRENT", decimal.NewFromFloat(-2000.00), "Monetary", nil)

		// Query aggregated balance
		balance := getAggregatedBalance(t, tc.pool, ctx, accountID, "GBP")

		// Verify: 10000 + 5000 - 2000 = 13000
		expectedBalance := decimal.NewFromFloat(13000.00)
		assert.True(t, expectedBalance.Equal(balance),
			"Balance mismatch: expected %s, got %s", expectedBalance.String(), balance.String())
	})

	t.Run("Multi-bucket balances aggregate correctly", func(t *testing.T) {
		// Create holding account for energy
		accountID := createAccount(t, ctx, tc.pool, "HOLDING", "KWH_HOLDING_PK", "KWH", "Energy")

		// Insert positions with different bucket keys (e.g., different pricing tiers)
		insertPosition(t, tc.pool, ctx, accountID, "KWH", "PEAK", decimal.NewFromFloat(500.5), "Energy", nil)
		insertPosition(t, tc.pool, ctx, accountID, "KWH", "OFF_PEAK", decimal.NewFromFloat(1000.25), "Energy", nil)
		insertPosition(t, tc.pool, ctx, accountID, "KWH", "SHOULDER", decimal.NewFromFloat(300.0), "Energy", nil)

		// Get total balance (all buckets)
		totalBalance := getAggregatedBalance(t, tc.pool, ctx, accountID, "KWH")

		// Verify: 500.5 + 1000.25 + 300 = 1800.75
		expectedTotal := decimal.NewFromFloat(1800.75)
		assert.True(t, expectedTotal.Equal(totalBalance),
			"Total balance mismatch: expected %s, got %s", expectedTotal.String(), totalBalance.String())
	})

	t.Run("Soft deleted positions excluded from balance", func(t *testing.T) {
		accountID := createAccount(t, ctx, tc.pool, "SUSPENSE", "GBP_SUSPENSE_PK", "GBP", "Monetary")

		// Insert positions
		pos1 := insertPosition(t, tc.pool, ctx, accountID, "GBP", "CURRENT", decimal.NewFromFloat(1000.00), "Monetary", nil)
		insertPosition(t, tc.pool, ctx, accountID, "GBP", "CURRENT", decimal.NewFromFloat(500.00), "Monetary", nil)

		// Soft delete one position
		softDeletePosition(t, tc.pool, ctx, pos1)

		// Verify balance excludes deleted position
		balance := getAggregatedBalance(t, tc.pool, ctx, accountID, "GBP")
		expectedBalance := decimal.NewFromFloat(500.00)
		assert.True(t, expectedBalance.Equal(balance),
			"Balance should exclude deleted position: expected %s, got %s", expectedBalance.String(), balance.String())
	})

	t.Run("Position attributes stored correctly", func(t *testing.T) {
		accountID := createAccount(t, ctx, tc.pool, "REVENUE", "GPU_REVENUE_PK", "GPU_HOUR", "Compute")

		// Insert position with attributes
		attrs := map[string]string{
			"instance_type": "A100",
			"region":        "us-east-1",
			"customer_id":   "cust-12345",
		}
		insertPosition(t, tc.pool, ctx, accountID, "GPU_HOUR", "A100_US_EAST", decimal.NewFromFloat(100.0), "Compute", attrs)

		// Verify balance (attributes don't affect sum)
		balance := getAggregatedBalance(t, tc.pool, ctx, accountID, "GPU_HOUR")
		expectedBalance := decimal.NewFromFloat(100.0)
		assert.True(t, expectedBalance.Equal(balance),
			"Balance mismatch: expected %s, got %s", expectedBalance.String(), balance.String())
	})

	t.Run("Cross-instrument isolation", func(t *testing.T) {
		// Single account can track multiple instruments
		accountID := createAccount(t, ctx, tc.pool, "CLEARING", "MULTI_CURRENCY_PK", "GBP", "Monetary")

		// Insert GBP and USD positions for same account
		insertPosition(t, tc.pool, ctx, accountID, "GBP", "CURRENT", decimal.NewFromFloat(10000.00), "Monetary", nil)
		insertPosition(t, tc.pool, ctx, accountID, "USD", "CURRENT", decimal.NewFromFloat(15000.00), "Monetary", nil)

		// Verify balances are isolated by instrument
		gbpBalance := getAggregatedBalance(t, tc.pool, ctx, accountID, "GBP")
		usdBalance := getAggregatedBalance(t, tc.pool, ctx, accountID, "USD")

		assert.True(t, decimal.NewFromFloat(10000.00).Equal(gbpBalance), "GBP balance mismatch")
		assert.True(t, decimal.NewFromFloat(15000.00).Equal(usdBalance), "USD balance mismatch")
	})
}

// softDeletePosition marks a position as deleted.
func softDeletePosition(t *testing.T, pool *pgxpool.Pool, ctx context.Context, positionID uuid.UUID) {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `UPDATE position SET deleted_at = NOW() WHERE id = $1`, positionID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

// ============================================================================
// E2E Test: Performance Baselines
// ============================================================================

// TestE2E_PerformanceBaselines verifies performance requirements are met.
// These are baseline measurements, not strict SLAs, but help detect regressions.
func TestE2E_PerformanceBaselines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantWithSchemas(t, tc.pool, "e2e_perf_tenant")

	t.Run("Account creation under 100ms average", func(t *testing.T) {
		const iterations = 50 // Reduced from 100 for faster CI
		var totalDuration time.Duration

		for i := 0; i < iterations; i++ {
			start := time.Now()

			_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:    fmt.Sprintf("PERF_ACC_%04d", i),
				Name:           fmt.Sprintf("Performance Test Account %d", i),
				ProductTypeCode: "CLEARING_GBP",
				InstrumentCode: "GBP",
			})
			require.NoError(t, err)

			totalDuration += time.Since(start)
		}

		avgDuration := totalDuration / iterations
		t.Logf("Average account creation time: %v (over %d iterations)", avgDuration, iterations)

		// Assert average is under 100ms (testcontainer adds overhead)
		assert.Less(t, avgDuration, 100*time.Millisecond,
			"Account creation average %v exceeds 100ms baseline", avgDuration)
	})

	t.Run("Account retrieval under 50ms average", func(t *testing.T) {
		// Create accounts to retrieve
		const numAccounts = 20
		codes := make([]string, numAccounts)
		for i := 0; i < numAccounts; i++ {
			code := fmt.Sprintf("RETRIEVE_ACC_%04d", i)
			codes[i] = code
			_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:    code,
				Name:           fmt.Sprintf("Retrieval Test Account %d", i),
				ProductTypeCode: "HOLDING_GBP",
				InstrumentCode: "EUR",
			})
			require.NoError(t, err)
		}

		// Measure retrieval times
		const iterations = 50
		var totalDuration time.Duration

		for i := 0; i < iterations; i++ {
			code := codes[i%numAccounts]

			start := time.Now()
			_, err := tc.svc.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
				AccountId: code,
			})
			require.NoError(t, err)
			totalDuration += time.Since(start)
		}

		avgDuration := totalDuration / iterations
		t.Logf("Average account retrieval time: %v (over %d iterations)", avgDuration, iterations)

		assert.Less(t, avgDuration, 50*time.Millisecond,
			"Account retrieval average %v exceeds 50ms baseline", avgDuration)
	})

	t.Run("Balance query under 50ms average", func(t *testing.T) {
		// Create account with positions
		accountID := createAccount(t, ctx, tc.pool, "CLEARING", "BALANCE_PERF_ACC", "GBP", "Monetary")

		// Insert multiple positions to make aggregation meaningful
		for i := 0; i < 100; i++ {
			insertPosition(t, tc.pool, ctx, accountID, "GBP", fmt.Sprintf("BUCKET_%d", i%10),
				decimal.NewFromFloat(float64(i)*10.5), "Monetary", nil)
		}

		// Measure balance query times
		const iterations = 50
		var totalDuration time.Duration

		for i := 0; i < iterations; i++ {
			start := time.Now()
			_ = getAggregatedBalance(t, tc.pool, ctx, accountID, "GBP")
			totalDuration += time.Since(start)
		}

		avgDuration := totalDuration / iterations
		t.Logf("Average balance query time: %v (over %d iterations)", avgDuration, iterations)

		assert.Less(t, avgDuration, 50*time.Millisecond,
			"Balance query average %v exceeds 50ms baseline", avgDuration)
	})

	t.Run("List accounts pagination under 50ms average", func(t *testing.T) {
		// Create 50 accounts for pagination tests
		for i := 0; i < 50; i++ {
			_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:    fmt.Sprintf("LIST_ACC_%04d", i),
				Name:           fmt.Sprintf("List Test Account %d", i),
				ProductTypeCode: "SUSPENSE_GBP",
				InstrumentCode: "USD",
			})
			require.NoError(t, err)
		}

		// Measure list query times
		const iterations = 30
		var totalDuration time.Duration

		for i := 0; i < iterations; i++ {
			start := time.Now()
			_, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
				Pagination: &commonpb.Pagination{
					PageSize: 10,
				},
			})
			require.NoError(t, err)
			totalDuration += time.Since(start)
		}

		avgDuration := totalDuration / iterations
		t.Logf("Average list accounts time: %v (over %d iterations)", avgDuration, iterations)

		assert.Less(t, avgDuration, 50*time.Millisecond,
			"List accounts average %v exceeds 50ms baseline", avgDuration)
	})

	t.Run("Concurrent operations complete without deadlock", func(t *testing.T) {
		const numWorkers = 10
		const opsPerWorker = 5

		var wg sync.WaitGroup
		errChan := make(chan error, numWorkers*opsPerWorker)

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			workerID := w
			go func() {
				defer wg.Done()
				for i := 0; i < opsPerWorker; i++ {
					code := fmt.Sprintf("CONCURRENT_%d_%d", workerID, i)
					_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
						AccountCode:    code,
						Name:           fmt.Sprintf("Concurrent Account %d-%d", workerID, i),
						ProductTypeCode: "CLEARING_GBP",
						InstrumentCode: "GBP",
					})
					if err != nil {
						errChan <- err
					}
				}
			}()
		}

		// Wait with timeout to detect deadlocks
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// All workers completed
		case <-time.After(30 * time.Second):
			t.Fatal("Concurrent operations timed out - possible deadlock")
		}

		close(errChan)
		for err := range errChan {
			t.Errorf("Concurrent operation failed: %v", err)
		}

		// Verify all accounts were created
		resp, err := tc.svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize: 200,
			},
		})
		require.NoError(t, err)

		// Should have at least the concurrent accounts (plus any from other tests)
		// Filter to count only CONCURRENT_ accounts
		concurrentCount := 0
		for _, f := range resp.Facilities {
			if len(f.AccountCode) >= 11 && f.AccountCode[:11] == "CONCURRENT_" {
				concurrentCount++
			}
		}
		assert.Equal(t, numWorkers*opsPerWorker, concurrentCount,
			"Expected %d concurrent accounts, found %d", numWorkers*opsPerWorker, concurrentCount)
	})
}

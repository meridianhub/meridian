package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const lienTestTenantID = "lien_test_tenant"

// setupLienTestDB initializes a CockroachDB testcontainer with both the
// internal_account and lien tables created in the tenant schema.
func setupLienTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&InternalAccountEntity{},
		&StatusHistoryEntity{},
		&LienEntity{},
	})

	tid := tenant.TenantID(lienTestTenantID)
	schemaName := tid.SchemaName()

	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// internal_account table (required for FK or just for co-existence)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.internal_account (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		created_by VARCHAR(100) NOT NULL DEFAULT '',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_by VARCHAR(100) NOT NULL DEFAULT '',
		deleted_at TIMESTAMPTZ,
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_code VARCHAR(50) NOT NULL,
		name VARCHAR(255) NOT NULL,
		account_type VARCHAR(20) NOT NULL,
		clearing_purpose VARCHAR(32) NULL,
		org_party_id UUID NULL,
		product_type_code VARCHAR(100) NULL,
		product_type_version INTEGER NULL,
		instrument_code VARCHAR(32) NOT NULL,
		dimension VARCHAR(20) NOT NULL DEFAULT '',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		counterparty_id VARCHAR(50),
		counterparty_name VARCHAR(255),
		counterparty_external_ref VARCHAR(100),
		attributes JSONB NOT NULL DEFAULT '{}',
		version BIGINT NOT NULL DEFAULT 1
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// lien table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.lien (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		instrument_code VARCHAR(32) NOT NULL,
		bucket_id VARCHAR(255) NOT NULL DEFAULT '',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		payment_order_reference VARCHAR(255) NOT NULL,
		termination_reason VARCHAR(1000) NOT NULL DEFAULT '',
		expires_at TIMESTAMPTZ,
		reserved_quantity JSONB,
		valued_amount JSONB,
		valuation_analysis JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		version INTEGER NOT NULL DEFAULT 1,
		CONSTRAINT idx_lien_payment_order UNIQUE (payment_order_reference)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)
	return db, ctx, cleanup
}

// newLien creates a minimal valid domain.Lien for testing.
func newLien(accountID uuid.UUID, ref string) *domain.Lien {
	lien, err := domain.NewLien(accountID, 1000, "GBP", "", ref, nil)
	if err != nil {
		panic(err)
	}
	return lien
}

// ---------------------------------------------------------------------------
// NewLienRepository + WithTx
// ---------------------------------------------------------------------------

func TestNewLienRepository(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()
	_ = ctx

	repo := NewLienRepository(db)
	require.NotNil(t, repo)
}

func TestLienRepository_WithTx(t *testing.T) {
	db, _, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	tx := db.Begin()
	defer tx.Rollback()

	txRepo := repo.WithTx(tx)
	require.NotNil(t, txRepo)
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestLienRepository_Create_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	lien := newLien(accountID, "PAY-001")

	err := repo.Create(ctx, lien)
	require.NoError(t, err)
}

func TestLienRepository_Create_DuplicatePaymentOrderReference(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	lien1 := newLien(accountID, "PAY-DUP-001")
	err := repo.Create(ctx, lien1)
	require.NoError(t, err)

	// Duplicate payment order ref should fail
	lien2 := newLien(accountID, "PAY-DUP-001")
	err = repo.Create(ctx, lien2)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// FindByID
// ---------------------------------------------------------------------------

func TestLienRepository_FindByID_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	lien := newLien(accountID, "PAY-FIND-001")

	err := repo.Create(ctx, lien)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)
	assert.Equal(t, lien.ID, found.ID)
	assert.Equal(t, lien.AmountCents, found.AmountCents)
	assert.Equal(t, domain.LienStatusActive, found.Status)
}

func TestLienRepository_FindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLienNotFound)
}

// ---------------------------------------------------------------------------
// FindByIDForUpdate
// ---------------------------------------------------------------------------

func TestLienRepository_FindByIDForUpdate_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	lien := newLien(accountID, "PAY-FORUPDATE-001")
	require.NoError(t, repo.Create(ctx, lien))

	found, err := repo.FindByIDForUpdate(ctx, lien.ID)
	require.NoError(t, err)
	assert.Equal(t, lien.ID, found.ID)
}

func TestLienRepository_FindByIDForUpdate_NotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	_, err := repo.FindByIDForUpdate(ctx, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLienNotFound)
}

// ---------------------------------------------------------------------------
// FindByAccountID
// ---------------------------------------------------------------------------

func TestLienRepository_FindByAccountID_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	lien1 := newLien(accountID, "PAY-ACCT-001")
	lien2 := newLien(accountID, "PAY-ACCT-002")
	require.NoError(t, repo.Create(ctx, lien1))
	require.NoError(t, repo.Create(ctx, lien2))

	// Different account — should not appear in results
	otherLien := newLien(uuid.New(), "PAY-ACCT-OTHER")
	require.NoError(t, repo.Create(ctx, otherLien))

	liens, err := repo.FindByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Len(t, liens, 2)
}

func TestLienRepository_FindByAccountID_Empty(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	liens, err := repo.FindByAccountID(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, liens)
}

// ---------------------------------------------------------------------------
// FindActiveByAccountID
// ---------------------------------------------------------------------------

func TestLienRepository_FindActiveByAccountID_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	activeLien := newLien(accountID, "PAY-ACTIVE-001")
	require.NoError(t, repo.Create(ctx, activeLien))

	// Create and terminate a second lien
	terminatedLien := newLien(accountID, "PAY-TERM-001")
	require.NoError(t, repo.Create(ctx, terminatedLien))
	terminatedLien.Status = domain.LienStatusTerminated
	require.NoError(t, repo.Update(ctx, terminatedLien))

	active, err := repo.FindActiveByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, activeLien.ID, active[0].ID)
}

func TestLienRepository_FindActiveByAccountID_WithExpiry(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Expired lien (expires in the past)
	past := time.Now().Add(-time.Hour)
	expiredLien, err := domain.NewLien(accountID, 500, "GBP", "", "PAY-EXPIRED-001", &past)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, expiredLien))

	// Active lien with no expiry
	activeLien := newLien(accountID, "PAY-NOEXPIRY-001")
	require.NoError(t, repo.Create(ctx, activeLien))

	active, err := repo.FindActiveByAccountID(ctx, accountID)
	require.NoError(t, err)
	// Only non-expired active liens
	assert.Len(t, active, 1)
	assert.Equal(t, activeLien.ID, active[0].ID)
}

// ---------------------------------------------------------------------------
// FindByPaymentOrderReference
// ---------------------------------------------------------------------------

func TestLienRepository_FindByPaymentOrderReference_Success(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	lien := newLien(accountID, "PAY-REF-001")
	require.NoError(t, repo.Create(ctx, lien))

	found, err := repo.FindByPaymentOrderReference(ctx, "PAY-REF-001")
	require.NoError(t, err)
	assert.Equal(t, lien.ID, found.ID)
}

func TestLienRepository_FindByPaymentOrderReference_NotFound(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)

	_, err := repo.FindByPaymentOrderReference(ctx, "NONEXISTENT-PAY-REF")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLienNotFound)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func TestLienRepository_Update_StatusTransition(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	lien := newLien(accountID, "PAY-UPDATE-001")
	require.NoError(t, repo.Create(ctx, lien))

	// Execute the lien
	lien.Status = domain.LienStatusExecuted
	err := repo.Update(ctx, lien)
	require.NoError(t, err)
	assert.Equal(t, 2, lien.Version) // version incremented

	// Verify persisted status
	found, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienStatusExecuted, found.Status)
}

func TestLienRepository_Update_VersionConflict(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	lien := newLien(accountID, "PAY-VER-001")
	require.NoError(t, repo.Create(ctx, lien))

	// Simulate stale version
	lien.Version = 999
	lien.Status = domain.LienStatusTerminated
	err := repo.Update(ctx, lien)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLienVersionConflict)
}

func TestLienRepository_Update_TerminationReason(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()
	lien := newLien(accountID, "PAY-REASON-001")
	require.NoError(t, repo.Create(ctx, lien))

	lien.Status = domain.LienStatusTerminated
	lien.TerminationReason = "customer cancelled"
	err := repo.Update(ctx, lien)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)
	assert.Equal(t, "customer cancelled", found.TerminationReason)
}

// ---------------------------------------------------------------------------
// CountActiveByAccountID
// ---------------------------------------------------------------------------

func TestLienRepository_CountActiveByAccountID(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// No liens
	count, err := repo.CountActiveByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// Add two active liens
	require.NoError(t, repo.Create(ctx, newLien(accountID, "PAY-COUNT-001")))
	require.NoError(t, repo.Create(ctx, newLien(accountID, "PAY-COUNT-002")))

	count, err = repo.CountActiveByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// Terminate one
	lien, err := repo.FindByPaymentOrderReference(ctx, "PAY-COUNT-001")
	require.NoError(t, err)
	lien.Status = domain.LienStatusTerminated
	require.NoError(t, repo.Update(ctx, lien))

	count, err = repo.CountActiveByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// ---------------------------------------------------------------------------
// SumActiveAmountByAccountID
// ---------------------------------------------------------------------------

func TestLienRepository_SumActiveAmountByAccountID(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Empty
	total, err := repo.SumActiveAmountByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// Add liens with different amounts
	lien1, err := domain.NewLien(accountID, 1000, "GBP", "", "PAY-SUM-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien1))

	lien2, err := domain.NewLien(accountID, 2500, "GBP", "", "PAY-SUM-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, lien2))

	total, err = repo.SumActiveAmountByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(3500), total)
}

// ---------------------------------------------------------------------------
// SumActiveAmountByAccountIDAndBucket
// ---------------------------------------------------------------------------

func TestLienRepository_SumActiveAmountByAccountIDAndBucket(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Add liens in different buckets
	bucketALien1, err := domain.NewLien(accountID, 1000, "GBP", "bucket-A", "PAY-BUCKET-A-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, bucketALien1))

	bucketALien2, err := domain.NewLien(accountID, 2000, "GBP", "bucket-A", "PAY-BUCKET-A-002", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, bucketALien2))

	bucketBLien, err := domain.NewLien(accountID, 5000, "GBP", "bucket-B", "PAY-BUCKET-B-001", nil)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, bucketBLien))

	// Bucket A sum
	total, err := repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, "bucket-A")
	require.NoError(t, err)
	assert.Equal(t, int64(3000), total)

	// Bucket B sum
	total, err = repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, "bucket-B")
	require.NoError(t, err)
	assert.Equal(t, int64(5000), total)

	// Non-existent bucket
	total, err = repo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, "bucket-C")
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

// ---------------------------------------------------------------------------
// JSONBMap (lien_entity.go coverage)
// ---------------------------------------------------------------------------

func TestJSONBMap_TableName(t *testing.T) {
	entity := LienEntity{}
	assert.Equal(t, "lien", entity.TableName())
}

func TestJSONBMap_ValueAndScan(t *testing.T) {
	// Value: nil map → SQL NULL
	var nilMap JSONBMap
	val, err := nilMap.Value()
	require.NoError(t, err)
	assert.Nil(t, val)

	// Value: non-nil map → bytes
	m := JSONBMap(`{"key":"value"}`)
	val, err = m.Value()
	require.NoError(t, err)
	assert.NotNil(t, val)

	// Scan: nil → nil map
	var scanned JSONBMap
	err = scanned.Scan(nil)
	require.NoError(t, err)
	assert.Nil(t, scanned)

	// Scan: []byte
	err = scanned.Scan([]byte(`{"a":1}`))
	require.NoError(t, err)
	assert.Equal(t, JSONBMap(`{"a":1}`), scanned)

	// Scan: string
	err = scanned.Scan(`{"b":2}`)
	require.NoError(t, err)
	assert.Equal(t, JSONBMap(`{"b":2}`), scanned)

	// Scan: unsupported type
	err = scanned.Scan(42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedJSONBType)
}

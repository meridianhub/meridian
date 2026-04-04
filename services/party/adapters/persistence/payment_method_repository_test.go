package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPaymentMethodTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&PartyEntity{},
		&PaymentMethodEntity{},
		&audit.AuditOutbox{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party (
		id UUID PRIMARY KEY,
		party_type VARCHAR(20) NOT NULL,
		legal_name VARCHAR(255) NOT NULL,
		display_name VARCHAR(255),
		status VARCHAR(20) NOT NULL,
		external_reference VARCHAR(255),
		external_reference_type VARCHAR(50),
		attributes JSONB NOT NULL DEFAULT '[]'::jsonb,
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		UNIQUE(external_reference, external_reference_type)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party_payment_method table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_payment_method (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL REFERENCES %s.party(id) ON DELETE CASCADE,
		provider VARCHAR(50) NOT NULL,
		provider_customer_id VARCHAR(255) NOT NULL,
		provider_method_id VARCHAR(255) NOT NULL,
		method_type VARCHAR(50) NOT NULL,
		is_default BOOLEAN NOT NULL DEFAULT false,
		metadata JSONB DEFAULT '{}',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		CONSTRAINT chk_party_payment_method_provider CHECK (provider IN ('STRIPE')),
		CONSTRAINT chk_party_payment_method_status CHECK (status IN ('ACTIVE', 'EXPIRED', 'REMOVED'))
	)`, pq.QuoteIdentifier(schemaName), pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create indexes
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_payment_method_party
		ON %s.party_payment_method (party_id) WHERE status = 'ACTIVE'`,
		pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS idx_party_payment_method_provider_method
		ON %s.party_payment_method (provider, provider_method_id) WHERE status = 'ACTIVE'`,
		pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS idx_party_payment_method_default
		ON %s.party_payment_method (party_id) WHERE is_default = true AND status = 'ACTIVE'`,
		pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the audit_outbox table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL,
		record_id VARCHAR(50) NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

// createPaymentTestParty creates a party record for payment method tests
func createPaymentTestParty(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	partyID := uuid.New()
	err := db.Exec(`
		INSERT INTO party (id, party_type, legal_name, status, version, created_at, updated_at, created_by, updated_by)
		VALUES (?, 'PERSON', 'Test Person', 'ACTIVE', 1, now(), now(), 'system', 'system')
	`, partyID).Error
	require.NoError(t, err)
	return partyID
}

func TestCreatePaymentMethod(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		false,
		&domain.PaymentMethodMetadata{Last4: "4242", Brand: "visa", ExpMonth: 12, ExpYear: 2027},
	)
	require.NoError(t, err)

	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	// Retrieve and verify
	retrieved, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)
	assert.Equal(t, pm.ID(), retrieved.ID())
	assert.Equal(t, partyID, retrieved.PartyID())
	assert.Equal(t, domain.PaymentProviderStripe, retrieved.Provider())
	assert.Equal(t, "cus_1234567890ab", retrieved.ProviderCustomerID())
	assert.Equal(t, "pm_1234567890ab", retrieved.ProviderMethodID())
	assert.Equal(t, domain.PaymentMethodTypeCard, retrieved.MethodType())
	assert.False(t, retrieved.IsDefault())
	assert.Equal(t, domain.PaymentMethodStatusActive, retrieved.Status())
	assert.Equal(t, int64(1), retrieved.Version())
	assert.NotNil(t, retrieved.Metadata())
	assert.Equal(t, "4242", retrieved.Metadata().Last4)
	assert.Equal(t, "visa", retrieved.Metadata().Brand)
}

func TestCreatePaymentMethod_WithDefault(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		true,
		nil,
	)
	require.NoError(t, err)

	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)
	assert.True(t, retrieved.IsDefault())
}

func TestCreatePaymentMethod_DefaultSwitching(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	// Create first payment method as default
	pm1, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_first1234567",
		domain.PaymentMethodTypeCard,
		true,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm1)
	require.NoError(t, err)

	// Create second payment method as default - should unset first
	pm2, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_second234567",
		domain.PaymentMethodTypeBankAccount,
		true,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm2)
	require.NoError(t, err)

	// Verify first is no longer default
	first, err := repo.FindByID(ctx, pm1.ID())
	require.NoError(t, err)
	assert.False(t, first.IsDefault(), "first payment method should no longer be default")

	// Verify second is default
	second, err := repo.FindByID(ctx, pm2.ID())
	require.NoError(t, err)
	assert.True(t, second.IsDefault(), "second payment method should be default")
}

func TestFindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	repo := NewPaymentMethodRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrPaymentMethodNotFound)
}

func TestFindByProviderMethod(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_lookup1234567",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	// Find by provider and method ID
	found, err := repo.FindByProviderMethod(ctx, domain.PaymentProviderStripe, "pm_lookup1234567")
	require.NoError(t, err)
	assert.Equal(t, pm.ID(), found.ID())
	assert.Equal(t, "pm_lookup1234567", found.ProviderMethodID())
}

func TestFindByProviderMethod_NotFound(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	repo := NewPaymentMethodRepository(db)

	_, err := repo.FindByProviderMethod(ctx, domain.PaymentProviderStripe, "pm_nonexistent99")
	assert.ErrorIs(t, err, ErrPaymentMethodNotFound)
}

func TestFindByProviderMethod_IgnoresRemovedMethods(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_removed123456",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	// Remove the method
	err = pm.Remove()
	require.NoError(t, err)
	err = repo.Update(ctx, pm)
	require.NoError(t, err)

	// Should not find removed method
	_, err = repo.FindByProviderMethod(ctx, domain.PaymentProviderStripe, "pm_removed123456")
	assert.ErrorIs(t, err, ErrPaymentMethodNotFound)
}

func TestListActiveByParty(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	// Create 3 payment methods, remove one
	for i := 0; i < 3; i++ {
		pm, err := domain.NewPaymentMethod(
			partyID,
			domain.PaymentProviderStripe,
			"cus_1234567890ab",
			fmt.Sprintf("pm_method%07d", i),
			domain.PaymentMethodTypeCard,
			false,
			nil,
		)
		require.NoError(t, err)
		err = repo.Create(ctx, pm)
		require.NoError(t, err)

		// Remove the third one
		if i == 2 {
			err = pm.Remove()
			require.NoError(t, err)
			err = repo.Update(ctx, pm)
			require.NoError(t, err)
		}
	}

	// List active - should only return 2
	methods, err := repo.ListActiveByParty(ctx, partyID)
	require.NoError(t, err)
	assert.Len(t, methods, 2)
	for _, m := range methods {
		assert.Equal(t, domain.PaymentMethodStatusActive, m.Status())
	}
}

func TestListActiveByParty_Empty(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	methods, err := repo.ListActiveByParty(ctx, partyID)
	require.NoError(t, err)
	assert.Empty(t, methods)
}

func TestFindDefaultByParty(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	// No default initially
	def, err := repo.FindDefaultByParty(ctx, partyID)
	require.NoError(t, err)
	assert.Nil(t, def)

	// Create a default payment method
	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		true,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	// Should find the default
	def, err = repo.FindDefaultByParty(ctx, partyID)
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.Equal(t, pm.ID(), def.ID())
	assert.True(t, def.IsDefault())
}

func TestUpdate_OptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	// Load two copies
	copy1, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)
	copy2, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)

	// First update succeeds
	err = copy1.SetDefault(true)
	require.NoError(t, err)
	err = repo.Update(ctx, copy1)
	require.NoError(t, err)

	// Second update with stale version fails
	err = copy2.Expire()
	require.NoError(t, err)
	err = repo.Update(ctx, copy2)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestUpdate_DefaultSwitching(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	// Create two methods, first as default
	pm1, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_first1234567",
		domain.PaymentMethodTypeCard,
		true,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm1)
	require.NoError(t, err)

	pm2, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_second234567",
		domain.PaymentMethodTypeBankAccount,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm2)
	require.NoError(t, err)

	// Set second as default via Update
	err = pm2.SetDefault(true)
	require.NoError(t, err)
	err = repo.Update(ctx, pm2)
	require.NoError(t, err)

	// Verify first is no longer default
	first, err := repo.FindByID(ctx, pm1.ID())
	require.NoError(t, err)
	assert.False(t, first.IsDefault())

	// Verify second is default
	second, err := repo.FindByID(ctx, pm2.ID())
	require.NoError(t, err)
	assert.True(t, second.IsDefault())
}

func TestProviderCheckConstraint(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)

	// Try to insert with invalid provider directly via SQL
	err := db.WithContext(ctx).Exec(`
		INSERT INTO party_payment_method (id, party_id, provider, provider_customer_id, provider_method_id, method_type, status, version, created_at, updated_at)
		VALUES (gen_random_uuid(), ?, 'PAYPAL', 'cus_abc', 'pm_abc', 'CARD', 'ACTIVE', 1, now(), now())
	`, partyID).Error
	assert.Error(t, err, "should reject invalid provider")
}

func TestStatusCheckConstraint(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)

	// Try to insert with invalid status directly via SQL
	err := db.WithContext(ctx).Exec(`
		INSERT INTO party_payment_method (id, party_id, provider, provider_customer_id, provider_method_id, method_type, status, version, created_at, updated_at)
		VALUES (gen_random_uuid(), ?, 'STRIPE', 'cus_abc', 'pm_abc', 'CARD', 'INVALID', 1, now(), now())
	`, partyID).Error
	assert.Error(t, err, "should reject invalid status")
}

func TestStatusTransition_ActiveToExpired(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		true,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	// Expire the method
	err = pm.Expire()
	require.NoError(t, err)
	err = repo.Update(ctx, pm)
	require.NoError(t, err)

	// Verify status and default unset
	retrieved, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentMethodStatusExpired, retrieved.Status())
	assert.False(t, retrieved.IsDefault())
}

func TestStatusTransition_ActiveToRemoved(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	// Remove the method
	err = pm.Remove()
	require.NoError(t, err)
	err = repo.Update(ctx, pm)
	require.NoError(t, err)

	// Verify status
	retrieved, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentMethodStatusRemoved, retrieved.Status())

	// Should not appear in active list
	active, err := repo.ListActiveByParty(ctx, partyID)
	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestUniqueProviderMethodIndex(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	// Create first method
	pm1, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_samemethod123",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm1)
	require.NoError(t, err)

	// Create second method with same provider_method_id - should fail
	pm2, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_samemethod123",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm2)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrPaymentMethodExists), "should return ErrPaymentMethodExists for duplicate provider_method_id")
}

func TestUniqueProviderMethodIndex_AllowsAfterRemoval(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	// Create and remove first method
	pm1, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_reusable12345",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm1)
	require.NoError(t, err)

	err = pm1.Remove()
	require.NoError(t, err)
	err = repo.Update(ctx, pm1)
	require.NoError(t, err)

	// Create second method with same provider_method_id - should succeed since first is REMOVED
	pm2, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_reusable12345",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm2)
	require.NoError(t, err, "should allow same provider_method_id after removal")
}

func TestMetadataPersistence(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	metadata := &domain.PaymentMethodMetadata{
		Last4:    "4242",
		Brand:    "visa",
		ExpMonth: 12,
		ExpYear:  2027,
	}

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		false,
		metadata,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)
	require.NotNil(t, retrieved.Metadata())
	assert.Equal(t, "4242", retrieved.Metadata().Last4)
	assert.Equal(t, "visa", retrieved.Metadata().Brand)
	assert.Equal(t, 12, retrieved.Metadata().ExpMonth)
	assert.Equal(t, 2027, retrieved.Metadata().ExpYear)
}

func TestNilMetadataPersistence(t *testing.T) {
	db, ctx, cleanup := setupPaymentMethodTestDB(t)
	defer cleanup()

	partyID := createPaymentTestParty(t, db)
	repo := NewPaymentMethodRepository(db)

	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		domain.PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	err = repo.Create(ctx, pm)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, pm.ID())
	require.NoError(t, err)
	assert.Nil(t, retrieved.Metadata())
}

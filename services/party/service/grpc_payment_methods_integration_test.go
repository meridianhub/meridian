package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/lib/pq"

	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// setupPaymentMethodIntegrationTest creates a test database with both party and payment method
// tables, and returns a Service with PaymentMethodRepository wired in.
func setupPaymentMethodIntegrationTest(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.PartyEntity{},
		&persistence.PaymentMethodEntity{},
		&audit.AuditOutbox{},
	})

	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create party table
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

	// Create payment method table
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

	// Create audit outbox table
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

	// Set search_path
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	pmRepo := persistence.NewPaymentMethodRepository(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := NewService(repo, logger)
	require.NoError(t, err)
	svc.WithPaymentMethodRepository(pmRepo)

	return svc, db, ctx, cleanup
}

// registerPartyForPM is a helper that registers a party via gRPC and returns the party ID.
func registerPartyForPM(t *testing.T, svc *Service, ctx context.Context) string {
	t.Helper()
	resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_PERSON,
		LegalName: "Test Person",
	})
	require.NoError(t, err)
	return resp.Party.PartyId
}

// TestAddPaymentMethod_Integration verifies that AddPaymentMethod persists through
// the wired service and can be retrieved via GetDefaultPaymentMethod.
func TestAddPaymentMethod_Integration(t *testing.T) {
	svc, _, ctx, cleanup := setupPaymentMethodIntegrationTest(t)
	defer cleanup()

	partyID := registerPartyForPM(t, svc, ctx)

	// Add a payment method as default
	addResp, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
		PartyId:            partyID,
		Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
		ProviderCustomerId: "cus_integtest1234",
		ProviderMethodId:   "pm_integtest12345",
		MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		IsDefault:          true,
		Metadata: map[string]string{
			"last4": "4242",
			"brand": "visa",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, addResp.PaymentMethod)

	pm := addResp.PaymentMethod
	assert.NotEmpty(t, pm.Id)
	assert.Equal(t, partyID, pm.PartyId)
	assert.Equal(t, pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE, pm.Provider)
	assert.Equal(t, "cus_integtest1234", pm.ProviderCustomerId)
	assert.Equal(t, "pm_integtest12345", pm.ProviderMethodId)
	assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD, pm.MethodType)
	assert.True(t, pm.IsDefault)
	assert.Equal(t, pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_ACTIVE, pm.Status)
	assert.Equal(t, "4242", pm.Metadata["last4"])
	assert.Equal(t, "visa", pm.Metadata["brand"])

	// Retrieve default and verify it matches
	defaultResp, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
		PartyId: partyID,
	})
	require.NoError(t, err)
	assert.Equal(t, pm.Id, defaultResp.PaymentMethod.Id)
	assert.True(t, defaultResp.PaymentMethod.IsDefault)
}

// TestListPaymentMethods_Integration verifies listing active payment methods.
func TestListPaymentMethods_Integration(t *testing.T) {
	svc, _, ctx, cleanup := setupPaymentMethodIntegrationTest(t)
	defer cleanup()

	partyID := registerPartyForPM(t, svc, ctx)

	// Add two payment methods
	_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
		PartyId:            partyID,
		Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
		ProviderCustomerId: "cus_integtest1234",
		ProviderMethodId:   "pm_listmethod01a",
		MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		IsDefault:          true,
	})
	require.NoError(t, err)

	_, err = svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
		PartyId:            partyID,
		Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
		ProviderCustomerId: "cus_integtest1234",
		ProviderMethodId:   "pm_listmethod02b",
		MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT,
	})
	require.NoError(t, err)

	// List and verify both returned
	listResp, err := svc.ListPaymentMethods(ctx, &pb.ListPaymentMethodsRequest{
		PartyId: partyID,
	})
	require.NoError(t, err)
	assert.Len(t, listResp.PaymentMethods, 2)
}

// TestGetDefaultPaymentMethod_NotFound_Integration verifies NOT_FOUND when no default exists.
func TestGetDefaultPaymentMethod_NotFound_Integration(t *testing.T) {
	svc, _, ctx, cleanup := setupPaymentMethodIntegrationTest(t)
	defer cleanup()

	partyID := registerPartyForPM(t, svc, ctx)

	_, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
		PartyId: partyID,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestRemovePaymentMethod_Integration verifies removing a payment method via gRPC.
func TestRemovePaymentMethod_Integration(t *testing.T) {
	svc, _, ctx, cleanup := setupPaymentMethodIntegrationTest(t)
	defer cleanup()

	partyID := registerPartyForPM(t, svc, ctx)

	// Add a payment method
	addResp, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
		PartyId:            partyID,
		Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
		ProviderCustomerId: "cus_integtest1234",
		ProviderMethodId:   "pm_removetest1234",
		MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
	})
	require.NoError(t, err)

	// Remove it
	_, err = svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
		Id:      addResp.PaymentMethod.Id,
		Version: addResp.PaymentMethod.Version,
	})
	require.NoError(t, err)

	// Verify it no longer appears in list
	listResp, err := svc.ListPaymentMethods(ctx, &pb.ListPaymentMethodsRequest{
		PartyId: partyID,
	})
	require.NoError(t, err)
	assert.Empty(t, listResp.PaymentMethods)
}

// TestSetDefaultPaymentMethod_Integration verifies switching the default payment method.
func TestSetDefaultPaymentMethod_Integration(t *testing.T) {
	svc, _, ctx, cleanup := setupPaymentMethodIntegrationTest(t)
	defer cleanup()

	partyID := registerPartyForPM(t, svc, ctx)

	// Add first payment method as default
	first, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
		PartyId:            partyID,
		Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
		ProviderCustomerId: "cus_defaulttest12",
		ProviderMethodId:   "pm_defaultfirst12",
		MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		IsDefault:          true,
	})
	require.NoError(t, err)
	assert.True(t, first.PaymentMethod.IsDefault)

	// Add second payment method (not default)
	second, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
		PartyId:            partyID,
		Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
		ProviderCustomerId: "cus_defaulttest12",
		ProviderMethodId:   "pm_defaultsecnd1",
		MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT,
	})
	require.NoError(t, err)
	assert.False(t, second.PaymentMethod.IsDefault)

	// Switch default to second
	setResp, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
		Id: second.PaymentMethod.Id,
	})
	require.NoError(t, err)
	assert.True(t, setResp.PaymentMethod.IsDefault)

	// Verify new default
	defaultResp, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
		PartyId: partyID,
	})
	require.NoError(t, err)
	assert.Equal(t, second.PaymentMethod.Id, defaultResp.PaymentMethod.Id)
}

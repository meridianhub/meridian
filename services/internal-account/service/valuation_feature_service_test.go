package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const testTenantIDForVF = "test_tenant"

func setupValuationFeatureServiceTest(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, nil)

	tid := tenant.TenantID(testTenantIDForVF)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create the internal_account table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.internal_account (
		id UUID PRIMARY KEY,
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_code VARCHAR(50) NOT NULL UNIQUE,
		name VARCHAR(255) NOT NULL,
		account_type VARCHAR(20) NOT NULL DEFAULT 'CLEARING',
		clearing_purpose VARCHAR(20) NOT NULL DEFAULT 'UNSPECIFIED',
		org_party_id UUID NULL,
		product_type_code VARCHAR(100) NULL,
		product_type_version INTEGER NULL,
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(32) NOT NULL DEFAULT 'CURRENCY',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		counterparty_id VARCHAR(100),
		counterparty_name VARCHAR(255),
		counterparty_external_ref VARCHAR(255),
		attributes JSONB,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		created_by VARCHAR(100) NOT NULL DEFAULT 'system',
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		updated_by VARCHAR(100) NOT NULL DEFAULT 'system',
		deleted_at TIMESTAMP WITH TIME ZONE,
		version BIGINT NOT NULL DEFAULT 1
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create the valuation_features table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.valuation_features (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		instrument_code VARCHAR(32) NOT NULL,
		valuation_method_id UUID NOT NULL,
		valuation_method_version INT NOT NULL,
		parameters JSONB,
		lifecycle_status VARCHAR(16) NOT NULL,
		valid_from TIMESTAMP WITH TIME ZONE NOT NULL,
		valid_to TIMESTAMP WITH TIME ZONE NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(100) NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_by VARCHAR(100) NOT NULL,
		version INT NOT NULL DEFAULT 1,
		CONSTRAINT chk_valuation_feature_lifecycle_status CHECK (lifecycle_status IN ('INITIATED','ACTIVE','TERMINATED'))
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create unique index for active features
	err = db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS idx_valuation_feature_account_instrument_active
		ON %q.valuation_features (account_id, instrument_code)
		WHERE lifecycle_status = 'ACTIVE' AND valid_to = '9999-12-31 23:59:59+00'`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	valuationFeatureRepo := persistence.NewValuationFeatureRepository(db)

	svc, err := NewServiceWithValuationFeatures(repo, valuationFeatureRepo)
	require.NoError(t, err)

	return svc, db, ctx, cleanup
}

func createTestIBAForVF(t *testing.T, db *gorm.DB, instrumentCode string) (uuid.UUID, string) {
	t.Helper()
	accountUUID := uuid.New()
	accountID := fmt.Sprintf("IBA-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("IBA-TEST-%s", uuid.New().String()[:6])

	err := db.Exec(`INSERT INTO internal_account (id, account_id, account_code, name, account_type, clearing_purpose, instrument_code, dimension, status, created_by, updated_by)
		VALUES (?, ?, ?, 'Test Account', 'CLEARING', 'GENERAL', ?, 'CURRENCY', 'ACTIVE', 'test', 'test')`,
		accountUUID, accountID, accountCode, instrumentCode).Error
	require.NoError(t, err)

	return accountUUID, accountCode
}

func TestCreateValuationFeature_Success(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	methodID := uuid.New()
	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      methodID.String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
		Parameters:             `{"source": "ECB"}`,
	}

	resp, err := svc.CreateValuationFeature(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Feature)
	assert.NotEmpty(t, resp.Feature.Id)
	assert.Equal(t, "USD", resp.Feature.InstrumentCode)
	assert.Equal(t, methodID.String(), resp.Feature.ValuationMethodId)
	assert.Equal(t, int32(1), resp.Feature.ValuationMethodVersion)
	assert.Equal(t, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE, resp.Feature.LifecycleStatus)
	assert.Contains(t, resp.Feature.Parameters, "ECB")
}

func TestCreateValuationFeature_MethodOutputMismatch(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	methodID := uuid.New()
	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      methodID.String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "EUR", // Does NOT match account native instrument (GBP)
		Parameters:             `{}`,
	}

	resp, err := svc.CreateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "output_instrument mismatch")
	assert.Contains(t, st.Message(), "expected GBP")
	assert.Contains(t, st.Message(), "got EUR")
}

func TestCreateValuationFeature_AccountNotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.CreateValuationFeatureRequest{
		AccountId:              "non-existent-account",
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}

	resp, err := svc.CreateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	// Returns an error (gRPC status) when account lookup fails
	_, ok := status.FromError(err)
	require.True(t, ok)
}

func TestCreateValuationFeature_InvalidMethodID(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      "not-a-uuid",
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}

	resp, err := svc.CreateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateValuationFeature_Terminate(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	updateReq := &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	}

	updateResp, err := svc.UpdateValuationFeature(ctx, updateReq)

	require.NoError(t, err)
	require.NotNil(t, updateResp)
	assert.Equal(t, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED, updateResp.Feature.LifecycleStatus)
}

func TestUpdateValuationFeature_FeatureNotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.UpdateValuationFeatureRequest{
		FeatureId: uuid.New().String(),
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	}

	resp, err := svc.UpdateValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdateValuationFeature_InvalidAction(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	updateReq := &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_UNSPECIFIED,
	}

	resp, err := svc.UpdateValuationFeature(ctx, updateReq)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetValuationFeature_ByID(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	getReq := &pb.GetValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
	}

	getResp, err := svc.GetValuationFeature(ctx, getReq)

	require.NoError(t, err)
	require.NotNil(t, getResp)
	assert.Equal(t, createResp.Feature.Id, getResp.Feature.Id)
	assert.Equal(t, "USD", getResp.Feature.InstrumentCode)
}

func TestGetValuationFeature_ByAccountAndInstrument(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	getReq := &pb.GetValuationFeatureRequest{
		AccountId:      accountCode,
		InstrumentCode: "USD",
	}

	getResp, err := svc.GetValuationFeature(ctx, getReq)

	require.NoError(t, err)
	require.NotNil(t, getResp)
	assert.Equal(t, createResp.Feature.Id, getResp.Feature.Id)
	assert.Equal(t, "USD", getResp.Feature.InstrumentCode)
}

func TestGetValuationFeature_NotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.GetValuationFeatureRequest{
		FeatureId: uuid.New().String(),
	}

	resp, err := svc.GetValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetValuationFeature_MissingIdentifiers(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.GetValuationFeatureRequest{}

	resp, err := svc.GetValuationFeature(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "must provide either feature_id or")
}

func TestListValuationFeatures_Success(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	for _, instrument := range []string{"USD", "EUR", "CHF"} {
		createReq := &pb.CreateValuationFeatureRequest{
			AccountId:              accountCode,
			InstrumentCode:         instrument,
			ValuationMethodId:      uuid.New().String(),
			ValuationMethodVersion: 1,
			OutputInstrument:       "GBP",
		}
		_, err := svc.CreateValuationFeature(ctx, createReq)
		require.NoError(t, err)
	}

	listReq := &pb.ListValuationFeaturesRequest{
		AccountId: accountCode,
	}

	listResp, err := svc.ListValuationFeatures(ctx, listReq)

	require.NoError(t, err)
	require.NotNil(t, listResp)
	assert.Len(t, listResp.Features, 3)
}

func TestListValuationFeatures_FilterByStatus(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	// Create first feature (will remain active)
	createReq1 := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	_, err := svc.CreateValuationFeature(ctx, createReq1)
	require.NoError(t, err)

	// Create second feature and terminate it
	createReq2 := &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "EUR",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp2, err := svc.CreateValuationFeature(ctx, createReq2)
	require.NoError(t, err)

	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp2.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})
	require.NoError(t, err)

	// List only active features
	listReq := &pb.ListValuationFeaturesRequest{
		AccountId:       accountCode,
		LifecycleStatus: pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE,
	}

	listResp, err := svc.ListValuationFeatures(ctx, listReq)

	require.NoError(t, err)
	require.NotNil(t, listResp)
	assert.Len(t, listResp.Features, 1)
	assert.Equal(t, "USD", listResp.Features[0].InstrumentCode)
}

func TestListValuationFeatures_AccountNotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.ListValuationFeaturesRequest{
		AccountId: "non-existent-account",
	}

	resp, err := svc.ListValuationFeatures(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	// findAccountByID checks both domain.ErrAccountNotFound and persistence.ErrAccountNotFound,
	// so a non-existent account correctly returns NotFound.
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestValuationFeatureRepoNil(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(testTenantIDForVF))

	_, err = svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())

	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())

	_, err = svc.GetValuationFeature(ctx, &pb.GetValuationFeatureRequest{})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())

	_, err = svc.ListValuationFeatures(ctx, &pb.ListValuationFeaturesRequest{})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

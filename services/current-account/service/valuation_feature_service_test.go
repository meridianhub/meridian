package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func setupValuationFeatureServiceTest(t *testing.T) (*Service, context.Context, func()) {
	t.Helper()
	db := openSharedDB(t)

	// Create the tenant schema for tests
	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create the account table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.account (
		id UUID PRIMARY KEY,
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_identification VARCHAR(34) NOT NULL UNIQUE,
		party_id UUID NOT NULL,
		org_party_id UUID NULL,
		balance BIGINT NOT NULL DEFAULT 0,
		available_balance BIGINT NOT NULL DEFAULT 0,
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		overdraft_limit BIGINT NOT NULL DEFAULT 0,
		overdraft_enabled BOOLEAN NOT NULL DEFAULT FALSE,
		overdraft_rate NUMERIC(5,4) NOT NULL DEFAULT 0,
		balance_updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		created_by VARCHAR(100) NOT NULL DEFAULT 'system',
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		updated_by VARCHAR(100) NOT NULL DEFAULT 'system',
		deleted_at TIMESTAMP WITH TIME ZONE,
		opened_at TIMESTAMP WITH TIME ZONE,
		closed_at TIMESTAMP WITH TIME ZONE,
		freeze_reason TEXT,
		product_type_code VARCHAR(50) NULL,
		product_type_version INT NULL,
		behavior_class VARCHAR(50) NULL,
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

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	// Create repositories
	repo := persistence.NewRepository(db)
	valuationFeatureRepo := persistence.NewValuationFeatureRepository(db)

	// Create service
	svc, err := NewServiceWithValuationFeatures(repo, valuationFeatureRepo)
	require.NoError(t, err)

	cleanup := func() {
		db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schemaName))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}

	return svc, ctx, cleanup
}

func createTestAccountForVF(t *testing.T, db *gorm.DB, currency string) (uuid.UUID, string) {
	t.Helper()
	accountUUID := uuid.New()
	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])
	iban := fmt.Sprintf("GB%s", uuid.New().String()[:20])

	err := db.Exec(`INSERT INTO account (id, account_id, account_identification, party_id, instrument_code, dimension, status, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, 'CURRENCY', 'ACTIVE', 'test', 'test')`,
		accountUUID, accountID, iban, uuid.New(), currency).Error
	require.NoError(t, err)

	return accountUUID, accountID
}

func TestCreateValuationFeature_Success(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account with GBP currency
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	methodID := uuid.New()
	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      methodID.String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP", // Matches account native instrument
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account with GBP currency
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	methodID := uuid.New()
	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
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
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCreateValuationFeature_InvalidMethodID(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	req := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account and feature
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	// First create a feature
	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	// Now terminate it
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account and feature
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	// Try with unspecified action
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account and feature
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	// Get by ID
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account and feature
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	createReq := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp, err := svc.CreateValuationFeature(ctx, createReq)
	require.NoError(t, err)

	// Get by account ID and instrument
	getReq := &pb.GetValuationFeatureRequest{
		AccountId:      accountID,
		InstrumentCode: "USD",
	}

	getResp, err := svc.GetValuationFeature(ctx, getReq)

	require.NoError(t, err)
	require.NotNil(t, getResp)
	assert.Equal(t, createResp.Feature.Id, getResp.Feature.Id)
	assert.Equal(t, "USD", getResp.Feature.InstrumentCode)
}

func TestGetValuationFeature_NotFound(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Missing both feature_id and (account_id + instrument_code)
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
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	// Create multiple features
	for _, instrument := range []string{"USD", "EUR", "CHF"} {
		createReq := &pb.CreateValuationFeatureRequest{
			AccountId:              accountID,
			InstrumentCode:         instrument,
			ValuationMethodId:      uuid.New().String(),
			ValuationMethodVersion: 1,
			OutputInstrument:       "GBP",
		}
		_, err := svc.CreateValuationFeature(ctx, createReq)
		require.NoError(t, err)
	}

	// List all features
	listReq := &pb.ListValuationFeaturesRequest{
		AccountId: accountID,
	}

	listResp, err := svc.ListValuationFeatures(ctx, listReq)

	require.NoError(t, err)
	require.NotNil(t, listResp)
	assert.Len(t, listResp.Features, 3)
}

func TestListValuationFeatures_FilterByStatus(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	// Create a test account
	_, accountID := createTestAccountForVF(t, svc.repo.DB(), "GBP")

	// Create first feature (will remain active)
	createReq1 := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	_, err := svc.CreateValuationFeature(ctx, createReq1)
	require.NoError(t, err)

	// Create second feature and terminate it
	createReq2 := &pb.CreateValuationFeatureRequest{
		AccountId:              accountID,
		InstrumentCode:         "EUR",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	}
	createResp2, err := svc.CreateValuationFeature(ctx, createReq2)
	require.NoError(t, err)

	// Terminate the second feature
	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp2.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})
	require.NoError(t, err)

	// List only active features
	listReq := &pb.ListValuationFeaturesRequest{
		AccountId:       accountID,
		LifecycleStatus: pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE,
	}

	listResp, err := svc.ListValuationFeatures(ctx, listReq)

	require.NoError(t, err)
	require.NotNil(t, listResp)
	assert.Len(t, listResp.Features, 1)
	assert.Equal(t, "USD", listResp.Features[0].InstrumentCode)
}

func TestListValuationFeatures_AccountNotFound(t *testing.T) {
	svc, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	req := &pb.ListValuationFeaturesRequest{
		AccountId: "non-existent-account",
	}

	resp, err := svc.ListValuationFeatures(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestValuationFeatureRepoNil(t *testing.T) {
	// Create service without valuation feature repo
	repo := &persistence.Repository{}
	svc, err := NewService(repo, nil)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))

	// All valuation feature operations should fail
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

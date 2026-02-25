package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// mockValuationEngine implements ValuationEngine for testing.
type mockValuationEngine struct {
	result *ValuationResult
	err    error
}

func (m *mockValuationEngine) Evaluate(_ context.Context, _ ValuationParams) (*ValuationResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

const testTenantIDForVE = "test_tenant_ve"

func setupValuationEngineTest(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, nil)

	tid := tenant.TenantID(testTenantIDForVE)
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
		correspondent_bank_id VARCHAR(100),
		correspondent_bank_name VARCHAR(255),
		correspondent_external_ref VARCHAR(255),
		correspondent_swift_code VARCHAR(11),
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
		CONSTRAINT chk_ve_lifecycle_status CHECK (lifecycle_status IN ('INITIATED','ACTIVE','TERMINATED'))
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create unique index for active features
	err = db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS idx_ve_feature_account_instrument_active
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

func createTestIBAForVE(t *testing.T, db *gorm.DB, instrumentCode string) (uuid.UUID, string, string) {
	t.Helper()
	accountUUID := uuid.New()
	accountID := fmt.Sprintf("IBA-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("IBA-VE-%s", uuid.New().String()[:6])

	err := db.Exec(`INSERT INTO internal_account (id, account_id, account_code, name, account_type, clearing_purpose, instrument_code, dimension, status, created_by, updated_by)
		VALUES (?, ?, ?, 'Test VE Account', 'CLEARING', 'GENERAL', ?, 'CURRENCY', 'ACTIVE', 'test', 'test')`,
		accountUUID, accountID, accountCode, instrumentCode).Error
	require.NoError(t, err)

	return accountUUID, accountID, accountCode
}

func createTestValuationFeature(t *testing.T, db *gorm.DB, accountUUID uuid.UUID, inputInstrument string) uuid.UUID {
	t.Helper()
	featureID := uuid.New()
	methodID := uuid.New()
	now := time.Now()
	farFuture := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

	err := db.Exec(`INSERT INTO valuation_features (id, account_id, instrument_code, valuation_method_id, valuation_method_version, parameters, lifecycle_status, valid_from, valid_to, created_at, created_by, updated_at, updated_by, version)
		VALUES (?, ?, ?, ?, 1, '{"source":"ECB"}', 'ACTIVE', ?, ?, ?, 'test', ?, 'test', 1)`,
		featureID, accountUUID, inputInstrument, methodID, now, farFuture, now, now).Error
	require.NoError(t, err)

	return featureID
}

// ========================================
// EvaluateAssetValuation - Unit Tests (mock repo)
// ========================================

func TestEvaluateAssetValuation_RepoNotConfigured(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	resp, err := svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "test-id",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestEvaluateAssetValuation_MissingAccountID(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceWithValuationFeatures(repo, &persistence.ValuationFeatureRepository{})
	require.NoError(t, err)

	resp, err := svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_NilInput(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceWithValuationFeatures(repo, &persistence.ValuationFeatureRepository{})
	require.NoError(t, err)

	resp, err := svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "test-id",
		Input:     nil,
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_EmptyInstrumentCode(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceWithValuationFeatures(repo, &persistence.ValuationFeatureRepository{})
	require.NoError(t, err)

	resp, err := svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "test-id",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_EmptyAmount(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceWithValuationFeatures(repo, &persistence.ValuationFeatureRepository{})
	require.NoError(t, err)

	resp, err := svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "test-id",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_InvalidAmount(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceWithValuationFeatures(repo, &persistence.ValuationFeatureRepository{})
	require.NoError(t, err)

	resp, err := svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "test-id",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "not-a-number",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_NonPositiveAmount(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceWithValuationFeatures(repo, &persistence.ValuationFeatureRepository{})
	require.NoError(t, err)

	resp, err := svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "test-id",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "-50.00",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ========================================
// EvaluateAssetValuation - Integration Tests (real DB)
// ========================================

func TestEvaluateAssetValuation_IdentityConversion(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	// Create account with GBP native instrument
	_, _, accountCode := createTestIBAForVE(t, db, "GBP")

	// Request valuation with same instrument as native -> identity conversion
	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: accountCode,
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "GBP", // Same as native
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Output)
	assert.Equal(t, "100", resp.Output.Amount)
	assert.Equal(t, "GBP", resp.Output.InstrumentCode)
	assert.True(t, resp.IsEstimate)
	require.NotNil(t, resp.Basis)
	assert.Equal(t, "identity", resp.Basis.MethodId)
	assert.Equal(t, "1", resp.Basis.MethodVersion)
	assert.Contains(t, resp.Basis.CalculationPath, "identity_conversion")
	assert.False(t, resp.Basis.DegradedMode)
}

func TestEvaluateAssetValuation_WithValuationFeature_NoEngine(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	// Create account with GBP native instrument
	accountUUID, _, accountCode := createTestIBAForVE(t, db, "GBP")

	// Create a valuation feature for USD -> GBP
	createTestValuationFeature(t, db, accountUUID, "USD")

	// Request valuation with USD (requires conversion, no engine configured)
	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: accountCode,
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Output)
	// Passthrough when no engine configured
	assert.Equal(t, "100", resp.Output.Amount)
	assert.Equal(t, "GBP", resp.Output.InstrumentCode) // Output in native instrument
	assert.True(t, resp.IsEstimate)
	require.NotNil(t, resp.Basis)
	assert.True(t, resp.Basis.DegradedMode)
	assert.Contains(t, resp.Basis.CalculationPath, "passthrough_no_engine")
	require.Len(t, resp.Basis.Warnings, 1)
	assert.Equal(t, "NO_VALUATION_ENGINE", resp.Basis.Warnings[0].Code)
}

func TestEvaluateAssetValuation_WithValuationEngine(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	// Create account with GBP native instrument
	accountUUID, _, accountCode := createTestIBAForVE(t, db, "GBP")

	// Create a valuation feature for USD -> GBP
	createTestValuationFeature(t, db, accountUUID, "USD")

	// Configure a mock valuation engine
	svc.valuationEngine = &mockValuationEngine{
		result: &ValuationResult{
			OutputAmount:    decimal.NewFromFloat(79.50),
			OutputCode:      "GBP",
			AppliedRates:    map[string]string{"fx_rate": "0.795"},
			ObservationIDs:  []string{"obs-001"},
			ComputedAt:      time.Now(),
			CalculationPath: []string{"spot_fx_conversion"},
			DegradedMode:    false,
			CacheHit:        false,
			Warnings:        nil,
			MarketQualities: []MarketDataQualityResult{
				{
					Source:           "live_feed",
					QualityLevel:     "ACTUAL",
					ObservedAt:       time.Now(),
					StalenessSeconds: 2,
				},
			},
		},
	}

	// Request valuation with USD
	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: accountCode,
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Output)
	assert.Equal(t, "79.5", resp.Output.Amount)
	assert.Equal(t, "GBP", resp.Output.InstrumentCode)
	assert.True(t, resp.IsEstimate)
	assert.False(t, resp.CacheHit)
	require.NotNil(t, resp.Basis)
	assert.False(t, resp.Basis.DegradedMode)
	assert.Contains(t, resp.Basis.AppliedRates, "fx_rate")
	assert.Equal(t, "0.795", resp.Basis.AppliedRates["fx_rate"])
	assert.Contains(t, resp.Basis.CalculationPath, "spot_fx_conversion")
	require.Len(t, resp.Basis.MarketDataQualities, 1)
	assert.Equal(t, "live_feed", resp.Basis.MarketDataQualities[0].Source)
	assert.Equal(t, "ACTUAL", resp.Basis.MarketDataQualities[0].QualityLevel)
}

func TestEvaluateAssetValuation_WithKnowledgeAt(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	// Create account with GBP native instrument
	_, _, accountCode := createTestIBAForVE(t, db, "GBP")

	// Use custom knowledge_at - same instrument identity should work regardless
	knowledgeAt := timestamppb.New(time.Now().Add(-1 * time.Hour))

	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: accountCode,
		Input: &quantityv1.InstrumentAmount{
			Amount:         "50.00",
			InstrumentCode: "GBP",
		},
		KnowledgeAt: knowledgeAt,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Output)
	assert.Equal(t, "50", resp.Output.Amount)
	assert.Equal(t, "GBP", resp.Output.InstrumentCode)
	// Verify knowledge_at was passed through to analysis
	require.NotNil(t, resp.Basis)
	require.NotNil(t, resp.Basis.KnowledgeAt)
}

func TestEvaluateAssetValuation_AccountNotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "nonexistent-account",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestEvaluateAssetValuation_NoValuationFeature(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	// Create account with GBP native instrument (no valuation feature)
	_, _, accountCode := createTestIBAForVE(t, db, "GBP")

	// Request valuation with different instrument - no feature exists
	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: accountCode,
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestEvaluateAssetValuation_ValuationEngineFailed(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	// Create account and valuation feature
	accountUUID, _, accountCode := createTestIBAForVE(t, db, "GBP")
	createTestValuationFeature(t, db, accountUUID, "USD")

	// Configure a failing valuation engine
	svc.valuationEngine = &mockValuationEngine{
		err: fmt.Errorf("market data unavailable"),
	}

	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: accountCode,
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ========================================
// valuateInternal - Unit Tests
// ========================================

func TestValuateInternal_IdentityConversion_NoDbNeeded(t *testing.T) {
	// For identity conversion, only need account lookup (mock repo)
	repo := newMockRepository()
	accountUUID := uuid.New()
	account := buildTestAccount(t, accountUUID, "IBA-001", "GBP")
	repo.accounts[accountUUID] = account
	repo.accountsByCode["IBA-001"] = account

	svc, err := NewServiceWithValuationFeatures(repo, &persistence.ValuationFeatureRepository{})
	require.NoError(t, err)

	result, err := svc.valuateInternal(
		context.Background(),
		"IBA-001",
		decimal.NewFromFloat(100.00),
		"GBP", // Same as native
		time.Now(),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.OutputAmount.Equal(decimal.NewFromFloat(100.00)))
	assert.Equal(t, "GBP", result.OutputCode)
	assert.Equal(t, "identity", result.Analysis.MethodId)
}

func TestValuateInternal_RepoNotConfigured(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)
	// valuationFeatureRepo is nil

	result, err := svc.valuateInternal(
		context.Background(),
		"IBA-001",
		decimal.NewFromFloat(100.00),
		"USD",
		time.Now(),
	)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrValuationRepoNotConfigured)
}

// buildTestAccount creates an InternalAccount using the builder (for unit tests).
func buildTestAccount(t *testing.T, id uuid.UUID, accountCode, instrumentCode string) domain.InternalAccount {
	t.Helper()
	return domain.NewInternalAccountBuilder().
		WithID(id).
		WithAccountID(accountCode).
		WithAccountCode(accountCode).
		WithName("Test Account").
		WithAccountType(domain.AccountTypeClearing).
		WithClearingPurpose(domain.ClearingPurposeGeneral).
		WithInstrumentCode(instrumentCode).
		WithDimension("CURRENCY").
		WithStatus(domain.AccountStatusActive).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()
}

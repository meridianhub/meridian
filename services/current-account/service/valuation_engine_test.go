package service

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
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

const testTenantIDForValuation = "test_valuation_tenant"

// mockValuationEngine implements ValuationEngine for testing.
type mockValuationEngine struct {
	evaluateFn func(ctx context.Context, params ValuationParams) (*ValuationResult, error)
}

func (m *mockValuationEngine) Evaluate(ctx context.Context, params ValuationParams) (*ValuationResult, error) {
	if m.evaluateFn != nil {
		return m.evaluateFn(ctx, params)
	}
	// Default: multiply by 0.80 (simulating USD→GBP rate)
	return &ValuationResult{
		OutputAmount:    params.InputAmount.Mul(decimal.NewFromFloat(0.80)),
		OutputCode:      params.OutputCode,
		AppliedRates:    map[string]string{"fx_rate": "0.80"},
		ObservationIDs:  []string{"obs-001"},
		ComputedAt:      time.Now(),
		CalculationPath: []string{"lookup_rate", "apply_fx"},
		DegradedMode:    false,
		CacheHit:        false,
		Warnings:        nil,
		MarketQualities: []MarketDataQualityResult{
			{
				Source:           "live_feed",
				QualityLevel:     "ACTUAL",
				ObservedAt:       time.Now(),
				StalenessSeconds: 0,
			},
		},
	}, nil
}

func setupValuationEngineTest(t *testing.T) (*Service, context.Context, func()) {
	return setupValuationEngineTestWithEngine(t, nil)
}

func setupValuationEngineTestWithEngine(t *testing.T, engine ValuationEngine) (*Service, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, nil)

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create account table
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

	// Create valuation_features table
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

	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	valuationFeatureRepo := persistence.NewValuationFeatureRepository(db)

	svc, err := NewServiceWithValuationFeatures(repo, valuationFeatureRepo)
	require.NoError(t, err)

	// Inject the valuation engine if provided
	if engine != nil {
		svc.valuationEngine = engine
	}

	return svc, ctx, cleanup
}

// createTestAccountForValuation creates a test account and returns its string account_id.
func createTestAccountForValuation(t *testing.T, _ context.Context, db *gorm.DB, schemaName, accountID, currency string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	partyID := uuid.New()

	ident := accountID
	if len(ident) > 8 {
		ident = ident[:8]
	}
	err := db.Exec(fmt.Sprintf(`INSERT INTO %q.account (id, account_id, account_identification, party_id, instrument_code, dimension, status)
		VALUES (?, ?, ?, ?, ?, 'CURRENCY', 'ACTIVE')`, schemaName),
		id, accountID, "GB29NWBK"+ident, partyID, currency).Error
	require.NoError(t, err)
	return id
}

// createTestValuationFeature creates an active valuation feature for testing.
func createTestValuationFeature(t *testing.T, _ context.Context, db *gorm.DB, schemaName string, accountUUID uuid.UUID, instrumentCode string) uuid.UUID {
	t.Helper()
	methodID := uuid.New()
	feature, err := domain.NewValuationFeature(accountUUID, instrumentCode, methodID, 1, nil, "test")
	require.NoError(t, err)
	require.NoError(t, feature.Activate("test"))

	err = db.Exec(fmt.Sprintf(`INSERT INTO %q.valuation_features
		(id, account_id, instrument_code, valuation_method_id, valuation_method_version,
		 parameters, lifecycle_status, valid_from, valid_to, created_at, created_by, updated_at, updated_by, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, schemaName),
		feature.ID, feature.AccountID, feature.InstrumentCode,
		feature.ValuationMethodID, feature.ValuationMethodVersion,
		nil, string(feature.LifecycleStatus),
		feature.ValidFrom, feature.ValidTo,
		feature.CreatedAt, feature.CreatedBy,
		feature.UpdatedAt, feature.UpdatedBy, feature.Version,
	).Error
	require.NoError(t, err)
	return feature.ID
}

// --- Tests for EvaluateAssetValuation RPC ---

func TestEvaluateAssetValuation_IdentityConversion(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-EVAL-001", "GBP")

	// Request valuation where input is same as native instrument (identity conversion)
	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "GBP",
			Version:        1,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "100", resp.Output.Amount)
	assert.Equal(t, "GBP", resp.Output.InstrumentCode)
	assert.True(t, resp.IsEstimate)
	assert.NotNil(t, resp.Basis)
	assert.Equal(t, "identity", resp.Basis.MethodId)
	assert.False(t, resp.Basis.DegradedMode)
}

func TestEvaluateAssetValuation_WithEngine(t *testing.T) {
	engine := &mockValuationEngine{}
	svc, ctx, cleanup := setupValuationEngineTestWithEngine(t, engine)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-EVAL-002", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "USD")

	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-002",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	// 100 * 0.80 = 80
	assert.Equal(t, "80", resp.Output.Amount)
	assert.Equal(t, "GBP", resp.Output.InstrumentCode)
	assert.True(t, resp.IsEstimate)
	assert.NotNil(t, resp.Basis)
	assert.Equal(t, "0.80", resp.Basis.AppliedRates["fx_rate"])
	assert.False(t, resp.Basis.DegradedMode)
	assert.Len(t, resp.Basis.MarketDataQualities, 1)
	assert.Equal(t, "ACTUAL", resp.Basis.MarketDataQualities[0].QualityLevel)
}

func TestEvaluateAssetValuation_DegradedMode_NoEngine(t *testing.T) {
	// No engine configured - should return degraded mode passthrough
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-EVAL-003", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "USD")

	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-003",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	// Passthrough since no engine
	assert.Equal(t, "100", resp.Output.Amount)
	assert.Equal(t, "GBP", resp.Output.InstrumentCode)
	assert.True(t, resp.IsEstimate)
	assert.True(t, resp.Basis.DegradedMode)
	assert.Len(t, resp.Basis.Warnings, 1)
	assert.Equal(t, "NO_VALUATION_ENGINE", resp.Basis.Warnings[0].Code)
}

func TestEvaluateAssetValuation_AccountNotFound(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-NONEXISTENT",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestEvaluateAssetValuation_NoValuationFeature(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-EVAL-004", "GBP")

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-004",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD", // No feature exists for USD
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "no active valuation feature")
}

func TestEvaluateAssetValuation_InvalidInput_MissingAccountID(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id is required")
}

func TestEvaluateAssetValuation_InvalidInput_MissingInput(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-001",
		Input:     nil,
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_InvalidInput_EmptyInstrument(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_InvalidInput_BadAmount(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "not-a-number",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_InvalidInput_NegativeAmount(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "-50.00",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_InvalidInput_ZeroAmount(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "0",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEvaluateAssetValuation_RepoNotConfigured(t *testing.T) {
	svc, err := NewService(&persistence.Repository{}, nil)
	require.NoError(t, err)

	_, err = svc.EvaluateAssetValuation(context.Background(), &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestEvaluateAssetValuation_WithKnowledgeAt(t *testing.T) {
	engine := &mockValuationEngine{}
	svc, ctx, cleanup := setupValuationEngineTestWithEngine(t, engine)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-EVAL-005", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "EUR")

	// Use a knowledge_at slightly in the future to ensure the feature is valid
	knowledgeAt := time.Now().Add(1 * time.Minute)
	resp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-005",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "200.00",
			InstrumentCode: "EUR",
			Version:        1,
		},
		KnowledgeAt: timestamppb.New(knowledgeAt),
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	// 200 * 0.80 = 160
	assert.Equal(t, "160", resp.Output.Amount)
	assert.True(t, resp.IsEstimate)
	assert.NotNil(t, resp.Basis.KnowledgeAt)
}

func TestEvaluateAssetValuation_EngineError(t *testing.T) {
	engine := &mockValuationEngine{
		evaluateFn: func(_ context.Context, _ ValuationParams) (*ValuationResult, error) {
			return nil, fmt.Errorf("market data unavailable")
		},
	}
	svc, ctx, cleanup := setupValuationEngineTestWithEngine(t, engine)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-EVAL-006", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "USD")

	_, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
		AccountId: "ACC-EVAL-006",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "USD",
			Version:        1,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "valuation engine failed")
}

// --- Ghost Pricing Prevention Test ---
// This is the most critical test: verify that valuateInternal produces
// identical results regardless of the caller (inquiry vs binding).

func TestGhostPricingPrevention_IdenticalResults(t *testing.T) {
	// Use a deterministic engine that returns consistent results
	callCount := 0
	engine := &mockValuationEngine{
		evaluateFn: func(_ context.Context, params ValuationParams) (*ValuationResult, error) {
			callCount++
			// Deterministic: always returns input * 0.795 for USD->GBP
			rate := decimal.NewFromFloat(0.795)
			return &ValuationResult{
				OutputAmount:    params.InputAmount.Mul(rate),
				OutputCode:      params.OutputCode,
				AppliedRates:    map[string]string{"fx_rate": "0.795"},
				ObservationIDs:  []string{"obs-fixed-001"},
				ComputedAt:      time.Now(),
				CalculationPath: []string{"lookup_rate", "apply_fx"},
				DegradedMode:    false,
				CacheHit:        false,
			}, nil
		},
	}

	svc, ctx, cleanup := setupValuationEngineTestWithEngine(t, engine)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-GHOST-001", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "USD")

	knowledgeAt := time.Now()

	// Run 1000 random inputs through both the RPC (inquiry) and valuateInternal (binding)
	// and verify identical outputs
	for i := 0; i < 1000; i++ {
		// Generate random amount between 0.01 and 999999.99
		amount := decimal.NewFromFloat(rand.Float64()*999999.98 + 0.01).Round(2)

		// Path 1: Through EvaluateAssetValuation RPC (inquiry)
		rpcResp, err := svc.EvaluateAssetValuation(ctx, &pb.EvaluateAssetValuationRequest{
			AccountId: "ACC-GHOST-001",
			Input: &quantityv1.InstrumentAmount{
				Amount:         amount.String(),
				InstrumentCode: "USD",
				Version:        1,
			},
			KnowledgeAt: timestamppb.New(knowledgeAt),
		})
		require.NoError(t, err, "iteration %d: RPC failed", i)

		// Path 2: Through valuateInternal directly (simulating binding path)
		internalResult, err := svc.valuateInternal(ctx, "ACC-GHOST-001", amount, "USD", knowledgeAt)
		require.NoError(t, err, "iteration %d: internal failed", i)

		// CRITICAL ASSERTION: Both paths must produce identical output amounts
		rpcOutput, err := decimal.NewFromString(rpcResp.Output.Amount)
		require.NoError(t, err, "iteration %d: failed to parse RPC output", i)

		assert.True(t, rpcOutput.Equal(internalResult.OutputAmount),
			"iteration %d: Ghost Pricing detected! RPC output=%s, internal output=%s, input=%s",
			i, rpcOutput.String(), internalResult.OutputAmount.String(), amount.String())

		// Verify both use same method
		assert.Equal(t, rpcResp.Basis.AppliedRates["fx_rate"], internalResult.Analysis.AppliedRates["fx_rate"],
			"iteration %d: different rates applied", i)
	}

	// Verify engine was called for both paths (2 calls per iteration for non-identity)
	assert.Equal(t, 2000, callCount, "expected 2000 engine calls (1000 RPC + 1000 internal)")
}

// --- valuateInternal unit tests ---

func TestValuateInternal_IdentityConversion(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-INT-001", "GBP")

	result, err := svc.valuateInternal(ctx, "ACC-INT-001", decimal.NewFromFloat(100), "GBP", time.Now())
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(100).Equal(result.OutputAmount))
	assert.Equal(t, "GBP", result.OutputCode)
	assert.Equal(t, "identity", result.Analysis.MethodId)
}

func TestValuateInternal_AccountNotFound(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	_, err := svc.valuateInternal(ctx, "ACC-NONEXISTENT", decimal.NewFromFloat(100), "USD", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account not found")
}

func TestValuateInternal_NoFeature(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-INT-002", "GBP")

	_, err := svc.valuateInternal(ctx, "ACC-INT-002", decimal.NewFromFloat(100), "EUR", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no active valuation feature")
}

func TestValuateInternal_WithEngine(t *testing.T) {
	engine := &mockValuationEngine{}
	svc, ctx, cleanup := setupValuationEngineTestWithEngine(t, engine)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-INT-003", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "USD")

	result, err := svc.valuateInternal(ctx, "ACC-INT-003", decimal.NewFromFloat(100), "USD", time.Now())
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(80).Equal(result.OutputAmount))
	assert.Equal(t, "GBP", result.OutputCode)
	assert.False(t, result.Analysis.DegradedMode)
}

func TestValuateInternal_FallbackWithoutEngine(t *testing.T) {
	svc, ctx, cleanup := setupValuationEngineTest(t)
	defer cleanup()

	tid := tenant.TenantID(testTenantIDForValuation)
	schemaName := tid.SchemaName()
	accountUUID := createTestAccountForValuation(t, ctx, svc.repo.DB(), schemaName, "ACC-INT-004", "GBP")
	createTestValuationFeature(t, ctx, svc.repo.DB(), schemaName, accountUUID, "USD")

	result, err := svc.valuateInternal(ctx, "ACC-INT-004", decimal.NewFromFloat(100), "USD", time.Now())
	require.NoError(t, err)
	// Passthrough since no engine
	assert.True(t, decimal.NewFromFloat(100).Equal(result.OutputAmount))
	assert.True(t, result.Analysis.DegradedMode)
	assert.Len(t, result.Analysis.Warnings, 1)
}

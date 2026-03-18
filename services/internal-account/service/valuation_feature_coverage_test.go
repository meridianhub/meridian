package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ---------------------------------------------------------------------------
// protoToDomainLifecycleStatus – exhaustive enum coverage
// ---------------------------------------------------------------------------

func TestProtoToDomainLifecycleStatus_AllValues(t *testing.T) {
	svc := &Service{logger: testLogger()}

	tests := []struct {
		input    pb.ValuationFeatureLifecycleStatus
		expected domain.ValuationFeatureLifecycleStatus
	}{
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED, domain.ValuationFeatureLifecycleStatusInitiated},
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE, domain.ValuationFeatureLifecycleStatusActive},
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED, domain.ValuationFeatureLifecycleStatusTerminated},
		{pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED, domain.ValuationFeatureLifecycleStatusInitiated},
		{pb.ValuationFeatureLifecycleStatus(999), domain.ValuationFeatureLifecycleStatusInitiated}, // default case
	}

	for _, tc := range tests {
		got := svc.protoToDomainLifecycleStatus(tc.input)
		assert.Equal(t, tc.expected, got, "input %v", tc.input)
	}
}

// ---------------------------------------------------------------------------
// domainToProtoLifecycleStatus – exhaustive enum coverage
// ---------------------------------------------------------------------------

func TestDomainToProtoLifecycleStatus_AllValues(t *testing.T) {
	svc := &Service{logger: testLogger()}

	tests := []struct {
		input    domain.ValuationFeatureLifecycleStatus
		expected pb.ValuationFeatureLifecycleStatus
	}{
		{domain.ValuationFeatureLifecycleStatusInitiated, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_INITIATED},
		{domain.ValuationFeatureLifecycleStatusActive, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE},
		{domain.ValuationFeatureLifecycleStatusTerminated, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED},
		{domain.ValuationFeatureLifecycleStatus("unknown"), pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_UNSPECIFIED}, // default
	}

	for _, tc := range tests {
		got := svc.domainToProtoLifecycleStatus(tc.input)
		assert.Equal(t, tc.expected, got, "input %v", tc.input)
	}
}

// ---------------------------------------------------------------------------
// UpdateValuationFeature – activate on already-active feature (idempotent)
// ---------------------------------------------------------------------------

func TestUpdateValuationFeature_Activate_AlreadyActive_Idempotent(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	// Create a feature (starts in ACTIVE state per CreateValuationFeature)
	createResp, err := svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	})
	require.NoError(t, err)

	// ACTIVATE on already-ACTIVE feature → idempotent (no error, still ACTIVE)
	resp, err := svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_ACTIVATE,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_ACTIVE, resp.Feature.LifecycleStatus)
}

// TestUpdateValuationFeature_Activate_OnTerminated_FailedPrecondition tests
// activating a TERMINATED feature, which is an invalid transition.
func TestUpdateValuationFeature_Activate_OnTerminated_FailedPrecondition(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	// Create and immediately terminate
	createResp, err := svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	})
	require.NoError(t, err)

	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})
	require.NoError(t, err)

	// Now try to ACTIVATE a TERMINATED feature → FailedPrecondition
	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_ACTIVATE,
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetValuationFeature – invalid feature_id and knowledge_at paths
// ---------------------------------------------------------------------------

func TestGetValuationFeature_InvalidFeatureID(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, err := svc.GetValuationFeature(ctx, &pb.GetValuationFeatureRequest{
		FeatureId: "not-a-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetValuationFeature_ByAccountAndInstrument_WithKnowledgeAt(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	// Create and leave ACTIVE
	_, err := svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	})
	require.NoError(t, err)

	// Retrieve with explicit knowledge_at timestamp (covers the KnowledgeAt != nil path)
	resp, err := svc.GetValuationFeature(ctx, &pb.GetValuationFeatureRequest{
		AccountId:      accountCode,
		InstrumentCode: "USD",
		KnowledgeAt:    timestamppb.New(time.Now()),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

// ---------------------------------------------------------------------------
// InitiateLien integration – with bucket_id and expires_at
// ---------------------------------------------------------------------------

func TestInitiateLien_Integration_WithBucketAndExpiry(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-BUCKET-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("BUCKET-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	future := time.Now().Add(24 * time.Hour)

	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-BUCKET-001",
		BucketId:              "bucket-savings",
		ExpiresAt:             timestamppb.New(future),
		Input:                 &quantityv1.InstrumentAmount{Amount: "50.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Lien.ExpiresAt)
	assert.Equal(t, "bucket-savings", resp.Lien.BucketId)
}

// ---------------------------------------------------------------------------
// CreateValuationFeature – empty instrument code and invalid parameters JSON
// ---------------------------------------------------------------------------

// TestCreateValuationFeature_EmptyInstrumentCode covers the domain.NewValuationFeature
// error path (lines 103-106) triggered when InstrumentCode is empty.
func TestCreateValuationFeature_EmptyInstrumentCode(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	_, err := svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "", // empty → domain.NewValuationFeature returns ErrInstrumentCodeEmpty
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP", // must match account native instrument to pass prior check
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateValuationFeature_InvalidParametersJSON(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	_, err := svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
		Parameters:             "{not valid json}",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateValuationFeature – invalid feature_id format and unsupported action
// ---------------------------------------------------------------------------

func TestUpdateValuationFeature_InvalidFeatureID_Format(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, err := svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: "not-a-uuid",
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_ACTIVATE,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUpdateValuationFeature_UnsupportedAction_Default(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")
	createResp, err := svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	})
	require.NoError(t, err)

	// Pass an action value outside the valid range → default case
	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction(99),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetValuationFeature – account+instrument paths
// ---------------------------------------------------------------------------

func TestGetValuationFeature_ByAccountAndInstrument_AccountNotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, err := svc.GetValuationFeature(ctx, &pb.GetValuationFeatureRequest{
		AccountId:      "IBA-NONEXISTENT-9999",
		InstrumentCode: "USD",
	})
	require.Error(t, err)
	// account not found → NotFound
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetValuationFeature_ByAccountAndInstrument_FeatureNotFound(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	// No feature created for EUR instrument on this account
	_, err := svc.GetValuationFeature(ctx, &pb.GetValuationFeatureRequest{
		AccountId:      accountCode,
		InstrumentCode: "EUR",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateValuationFeature – terminate on already-terminated feature (idempotent)
// ---------------------------------------------------------------------------

func TestUpdateValuationFeature_Terminate_AlreadyTerminated_Idempotent(t *testing.T) {
	svc, db, ctx, cleanup := setupValuationFeatureServiceTest(t)
	defer cleanup()

	_, accountCode := createTestIBAForVF(t, db, "GBP")

	createResp, err := svc.CreateValuationFeature(ctx, &pb.CreateValuationFeatureRequest{
		AccountId:              accountCode,
		InstrumentCode:         "USD",
		ValuationMethodId:      uuid.New().String(),
		ValuationMethodVersion: 1,
		OutputInstrument:       "GBP",
	})
	require.NoError(t, err)

	// Terminate once
	_, err = svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})
	require.NoError(t, err)

	// Terminate again → idempotent (no error, still TERMINATED)
	resp, err := svc.UpdateValuationFeature(ctx, &pb.UpdateValuationFeatureRequest{
		FeatureId: createResp.Feature.Id,
		Action:    pb.ValuationFeatureAction_VALUATION_FEATURE_ACTION_TERMINATE,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, pb.ValuationFeatureLifecycleStatus_VALUATION_FEATURE_LIFECYCLE_STATUS_TERMINATED, resp.Feature.LifecycleStatus)
}

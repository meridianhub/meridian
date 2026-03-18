package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// Constructor and option coverage
// ---------------------------------------------------------------------------

func TestNewServiceWithValuationFeatures_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceWithValuationFeatures(repo, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Nil(t, svc.valuationFeatureRepo)
}

func TestNewServiceWithValuationFeatures_NilRepo(t *testing.T) {
	_, err := NewServiceWithValuationFeatures(nil, nil)
	require.Error(t, err)
}

func TestWithValuationEngine_SetsField(t *testing.T) {
	repo := newMockRepository()
	engine := &mockValuationEngine{}
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithValuationEngine(engine),
	)
	require.NoError(t, err)
	assert.NotNil(t, svc.valuationEngine)
}

func TestWithValuationFeatureRepo_SetsField(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithValuationFeatureRepo(nil),
	)
	require.NoError(t, err)
	assert.Nil(t, svc.valuationFeatureRepo)

	// Also verify a non-nil value is set
	featureRepo := persistence.NewValuationFeatureRepository(nil)
	svc, err = NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithValuationFeatureRepo(featureRepo),
	)
	require.NoError(t, err)
	assert.NotNil(t, svc.valuationFeatureRepo)
}

func TestWithOutboxPublisher_SetsField(t *testing.T) {
	repo := newMockRepository()
	// Passing nil publisher and db exercises the option code path without requiring
	// a real outbox publisher or database connection.
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithOutboxPublisher(nil, nil),
	)
	require.NoError(t, err)
	assert.Nil(t, svc.outboxPublisher)
	assert.Nil(t, svc.db)
}

func TestSetOutboxPublisher_SetsField(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	// SetOutboxPublisher wires in the publisher post-construction.
	svc.SetOutboxPublisher(nil, nil)
	assert.Nil(t, svc.outboxPublisher)
	assert.Nil(t, svc.db)
}

// ---------------------------------------------------------------------------
// UpdateInternalAccount – UUID-based lookup paths
// ---------------------------------------------------------------------------

func TestUpdateInternalAccount_ByUUID_NotFound(t *testing.T) {
	repo := newMockRepository() // empty – no accounts
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// A UUID-format account ID not in the repository → NotFound
	_, err = svc.UpdateInternalAccount(testCtx(), &pb.UpdateInternalAccountRequest{
		AccountId: uuid.New().String(),
		Name:      "New Name",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateInternalAccount_ByUUID_InternalError(t *testing.T) {
	repo := newMockRepository()
	repo.findByIDErr = errors.New("simulated db failure")

	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	_, err = svc.UpdateInternalAccount(testCtx(), &pb.UpdateInternalAccountRequest{
		AccountId: uuid.New().String(), // UUID → hits FindByID → returns error
		Name:      "New Name",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateInternalAccount – version mismatch
// ---------------------------------------------------------------------------

func TestUpdateInternalAccount_VersionMismatch(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// Create an account first
	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "MISMATCH-001",
		Name:            "Mismatch Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	_, err = svc.UpdateInternalAccount(testCtx(), &pb.UpdateInternalAccountRequest{
		AccountId:       createResp.Facility.AccountCode,
		Name:            "Updated Name",
		ExpectedVersion: 999, // wrong version
	})
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateInternalAccount – save errors through non-UUID path
// ---------------------------------------------------------------------------

func TestUpdateInternalAccount_VersionConflictOnSave(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// Create an account first
	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "VER-CONFLICT-001",
		Name:            "Version Conflict Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	// Simulate a version conflict on Save
	repo.saveErr = persistence.ErrVersionConflict

	_, err = svc.UpdateInternalAccount(testCtx(), &pb.UpdateInternalAccountRequest{
		AccountId: createResp.Facility.AccountCode,
		Name:      "Updated Name",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateInternalAccount – counterparty update
// ---------------------------------------------------------------------------

func TestUpdateInternalAccount_Success_WithCounterparty(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// NOSTRO accounts require counterparty details at creation
	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "CTR-001",
		Name:            "Counterparty Account",
		ProductTypeCode: "NOSTRO_GBP",
		InstrumentCode:  "GBP",
		CounterpartyDetails: &pb.CounterpartyDetails{
			CounterpartyId:          "CP-INITIAL",
			CounterpartyName:        "Initial Counterparty",
			CounterpartyExternalRef: "INIT-EXT-001",
		},
	})
	require.NoError(t, err)

	// Update the counterparty details
	resp, err := svc.UpdateInternalAccount(testCtx(), &pb.UpdateInternalAccountRequest{
		AccountId: createResp.Facility.AccountCode,
		CounterpartyDetails: &pb.CounterpartyDetails{
			CounterpartyId:          "CP-001",
			CounterpartyName:        "Updated Counterparty",
			CounterpartyExternalRef: "EXT-001",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "CP-001", resp.Facility.CounterpartyDetails.CounterpartyId)
}

// ---------------------------------------------------------------------------
// ListInternalAccounts – filter variants
// ---------------------------------------------------------------------------

func TestListInternalAccounts_WithStatusFilter(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// Create an account
	_, err = svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "LIST-STATUS-001",
		Name:            "Status Filter Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	// Filter by active status
	resp, err := svc.ListInternalAccounts(testCtx(), &pb.ListInternalAccountsRequest{
		StatusFilter: pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestListInternalAccounts_WithBehaviorClassFilter(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	resp, err := svc.ListInternalAccounts(testCtx(), &pb.ListInternalAccountsRequest{
		BehaviorClassFilter: "CLEARING",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestListInternalAccounts_WithInstrumentCodeFilter(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	resp, err := svc.ListInternalAccounts(testCtx(), &pb.ListInternalAccountsRequest{
		InstrumentCodeFilter: "GBP",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestListInternalAccounts_WithPagination(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	resp, err := svc.ListInternalAccounts(testCtx(), &pb.ListInternalAccountsRequest{
		Pagination: &commonpb.Pagination{
			PageSize:  10,
			PageToken: "5",
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestListInternalAccounts_WithClearingPurposeFilter(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	resp, err := svc.ListInternalAccounts(testCtx(), &pb.ListInternalAccountsRequest{
		ClearingPurposeFilter: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// ---------------------------------------------------------------------------
// ControlInternalAccount – extra paths
// ---------------------------------------------------------------------------

func TestControlInternalAccount_VersionConflictOnSave(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "CTRL-VER-001",
		Name:            "Control Version Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	repo.saveErr = persistence.ErrVersionConflict

	_, err = svc.ControlInternalAccount(testCtx(), &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
}

func TestControlInternalAccount_InvalidAction(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "CTRL-INV-001",
		Name:            "Invalid Action Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalAccount(testCtx(), &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNSPECIFIED,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestControlInternalAccount_Activate(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// Create and suspend an account first
	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "CTRL-ACT-001",
		Name:            "Activate Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalAccount(testCtx(), &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
	})
	require.NoError(t, err)

	// Now reactivate
	resp, err := svc.ControlInternalAccount(testCtx(), &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
}

// ---------------------------------------------------------------------------
// InitiateInternalAccount – missing tenant context
// ---------------------------------------------------------------------------

func TestInitiateInternalAccount_MissingTenantContext(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// context.Background() has no tenant — service with cache should require it
	_, err = svc.InitiateInternalAccount(context.Background(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "NO-TENANT-001",
		Name:            "No Tenant Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// findAccountByID – indirect tests via GetBalance / UpdateInternalAccount
// ---------------------------------------------------------------------------

func TestFindAccountByID_AccountIDInternalError(t *testing.T) {
	repo := newMockRepository()
	repo.findByAccountIDErr = errors.New("db connection lost")

	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	// Use a non-UUID accountID to trigger the FindByAccountID path
	_, err = svc.UpdateInternalAccount(testCtx(), &pb.UpdateInternalAccountRequest{
		AccountId: "IBA-NONEXISTENT",
		Name:      "Updated",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestFindAccountByID_UUID_InternalError(t *testing.T) {
	repo := newMockRepository()
	repo.findByIDErr = errors.New("db connection lost")

	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	_, err = svc.UpdateInternalAccount(testCtx(), &pb.UpdateInternalAccountRequest{
		AccountId: uuid.New().String(), // UUID format → FindByID → returns error
		Name:      "Updated",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetBalance – additional paths
// ---------------------------------------------------------------------------

func TestGetBalance_EmptyAccountID(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	_, err = svc.GetBalance(testCtx(), &pb.GetBalanceRequest{
		AccountId: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetBalance_PositionKeepingInvalidArgument(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.InvalidArgument, "bad account id"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "BAL-INV-001",
		Name:            "Balance Invalid",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	_, err = svc.GetBalance(testCtx(), &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.Error(t, err)
	// InvalidArgument from PK is remapped to Internal (our code issue)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestGetBalance_AsOf_Nil(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		asOfSet: true,
		asOf:    nil, // nil as_of → service falls back to Now()
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "BAL-ASOF-001",
		Name:            "Balance AsOf Nil",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	resp, err := svc.GetBalance(testCtx(), &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)
	// Service falls back to Now(), so AsOf should be non-nil
	assert.NotNil(t, resp.AsOf)
}

func TestGetBalance_PositionKeepingUnknownError(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.PermissionDenied, "access denied"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	createResp, err := svc.InitiateInternalAccount(testCtx(), &pb.InitiateInternalAccountRequest{
		AccountCode:     "BAL-PERM-001",
		Name:            "Balance Perm",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)

	_, err = svc.GetBalance(testCtx(), &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.Error(t, err)
	// Unknown PK codes map to Internal
	assert.Equal(t, codes.Internal, status.Code(err))
}

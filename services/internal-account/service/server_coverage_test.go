package service

import (
	"context"
	"errors"
	"testing"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// NewServiceWithClients and NewServiceFull – nil repo path
// ---------------------------------------------------------------------------

func TestNewServiceWithClients_NilRepo(t *testing.T) {
	_, err := NewServiceWithClients(nil, nil, nil, nil, nil)
	require.Error(t, err)
	assert.Equal(t, ErrRepositoryNil, err)
}

func TestNewServiceFull_NilRepo(t *testing.T) {
	_, err := NewServiceFull(nil, nil, nil, nil, nil)
	require.Error(t, err)
	assert.Equal(t, ErrRepositoryNil, err)
}

// ---------------------------------------------------------------------------
// updateAccount – cannot update a closed account
// ---------------------------------------------------------------------------

func TestUpdateInternalAccount_ClosedAccount_Direct(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	// Create an account and close it
	account, err := domain.NewInternalAccount(
		"IBA-CLOSED-TEST-001", "CLOSED-TEST-001", "Closed Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	closedAccount, err := account.Close("test close")
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), closedAccount))

	_, err = svc.UpdateInternalAccount(context.Background(), &pb.UpdateInternalAccountRequest{
		AccountId: "IBA-CLOSED-TEST-001",
		Name:      "New Name",
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateInternalAccount – UUID-format AccountId path (FindByID branch)
// ---------------------------------------------------------------------------

func TestUpdateInternalAccount_UUIDAccountId(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		"IBA-UUID-PATH-001", "UUID-PATH-001", "UUID Path Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	// Use the account's UUID (not the IBA- string) to trigger FindByID path
	_, err = svc.UpdateInternalAccount(context.Background(), &pb.UpdateInternalAccountRequest{
		AccountId: account.ID().String(),
		Name:      "Updated Name",
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ControlInternalAccount – default (unknown) action case
// ---------------------------------------------------------------------------

func TestControlInternalAccount_DefaultAction(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		"IBA-CTRL-DEFAULT-001", "CTRL-DEFAULT-001", "Default Action Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	_, err = svc.ControlInternalAccount(context.Background(), &pb.ControlInternalAccountRequest{
		AccountId:     "CTRL-DEFAULT-001",
		ControlAction: pb.ControlAction(99), // out-of-range value hits default case
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// ControlInternalAccount – generic save error (non-version-conflict)
// ---------------------------------------------------------------------------

func TestControlInternalAccount_GenericSaveError(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		"IBA-CTRL-SAVERR-001", "CTRL-SAVERR-001", "Save Error Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	// Inject a generic save error (not ErrVersionConflict)
	repo.saveErr = errors.New("database connection lost")

	_, err = svc.ControlInternalAccount(context.Background(), &pb.ControlInternalAccountRequest{
		AccountId:     "CTRL-SAVERR-001",
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "test",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// storeIdempotencyResultOrCleanup – store error AND delete error
// ---------------------------------------------------------------------------

func TestStoreIdempotencyResultOrCleanup_StoreAndDeleteBothFail(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	mockIdemp.storeErr = assert.AnError
	mockIdemp.deleteErr = assert.AnError // delete also fails → covers inner warn log

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	key := idempotency.Key{
		TenantID:  "test-tenant",
		Namespace: idempotencyNamespace,
		Operation: "execute_lien",
		RequestID: "store-delete-err-001",
	}

	// Should not panic when both store and delete fail
	svc.storeIdempotencyResultOrCleanup(context.Background(), key, &pb.ExecuteLienResponse{}, "test")
}

// ---------------------------------------------------------------------------
// mappers – accountStatusToProto and counterpartyTypeFromAccountType default cases
// ---------------------------------------------------------------------------

func TestAccountStatusToProto_DefaultCase(t *testing.T) {
	result := accountStatusToProto(domain.AccountStatus("completely-unknown"))
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED, result)
}

func TestCounterpartyTypeFromAccountType_DefaultCase(t *testing.T) {
	result := counterpartyTypeFromAccountType(domain.AccountType("completely-unknown"))
	assert.Equal(t, pb.CounterpartyType_COUNTERPARTY_TYPE_UNSPECIFIED, result)
}

// ---------------------------------------------------------------------------
// findAccountByID – UUID path (line 931)
// ---------------------------------------------------------------------------

// TestControlInternalAccount_WithUUIDAccountId exercises the UUID path in
// findAccountByID (line 931) by passing a UUID string as AccountId to ControlInternalAccount.
func TestControlInternalAccount_WithUUIDAccountId(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		"IBA-UUID-CTRL-001", "UUID-CTRL-001", "UUID Control Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	// Use the UUID as AccountId → triggers the UUID path in findAccountByID
	_, err = svc.ControlInternalAccount(context.Background(), &pb.ControlInternalAccountRequest{
		AccountId:     account.ID().String(),
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "test",
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ListInternalAccounts – pagination next-page-token path (line 771)
// ---------------------------------------------------------------------------

// TestListInternalAccounts_PaginationNextPageToken covers the branch where
// len(accounts) == filter.Limit, generating a nextPageToken.
func TestListInternalAccounts_PaginationNextPageToken(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	// Save 1 account so len(accounts) == 1
	account, err := domain.NewInternalAccount(
		"IBA-PAGE-001", "PAGE-001", "Pagination Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	// Request with PageSize=1 → filter.Limit=1, len(accounts)==1, nextPageToken is set
	resp, err := svc.ListInternalAccounts(context.Background(), &pb.ListInternalAccountsRequest{
		Pagination: &commonpb.Pagination{
			PageSize: 1,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Pagination.NextPageToken)
}

// ---------------------------------------------------------------------------
// updateAccount – counterparty and save-error paths
// ---------------------------------------------------------------------------

// TestUpdateInternalAccount_InvalidCounterpartyDetails covers the branch where
// NewCounterpartyDetailsWithOptions returns an error (lines 589-592 in server.go).
func TestUpdateInternalAccount_InvalidCounterpartyDetails(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		"IBA-CPTY-INVAL-001", "CPTY-INVAL-001", "Invalid Counterparty Test",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	// CounterpartyId is empty → NewCounterpartyDetailsWithOptions returns error
	_, err = svc.UpdateInternalAccount(context.Background(), &pb.UpdateInternalAccountRequest{
		AccountId: "IBA-CPTY-INVAL-001",
		CounterpartyDetails: &pb.CounterpartyDetails{
			CounterpartyId:          "",
			CounterpartyName:        "Some Name",
			CounterpartyExternalRef: "ext-001",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestUpdateInternalAccount_CounterpartyNotAllowed covers the branch where
// account.UpdateCounterparty returns an error because CLEARING doesn't allow counterparty
// (lines 594-597 in server.go).
func TestUpdateInternalAccount_CounterpartyNotAllowed(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		"IBA-CPTY-NOTALLOWED-001", "CPTY-NOTALLOWED-001", "No Counterparty Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	// Valid counterparty details but CLEARING account doesn't accept counterparty
	_, err = svc.UpdateInternalAccount(context.Background(), &pb.UpdateInternalAccountRequest{
		AccountId: "IBA-CPTY-NOTALLOWED-001",
		CounterpartyDetails: &pb.CounterpartyDetails{
			CounterpartyId:          "CP-001",
			CounterpartyName:        "Corp Name",
			CounterpartyExternalRef: "ext-ref-001",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestUpdateInternalAccount_GenericSaveError covers the generic save-failure branch
// (lines 606-607 in server.go) where the error is not a version conflict.
func TestUpdateInternalAccount_GenericSaveError(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	account, err := domain.NewInternalAccount(
		"IBA-UPD-SAVERR-001", "UPD-SAVERR-001", "Save Error Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))

	repo.saveErr = errors.New("database connection lost")

	_, err = svc.UpdateInternalAccount(context.Background(), &pb.UpdateInternalAccountRequest{
		AccountId: "IBA-UPD-SAVERR-001",
		Name:      "Updated Name",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-bank-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockRepository implements domain.Repository for testing.
type mockRepository struct {
	accounts        map[uuid.UUID]domain.InternalBankAccount
	accountsByCode  map[string]domain.InternalBankAccount
	saveErr         error
	findByIDErr     error
	findByCodeErr   error
	listErr         error
	existsByCodeErr error
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		accounts:       make(map[uuid.UUID]domain.InternalBankAccount),
		accountsByCode: make(map[string]domain.InternalBankAccount),
	}
}

func (m *mockRepository) Save(_ context.Context, account domain.InternalBankAccount) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.accounts[account.ID()] = account
	m.accountsByCode[account.AccountCode()] = account
	return nil
}

func (m *mockRepository) FindByID(_ context.Context, id uuid.UUID) (domain.InternalBankAccount, error) {
	if m.findByIDErr != nil {
		return domain.InternalBankAccount{}, m.findByIDErr
	}
	account, ok := m.accounts[id]
	if !ok {
		return domain.InternalBankAccount{}, domain.ErrAccountNotFound
	}
	return account, nil
}

func (m *mockRepository) FindByCode(_ context.Context, accountCode string) (domain.InternalBankAccount, error) {
	if m.findByCodeErr != nil {
		return domain.InternalBankAccount{}, m.findByCodeErr
	}
	account, ok := m.accountsByCode[accountCode]
	if !ok {
		return domain.InternalBankAccount{}, domain.ErrAccountNotFound
	}
	return account, nil
}

func (m *mockRepository) List(_ context.Context, _ domain.ListFilter) ([]domain.InternalBankAccount, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	accounts := make([]domain.InternalBankAccount, 0, len(m.accounts))
	for _, account := range m.accounts {
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (m *mockRepository) ExistsByCode(_ context.Context, accountCode string) (bool, error) {
	if m.existsByCodeErr != nil {
		return false, m.existsByCodeErr
	}
	_, ok := m.accountsByCode[accountCode]
	return ok, nil
}

// mockPositionKeepingClient implements PositionKeepingClient for testing.
type mockPositionKeepingClient struct {
	balances []*positionkeepingv1.BalanceEntry
	err      error
}

func (m *mockPositionKeepingClient) GetAccountBalances(_ context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances:  m.balances,
		AsOf:      timestamppb.Now(), // Provide timestamp for balance calculation time
	}, nil
}

func (m *mockPositionKeepingClient) Close() error {
	return nil
}

// mockReferenceDataClient implements ReferenceDataClient for testing.
type mockReferenceDataClient struct {
	instrument *referencedatav1.InstrumentDefinition
	err        error
}

func (m *mockReferenceDataClient) RetrieveInstrument(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &referencedatav1.RetrieveInstrumentResponse{
		Instrument: m.instrument,
	}, nil
}

func (m *mockReferenceDataClient) Close() error {
	return nil
}

func TestNewService_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestNewService_NilRepository(t *testing.T) {
	svc, err := NewService(nil)
	assert.Error(t, err)
	assert.Equal(t, ErrRepositoryNil, err)
	assert.Nil(t, svc)
}

func TestNewServiceWithClients_Success(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	refClient := &mockReferenceDataClient{}

	svc, err := NewServiceWithClients(repo, posClient, refClient, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestInitiateInternalBankAccount_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()
	req := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	}

	resp, err := svc.InitiateInternalBankAccount(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccountId)
	assert.NotNil(t, resp.Facility)
	assert.Equal(t, "CLR-001", resp.Facility.AccountCode)
	assert.Equal(t, "USD Clearing Account", resp.Facility.Name)
	assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING, resp.Facility.AccountType)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
}

func TestInitiateInternalBankAccount_WithCorrespondent(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()
	req := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "NOSTRO-USD-HSBC",
		Name:           "HSBC USD Nostro",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO,
		InstrumentCode: "USD",
		CorrespondentDetails: &pb.CorrespondentBankDetails{
			BankId:             "HSBC001",
			BankName:           "HSBC Bank",
			ExternalAccountRef: "12345678",
			SwiftCode:          "HSBCGB2L",
			CorrespondentType:  pb.CorrespondentType_CORRESPONDENT_TYPE_NOSTRO,
		},
	}

	resp, err := svc.InitiateInternalBankAccount(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, resp.Facility.CorrespondentDetails)
	assert.Equal(t, "HSBC001", resp.Facility.CorrespondentDetails.BankId)
	assert.Equal(t, "HSBC Bank", resp.Facility.CorrespondentDetails.BankName)
}

func TestInitiateInternalBankAccount_NostroWithoutCorrespondent(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()
	req := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "NOSTRO-USD-HSBC",
		Name:           "HSBC USD Nostro",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO,
		InstrumentCode: "USD",
		// Missing CorrespondentDetails
	}

	resp, err := svc.InitiateInternalBankAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestInitiateInternalBankAccount_InvalidAccountType(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()
	req := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "Test Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_UNSPECIFIED,
		InstrumentCode: "USD",
	}

	resp, err := svc.InitiateInternalBankAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRetrieveInternalBankAccount_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// First create an account
	createReq := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	}
	createResp, err := svc.InitiateInternalBankAccount(ctx, createReq)
	require.NoError(t, err)

	// Then retrieve it by code
	retrieveResp, err := svc.RetrieveInternalBankAccount(ctx, &pb.RetrieveInternalBankAccountRequest{
		AccountId: "CLR-001",
	})
	require.NoError(t, err)
	assert.Equal(t, createResp.Facility.AccountCode, retrieveResp.Facility.AccountCode)
}

func TestRetrieveInternalBankAccount_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()
	resp, err := svc.RetrieveInternalBankAccount(ctx, &pb.RetrieveInternalBankAccountRequest{
		AccountId: "nonexistent",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestControlInternalBankAccount_Suspend(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	// Suspend the account
	controlResp, err := svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Testing suspension for compliance review",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED, controlResp.Facility.AccountStatus)
}

func TestControlInternalBankAccount_Close(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	// Close the account
	controlResp, err := svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account no longer needed after migration",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED, controlResp.Facility.AccountStatus)
}

func TestControlInternalBankAccount_InvalidTransition(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create and close account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Closing the account for testing",
	})
	require.NoError(t, err)

	// Try to activate a closed account - should fail
	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		Reason:        "Trying to reactivate closed account",
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestListInternalBankAccounts_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create multiple accounts
	for i := 0; i < 3; i++ {
		_, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "CLR-00" + string(rune('1'+i)),
			Name:           "Clearing Account " + string(rune('1'+i)),
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "USD",
		})
		require.NoError(t, err)
	}

	// List all accounts
	resp, err := svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Facilities, 3)
}

func TestGetBalance_Success(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "1000.00",
				},
			},
		},
	}
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	// Get balance
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)
	assert.NotNil(t, balanceResp.Balance)
	assert.Equal(t, "USD", balanceResp.Balance.InstrumentCode)
	assert.Equal(t, "1000.00", balanceResp.Balance.Amount)
	assert.NotNil(t, balanceResp.LastUpdated)
}

func TestGetBalance_AccountSuspended(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create and suspend account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Suspending for balance test",
	})
	require.NoError(t, err)

	// Try to get balance - should fail with FailedPrecondition
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not active")
}

func TestGetBalance_NoPositionKeepingClient(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	// Try to get balance without position keeping client
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

// ErrPositionKeepingUnavailable is used in tests.
var errPositionKeepingUnavailable = errors.New("position keeping service unavailable")

func TestGetBalance_AccountNotFound(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Try to get balance for non-existent account
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: "non-existent-account",
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetBalance_AccountClosed(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	// Close the account
	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Closing account for balance test",
	})
	require.NoError(t, err)

	// Try to get balance - should fail with FailedPrecondition
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not active")
}

func TestGetBalance_PositionKeepingError(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: errPositionKeepingUnavailable, // Non-gRPC error maps to Unavailable
	}
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	// Try to get balance - position keeping error (non-gRPC error maps to Unavailable)
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestGetBalance_PositionKeepingUnavailable(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.Unavailable, "service temporarily unavailable"),
	}
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	// Try to get balance - Position Keeping Unavailable maps to Unavailable
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestUpdateInternalBankAccount_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)
	assert.Equal(t, "USD Clearing Account", createResp.Facility.Name)

	// Update account name
	updateResp, err := svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
		AccountId: createResp.Facility.AccountCode,
		Name:      "Updated USD Clearing Account",
	})
	require.NoError(t, err)
	assert.NotNil(t, updateResp.Facility)
	assert.Equal(t, "Updated USD Clearing Account", updateResp.Facility.Name)
	assert.Equal(t, int32(2), updateResp.Facility.Version) // Version should be bumped
}

func TestUpdateInternalBankAccount_VersionConflict(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account (version 1)
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)
	originalVersion := createResp.Facility.Version
	assert.Equal(t, int32(1), originalVersion)

	// Suspend the account - this bumps version to 2
	controlResp, err := svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Testing version conflict",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), controlResp.Facility.Version)

	// Try to update with stale version - should fail with Aborted
	_, err = svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
		AccountId:       createResp.Facility.AccountCode,
		Name:            "Update with stale version",
		ExpectedVersion: originalVersion, // Version 1 is now stale, current is 2
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestUpdateInternalBankAccount_ClosedAccount(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Create and close account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "USD Clearing Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Closing for update test",
	})
	require.NoError(t, err)

	// Try to update closed account - should fail
	_, err = svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
		AccountId: createResp.Facility.AccountCode,
		Name:      "Updated Name",
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

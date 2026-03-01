package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// mockRepository implements domain.Repository for testing.
type mockRepository struct {
	accounts        map[uuid.UUID]domain.InternalAccount
	accountsByCode  map[string]domain.InternalAccount
	saveErr         error
	findByIDErr     error
	findByCodeErr   error
	listErr         error
	existsByCodeErr error
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		accounts:       make(map[uuid.UUID]domain.InternalAccount),
		accountsByCode: make(map[string]domain.InternalAccount),
	}
}

func (m *mockRepository) Save(_ context.Context, account domain.InternalAccount) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.accounts[account.ID()] = account
	m.accountsByCode[account.AccountCode()] = account
	return nil
}

func (m *mockRepository) FindByID(_ context.Context, id uuid.UUID) (domain.InternalAccount, error) {
	if m.findByIDErr != nil {
		return domain.InternalAccount{}, m.findByIDErr
	}
	account, ok := m.accounts[id]
	if !ok {
		return domain.InternalAccount{}, domain.ErrAccountNotFound
	}
	return account, nil
}

func (m *mockRepository) FindByCode(_ context.Context, accountCode string) (domain.InternalAccount, error) {
	if m.findByCodeErr != nil {
		return domain.InternalAccount{}, m.findByCodeErr
	}
	account, ok := m.accountsByCode[accountCode]
	if !ok {
		return domain.InternalAccount{}, domain.ErrAccountNotFound
	}
	return account, nil
}

func (m *mockRepository) List(_ context.Context, _ domain.ListFilter) ([]domain.InternalAccount, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	accounts := make([]domain.InternalAccount, 0, len(m.accounts))
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

func (m *mockRepository) SaveInTx(ctx context.Context, account domain.InternalAccount, _ *gorm.DB) error {
	return m.Save(ctx, account)
}

// mockPositionKeepingClient implements PositionKeepingClient for testing.
type mockPositionKeepingClient struct {
	balances       []*positionkeepingv1.BalanceEntry
	err            error
	asOf           *timestamppb.Timestamp // optional override; nil means omit as_of from response
	asOfSet        bool                   // true when asOf was explicitly set (even to nil)
	singleBalance  *quantityv1.InstrumentAmount
	singleBalError error
}

func (m *mockPositionKeepingClient) GetAccountBalances(_ context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	asOf := timestamppb.Now()
	if m.asOfSet {
		asOf = m.asOf
	}
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances:  m.balances,
		AsOf:      asOf,
	}, nil
}

func (m *mockPositionKeepingClient) GetAccountBalance(_ context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	if m.singleBalError != nil {
		return nil, m.singleBalError
	}
	if m.singleBalance != nil {
		return &positionkeepingv1.GetAccountBalanceResponse{
			AccountId: req.AccountId,
			Amount:    m.singleBalance,
		}, nil
	}
	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId: req.AccountId,
		Amount: &quantityv1.InstrumentAmount{
			InstrumentCode: req.InstrumentCode,
			Amount:         "0",
		},
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

// standardTestDefs provides a basic set of product type definitions for tests that
// need to create accounts but are not testing product type resolution behavior.
var standardTestDefs = map[string]*accounttype.Definition{
	"CLEARING_GBP": {
		Code:           "CLEARING_GBP",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassClearing,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"CLEARING_USD": {
		Code:           "CLEARING_USD",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassClearing,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"CLEARING_EUR": {
		Code:           "CLEARING_EUR",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassClearing,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"HOLDING_GBP": {
		Code:           "HOLDING_GBP",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassHolding,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"HOLDING_EUR": {
		Code:           "HOLDING_EUR",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassHolding,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"HOLDING_USD": {
		Code:           "HOLDING_USD",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassHolding,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"INVENTORY_GBP": {
		Code:           "INVENTORY_GBP",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassInventory,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"NOSTRO_USD": {
		Code:           "NOSTRO_USD",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassNostro,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"NOSTRO_GBP": {
		Code:           "NOSTRO_GBP",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassNostro,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
	"VOSTRO_USD": {
		Code:           "VOSTRO_USD",
		Version:        1,
		BehaviorClass:  accounttype.BehaviorClassVostro,
		EligibilityCEL: "true",
		Status:         accounttype.StatusActive,
	},
}

// newTestServiceWithCache creates a service with a standard test cache for tests that
// need to create accounts but are not testing product type resolution behavior.
// All requests must include a tenant context (use testCtx()).
func newTestServiceWithCache(repo domain.Repository, opts ...Option) (*Service, error) {
	c := newTestCacheWithDefinitions(standardTestDefs)
	allOpts := append([]Option{WithAccountTypeCache(c)}, opts...)
	return NewServiceFull(repo, nil, nil, nil, nil, allOpts...)
}

// newTestServiceWithCacheAndPosClient creates a service with a cache and position keeping client.
func newTestServiceWithCacheAndPosClient(repo domain.Repository, posClient PositionKeepingClient) (*Service, error) {
	c := newTestCacheWithDefinitions(standardTestDefs)
	return NewServiceFull(repo, posClient, nil, nil, nil, WithAccountTypeCache(c))
}

// newTestServiceWithCacheAndRefClient creates a service with a cache and reference data client.
func newTestServiceWithCacheAndRefClient(repo domain.Repository, refClient ReferenceDataClient) (*Service, error) {
	c := newTestCacheWithDefinitions(standardTestDefs)
	return NewServiceFull(repo, nil, refClient, nil, nil, WithAccountTypeCache(c))
}

// testCtx returns a context with a standard test tenant.
func testCtx() context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant_server"))
}

func TestNewService_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
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

func TestInitiateInternalAccount_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccountId)
	assert.NotNil(t, resp.Facility)
	assert.Equal(t, "CLR-001", resp.Facility.AccountCode)
	assert.Equal(t, "USD Clearing Account", resp.Facility.Name)
	assert.Equal(t, "CLEARING", resp.Facility.BehaviorClass)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
}

func TestInitiateInternalAccount_WithCounterparty(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"NOSTRO_USD": {
			Code:           "NOSTRO_USD",
			Version:        1,
			BehaviorClass:  accounttype.BehaviorClassNostro,
			EligibilityCEL: "true",
			Status:         accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "NOSTRO-USD-HSBC",
		Name:            "HSBC USD Nostro",
		ProductTypeCode: "NOSTRO_USD",
		InstrumentCode:  "USD",
		CounterpartyDetails: &pb.CounterpartyDetails{
			CounterpartyId:          "HSBC001",
			CounterpartyName:        "HSBC Bank",
			CounterpartyExternalRef: "12345678",
			CounterpartyType:        pb.CounterpartyType_COUNTERPARTY_TYPE_NOSTRO,
		},
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, resp.Facility.CounterpartyDetails)
	assert.Equal(t, "HSBC001", resp.Facility.CounterpartyDetails.CounterpartyId)
	assert.Equal(t, "HSBC Bank", resp.Facility.CounterpartyDetails.CounterpartyName)
}

func TestInitiateInternalAccount_NostroWithoutCounterparty(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"NOSTRO_USD": {
			Code:           "NOSTRO_USD",
			Version:        1,
			BehaviorClass:  accounttype.BehaviorClassNostro,
			EligibilityCEL: "true",
			Status:         accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "NOSTRO-USD-HSBC",
		Name:            "HSBC USD Nostro",
		ProductTypeCode: "NOSTRO_USD",
		InstrumentCode:  "USD",
		// Missing CounterpartyDetails
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestInitiateInternalAccount_MissingProductTypeCode(t *testing.T) {
	// product_type_code is required; requests without it must be rejected
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:    "CLR-001",
		Name:           "Test Account",
		InstrumentCode: "USD",
		// ProductTypeCode intentionally omitted
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRetrieveInternalAccount_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// First create an account
	createReq := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	}
	createResp, err := svc.InitiateInternalAccount(ctx, createReq)
	require.NoError(t, err)

	// Then retrieve it by code
	retrieveResp, err := svc.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
		AccountId: "CLR-001",
	})
	require.NoError(t, err)
	assert.Equal(t, createResp.Facility.AccountCode, retrieveResp.Facility.AccountCode)
}

func TestRetrieveInternalAccount_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()
	resp, err := svc.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
		AccountId: "nonexistent",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestControlInternalAccount_Suspend(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Suspend the account
	controlResp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Testing suspension for compliance review",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED, controlResp.Facility.AccountStatus)
}

func TestControlInternalAccount_Close(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Close the account
	controlResp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account no longer needed after migration",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED, controlResp.Facility.AccountStatus)
}

func TestControlInternalAccount_InvalidTransition(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create and close account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Closing the account for testing",
	})
	require.NoError(t, err)

	// Try to activate a closed account - should fail
	_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		Reason:        "Trying to reactivate closed account",
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestListInternalAccounts_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create multiple accounts
	for i := 0; i < 3; i++ {
		_, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     "CLR-00" + string(rune('1'+i)),
			Name:            "Clearing Account " + string(rune('1'+i)),
			ProductTypeCode: "CLEARING_GBP",
			ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
			InstrumentCode:  "USD",
		})
		require.NoError(t, err)
	}

	// List all accounts
	resp, err := svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{})
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
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Get balance
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)
	assert.NotNil(t, balanceResp.CurrentBalance)
	assert.Equal(t, "USD", balanceResp.CurrentBalance.InstrumentCode)
	assert.Equal(t, "1000.00", balanceResp.CurrentBalance.Amount)
	assert.NotNil(t, balanceResp.AsOf)
}

func TestGetBalance_AccountSuspended(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create and suspend account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
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
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
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
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

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
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Close the account
	_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
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
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
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
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
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

func TestUpdateInternalAccount_Success(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)
	assert.Equal(t, "USD Clearing Account", createResp.Facility.Name)

	// Update account name
	updateResp, err := svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
		AccountId: createResp.Facility.AccountCode,
		Name:      "Updated USD Clearing Account",
	})
	require.NoError(t, err)
	assert.NotNil(t, updateResp.Facility)
	assert.Equal(t, "Updated USD Clearing Account", updateResp.Facility.Name)
	assert.Equal(t, int32(2), updateResp.Facility.Version) // Version should be bumped
}

func TestUpdateInternalAccount_VersionConflict(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account (version 1)
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)
	originalVersion := createResp.Facility.Version
	assert.Equal(t, int32(1), originalVersion)

	// Suspend the account - this bumps version to 2
	controlResp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Testing version conflict",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), controlResp.Facility.Version)

	// Try to update with stale version - should fail with Aborted
	_, err = svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
		AccountId:       createResp.Facility.AccountCode,
		Name:            "Update with stale version",
		ExpectedVersion: originalVersion, // Version 1 is now stale, current is 2
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestUpdateInternalAccount_ClosedAccount(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create and close account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Closing for update test",
	})
	require.NoError(t, err)

	// Try to update closed account - should fail
	_, err = svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
		AccountId: createResp.Facility.AccountCode,
		Name:      "Updated Name",
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// =============================================================================
// Reference Data Validation Tests
// =============================================================================

func TestInitiateInternalAccount_WithReferenceDataValidation_Success(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		instrument: &referencedatav1.InstrumentDefinition{
			Code:      "USD",
			Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
		},
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccountId)
	assert.Equal(t, "CLR-001", resp.Facility.AccountCode)

	// Verify dimension was correctly extracted and stored (DIMENSION_ prefix stripped)
	savedAccount, err := repo.FindByCode(ctx, "CLR-001")
	require.NoError(t, err)
	assert.Equal(t, "CURRENCY", savedAccount.Dimension(), "dimension should be stripped of DIMENSION_ prefix")
}

func TestInitiateInternalAccount_InstrumentNotFound(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		err: status.Error(codes.NotFound, "instrument not found"),
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "Test Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "INVALID",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "instrument not found")
}

func TestInitiateInternalAccount_InstrumentNotActive(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		instrument: &referencedatav1.InstrumentDefinition{
			Code:      "DRAFT_COIN",
			Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT,
			Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
		},
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "Test Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "DRAFT_COIN",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "is not active")
}

func TestInitiateInternalAccount_InstrumentDeprecated(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		instrument: &referencedatav1.InstrumentDefinition{
			Code:      "OLD_COIN",
			Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,
			Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
		},
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "Test Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "OLD_COIN",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "is not active")
}

func TestInitiateInternalAccount_ReferenceDataServiceUnavailable(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		err: status.Error(codes.Unavailable, "service unavailable"),
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "Test Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to validate instrument")
}

func TestInitiateInternalAccount_ReferenceDataTimeout(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		err: status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "Test Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, st.Code())
	assert.Contains(t, st.Message(), "timed out")
}

func TestInitiateInternalAccount_NilInstrumentInResponse(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		instrument: nil, // Simulate malformed response
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "Test Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "invalid response")
}

func TestInitiateInternalAccount_EnergyInstrument(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		instrument: &referencedatav1.InstrumentDefinition{
			Code:      "KWH",
			Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			Dimension: referencedatav1.Dimension_DIMENSION_ENERGY,
		},
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "INV-ENERGY-001",
		Name:            "Energy Inventory Account",
		ProductTypeCode: "INVENTORY_GBP",
		InstrumentCode:  "KWH",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccountId)
	assert.Equal(t, "INV-ENERGY-001", resp.Facility.AccountCode)
}

func TestInitiateInternalAccount_ComputeInstrument(t *testing.T) {
	repo := newMockRepository()
	refClient := &mockReferenceDataClient{
		instrument: &referencedatav1.InstrumentDefinition{
			Code:      "GPU_HOUR",
			Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			Dimension: referencedatav1.Dimension_DIMENSION_COMPUTE,
		},
	}

	svc, err := newTestServiceWithCacheAndRefClient(repo, refClient)
	require.NoError(t, err)

	ctx := testCtx()
	req := &pb.InitiateInternalAccountRequest{
		AccountCode:     "INV-COMPUTE-001",
		Name:            "GPU Compute Inventory",
		ProductTypeCode: "INVENTORY_GBP",
		InstrumentCode:  "GPU_HOUR",
	}

	resp, err := svc.InitiateInternalAccount(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccountId)
	assert.Equal(t, "INV-COMPUTE-001", resp.Facility.AccountCode)
}

// =============================================================================
// Additional Comprehensive Coverage Tests (per subtask 15.3)
// =============================================================================

// Test sentinel errors for use in mock error scenarios.
var (
	errDatabaseConnection = errors.New("database connection failed")
	errDatabaseQuery      = errors.New("database query failed")
)

func TestInitiateInternalAccount_DuplicateCode(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create first account
	_, err = svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Simulate duplicate code error from repository
	repo.saveErr = persistence.ErrDuplicateCode

	// Try to create account with same code - should fail
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "Another USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
	assert.Contains(t, st.Message(), "account code already exists")
}

func TestInitiateInternalAccount_RepositoryError(t *testing.T) {
	repo := newMockRepository()
	repo.saveErr = errDatabaseConnection
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Try to create account when repository has error
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to create account")
}

func TestUpdateInternalAccount_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Try to update non-existent account
	resp, err := svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
		AccountId: "non-existent-account",
		Name:      "Updated Name",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdateInternalAccount_RepositorySaveError(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Set save error for the update
	repo.saveErr = persistence.ErrVersionConflict

	// Try to update - should fail with Aborted due to version conflict
	resp, err := svc.UpdateInternalAccount(ctx, &pb.UpdateInternalAccountRequest{
		AccountId: createResp.Facility.AccountCode,
		Name:      "Updated Name",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestControlInternalAccount_Reactivate(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, createResp.Facility.AccountStatus)

	// Suspend the account
	suspendResp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Temporary suspension for maintenance",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED, suspendResp.Facility.AccountStatus)

	// Reactivate the account
	reactivateResp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		Reason:        "Maintenance complete, reactivating account",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, reactivateResp.Facility.AccountStatus)
	assert.NotNil(t, reactivateResp.ActionTimestamp)
}

func TestControlInternalAccount_NotFound(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Try to control non-existent account
	resp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     "non-existent-account",
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Testing",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestControlInternalAccount_UnspecifiedAction(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Try to control with unspecified action
	resp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_UNSPECIFIED,
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "control action must be specified")
}

func TestControlInternalAccount_RepositorySaveError(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Set save error
	repo.saveErr = persistence.ErrVersionConflict

	// Try to suspend - should fail due to version conflict
	resp, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Testing version conflict",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestListInternalAccounts_WithFilters(t *testing.T) {
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Create accounts with different types
	_, err = svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	_, err = svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "HOLD-001",
		Name:            "EUR Holding Account",
		ProductTypeCode: "HOLDING_EUR",
		InstrumentCode:  "EUR",
	})
	require.NoError(t, err)

	// List with type filter - should get all accounts since mock doesn't filter
	resp, err := svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{
		BehaviorClassFilter: "CLEARING",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.GreaterOrEqual(t, len(resp.Facilities), 1)
}

func TestListInternalAccounts_RepositoryError(t *testing.T) {
	repo := newMockRepository()
	repo.listErr = errDatabaseQuery
	svc, err := newTestServiceWithCache(repo)
	require.NoError(t, err)

	ctx := testCtx()

	// Try to list - should fail
	resp, err := svc.ListInternalAccounts(ctx, &pb.ListInternalAccountsRequest{})
	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetBalance_ZeroBalance(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			// No current balance entry - should return zero balance
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Get balance - should return nil balance when no current balance found
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)
	assert.Nil(t, balanceResp.CurrentBalance)
	assert.NotNil(t, balanceResp.AsOf)
}

func TestGetBalance_PositionKeepingNotFound(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.NotFound, "position not found"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Get balance - Position Keeping NotFound maps to Internal
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetBalance_PositionKeepingDeadlineExceeded(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.DeadlineExceeded, "request timeout"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Get balance - DeadlineExceeded maps to Unavailable
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestGetBalance_PositionKeepingResourceExhausted(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.ResourceExhausted, "rate limit exceeded"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Get balance - ResourceExhausted maps to Unavailable
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

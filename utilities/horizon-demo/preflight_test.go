package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	money "google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// errGenericTest is a static error for testing non-gRPC error handling.
var errGenericTest = errors.New("generic error")

func TestGenerateTestAccountID(t *testing.T) {
	id1 := GenerateTestAccountID()
	id2 := GenerateTestAccountID()

	// Should have the correct prefix
	assert.True(t, strings.HasPrefix(id1, "HORIZON-TEST-"), "ID should start with HORIZON-TEST-")

	// Should be alphanumeric with hyphens (valid for proto validation)
	assert.Regexp(t, `^HORIZON-TEST-\d+$`, id1)

	// IDs generated in quick succession may be the same (within same second)
	// This is acceptable for a demo tool
	_ = id2
}

func TestGenerateTestIBAN(t *testing.T) {
	timestamp := int64(1701607800)
	iban := GenerateTestIBAN(timestamp)

	// Should start with GB country code
	assert.True(t, strings.HasPrefix(iban, "GB82WEST"), "IBAN should start with GB82WEST")

	// Should match IBAN pattern (2 letter country + 2 check digits + up to 30 alphanumeric)
	assert.Regexp(t, `^[A-Z]{2}[0-9]{2}[A-Z0-9]{1,30}$`, iban)

	// Different timestamps should produce different IBANs
	iban2 := GenerateTestIBAN(timestamp + 1)
	assert.NotEqual(t, iban, iban2)
}

func TestPenceToPounds(t *testing.T) {
	tests := []struct {
		name      string
		pence     int64
		wantUnits int64
		wantNanos int32
	}{
		{
			name:      "GBP 1,000.00",
			pence:     100000,
			wantUnits: 1000,
			wantNanos: 0,
		},
		{
			name:      "GBP 100.00",
			pence:     10000,
			wantUnits: 100,
			wantNanos: 0,
		},
		{
			name:      "GBP 0.01",
			pence:     1,
			wantUnits: 0,
			wantNanos: 10000000,
		},
		{
			name:      "GBP 123.45",
			pence:     12345,
			wantUnits: 123,
			wantNanos: 450000000,
		},
		{
			name:      "zero",
			pence:     0,
			wantUnits: 0,
			wantNanos: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := penceToPounds(tt.pence)
			assert.Equal(t, "GBP", result.GetCurrencyCode())
			assert.Equal(t, tt.wantUnits, result.GetUnits())
			assert.Equal(t, tt.wantNanos, result.GetNanos())
		})
	}
}

func TestMoneyToPence(t *testing.T) {
	tests := []struct {
		name      string
		money     *money.Money
		wantPence int64
	}{
		{
			name: "GBP 1,000.00",
			money: &money.Money{
				CurrencyCode: "GBP",
				Units:        1000,
				Nanos:        0,
			},
			wantPence: 100000,
		},
		{
			name: "GBP 123.45",
			money: &money.Money{
				CurrencyCode: "GBP",
				Units:        123,
				Nanos:        450000000,
			},
			wantPence: 12345,
		},
		{
			name: "GBP 0.01",
			money: &money.Money{
				CurrencyCode: "GBP",
				Units:        0,
				Nanos:        10000000,
			},
			wantPence: 1,
		},
		{
			name:      "nil money",
			money:     nil,
			wantPence: 0,
		},
		{
			name: "zero",
			money: &money.Money{
				CurrencyCode: "GBP",
				Units:        0,
				Nanos:        0,
			},
			wantPence: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := moneyToPence(tt.money)
			assert.Equal(t, tt.wantPence, result)
		})
	}
}

func TestMoneyConversionRoundTrip(t *testing.T) {
	// Test that pence -> money -> pence is lossless
	testCases := []int64{0, 1, 50, 99, 100, 12345, 100000, 999999}

	for _, pence := range testCases {
		money := penceToPounds(pence)
		result := moneyToPence(money)
		assert.Equal(t, pence, result, "Round trip failed for %d pence", pence)
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name:      "nil error",
			err:       nil,
			wantRetry: false,
		},
		{
			name:      "unavailable",
			err:       status.Error(codes.Unavailable, "service unavailable"),
			wantRetry: true,
		},
		{
			name:      "resource exhausted",
			err:       status.Error(codes.ResourceExhausted, "rate limited"),
			wantRetry: true,
		},
		{
			name:      "aborted",
			err:       status.Error(codes.Aborted, "transaction aborted"),
			wantRetry: true,
		},
		{
			name:      "deadline exceeded",
			err:       status.Error(codes.DeadlineExceeded, "timeout"),
			wantRetry: true,
		},
		{
			name:      "not found",
			err:       status.Error(codes.NotFound, "not found"),
			wantRetry: false,
		},
		{
			name:      "invalid argument",
			err:       status.Error(codes.InvalidArgument, "bad request"),
			wantRetry: false,
		},
		{
			name:      "permission denied",
			err:       status.Error(codes.PermissionDenied, "forbidden"),
			wantRetry: false,
		},
		{
			name:      "non-grpc error",
			err:       errGenericTest,
			wantRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryableError(tt.err)
			assert.Equal(t, tt.wantRetry, result)
		})
	}
}

// mockCurrentAccountClient is a test double for CurrentAccountServiceClient
type mockCurrentAccountClient struct {
	currentaccountv1.CurrentAccountServiceClient

	initiateFunc func(ctx context.Context, req *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error)
	depositFunc  func(ctx context.Context, req *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error)
	retrieveFunc func(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error)
}

func (m *mockCurrentAccountClient) InitiateCurrentAccount(ctx context.Context, req *currentaccountv1.InitiateCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
	if m.initiateFunc != nil {
		return m.initiateFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCurrentAccountClient) ExecuteDeposit(ctx context.Context, req *currentaccountv1.ExecuteDepositRequest, _ ...grpc.CallOption) (*currentaccountv1.ExecuteDepositResponse, error) {
	if m.depositFunc != nil {
		return m.depositFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCurrentAccountClient) RetrieveCurrentAccount(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
	if m.retrieveFunc != nil {
		return m.retrieveFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestRunPreFlight_Success(t *testing.T) {
	mockClient := &mockCurrentAccountClient{
		initiateFunc: func(_ context.Context, _ *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			return &currentaccountv1.InitiateCurrentAccountResponse{
				AccountId: "test-account-123",
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId:     "test-account-123",
					AccountStatus: currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
				},
			}, nil
		},
		depositFunc: func(_ context.Context, req *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error) {
			return &currentaccountv1.ExecuteDepositResponse{
				AccountId:     req.GetAccountId(),
				TransactionId: "txn-deposit-123",
				NewBalance: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        1000,
						Nanos:        0,
					},
				},
				Status: currentaccountv1.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
			}, nil
		},
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId:     req.GetAccountId(),
					AccountStatus: currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
					CurrentBalance: &currentaccountv1.AccountBalance{
						CurrentBalance: &commonv1.MoneyAmount{
							Amount: &money.Money{
								CurrencyCode: "GBP",
								Units:        1000,
								Nanos:        0,
							},
						},
						LastUpdated: timestamppb.Now(),
					},
					CreatedAt: timestamppb.Now(),
				},
			}, nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	ctx := context.Background()
	cfg := &PreFlightConfig{
		InitialDepositPence: 100000, // GBP 1,000.00
		Logger:              slog.Default(),
	}

	result, err := RunPreFlight(ctx, clients, cfg)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "test-account-123", result.AccountID)
	assert.Equal(t, int64(100000), result.InitialBalancePence)
	assert.Equal(t, "txn-deposit-123", result.DepositTransactionID)
	assert.NotEmpty(t, result.AccountIdentification)
}

func TestRunPreFlight_NilClients(t *testing.T) {
	ctx := context.Background()
	result, err := RunPreFlight(ctx, nil, nil)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAccountCreationFailed))
}

func TestRunPreFlight_AccountCreationFails(t *testing.T) {
	mockClient := &mockCurrentAccountClient{
		initiateFunc: func(_ context.Context, _ *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			return nil, status.Error(codes.Internal, "database error")
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	ctx := context.Background()
	result, err := RunPreFlight(ctx, clients, nil)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAccountCreationFailed))
}

func TestRunPreFlight_DepositFails(t *testing.T) {
	mockClient := &mockCurrentAccountClient{
		initiateFunc: func(_ context.Context, _ *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			return &currentaccountv1.InitiateCurrentAccountResponse{
				AccountId: "test-account-123",
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId:     "test-account-123",
					AccountStatus: currentaccountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
				},
			}, nil
		},
		depositFunc: func(_ context.Context, _ *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	ctx := context.Background()
	result, err := RunPreFlight(ctx, clients, nil)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDepositFailed))
}

func TestRunPreFlight_DepositNotCompleted(t *testing.T) {
	mockClient := &mockCurrentAccountClient{
		initiateFunc: func(_ context.Context, _ *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			return &currentaccountv1.InitiateCurrentAccountResponse{
				AccountId: "test-account-123",
				Facility:  &currentaccountv1.CurrentAccountFacility{},
			}, nil
		},
		depositFunc: func(_ context.Context, _ *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error) {
			return &currentaccountv1.ExecuteDepositResponse{
				Status: currentaccountv1.TransactionStatus_TRANSACTION_STATUS_PENDING, // Not COMPLETED
			}, nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	ctx := context.Background()
	result, err := RunPreFlight(ctx, clients, nil)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDepositFailed))
	assert.Contains(t, err.Error(), "PENDING")
}

func TestRunPreFlight_BalanceVerificationFails(t *testing.T) {
	mockClient := &mockCurrentAccountClient{
		initiateFunc: func(_ context.Context, _ *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			return &currentaccountv1.InitiateCurrentAccountResponse{
				AccountId: "test-account-123",
				Facility:  &currentaccountv1.CurrentAccountFacility{},
			}, nil
		},
		depositFunc: func(_ context.Context, _ *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error) {
			return &currentaccountv1.ExecuteDepositResponse{
				TransactionId: "txn-123",
				Status:        currentaccountv1.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
			}, nil
		},
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "account not found")
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	ctx := context.Background()
	result, err := RunPreFlight(ctx, clients, nil)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBalanceVerificationFailed))
}

func TestRunPreFlight_BalanceMismatch(t *testing.T) {
	mockClient := &mockCurrentAccountClient{
		initiateFunc: func(_ context.Context, _ *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			return &currentaccountv1.InitiateCurrentAccountResponse{
				AccountId: "test-account-123",
				Facility:  &currentaccountv1.CurrentAccountFacility{},
			}, nil
		},
		depositFunc: func(_ context.Context, _ *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error) {
			return &currentaccountv1.ExecuteDepositResponse{
				TransactionId: "txn-123",
				Status:        currentaccountv1.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
			}, nil
		},
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					CurrentBalance: &currentaccountv1.AccountBalance{
						CurrentBalance: &commonv1.MoneyAmount{
							Amount: &money.Money{
								CurrencyCode: "GBP",
								Units:        500, // Only 500 GBP instead of 1000
								Nanos:        0,
							},
						},
						LastUpdated: timestamppb.Now(),
					},
				},
			}, nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	ctx := context.Background()
	cfg := &PreFlightConfig{
		InitialDepositPence: 100000, // Expecting 1000 GBP
		Logger:              slog.Default(),
	}

	result, err := RunPreFlight(ctx, clients, cfg)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidBalanceAmount))
	assert.Contains(t, err.Error(), "expected 100000 pence")
	assert.Contains(t, err.Error(), "got 50000 pence")
}

func TestRunPreFlight_DefaultConfig(t *testing.T) {
	mockClient := &mockCurrentAccountClient{
		initiateFunc: func(_ context.Context, req *currentaccountv1.InitiateCurrentAccountRequest) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
			// Verify GBP instrument code is used
			assert.Equal(t, "GBP", req.GetInstrumentCode())
			return &currentaccountv1.InitiateCurrentAccountResponse{
				AccountId: "test-account",
				Facility:  &currentaccountv1.CurrentAccountFacility{},
			}, nil
		},
		depositFunc: func(_ context.Context, req *currentaccountv1.ExecuteDepositRequest) (*currentaccountv1.ExecuteDepositResponse, error) {
			// Verify default deposit amount (1000 GBP = 100000 pence)
			assert.Equal(t, int64(1000), req.GetAmount().GetAmount().GetUnits())
			return &currentaccountv1.ExecuteDepositResponse{
				TransactionId: "txn-123",
				Status:        currentaccountv1.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
			}, nil
		},
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					CurrentBalance: &currentaccountv1.AccountBalance{
						CurrentBalance: &commonv1.MoneyAmount{
							Amount: &money.Money{
								CurrencyCode: "GBP",
								Units:        1000,
								Nanos:        0,
							},
						},
						LastUpdated: timestamppb.Now(),
					},
				},
			}, nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	ctx := context.Background()
	// Pass nil config to use defaults
	result, err := RunPreFlight(ctx, clients, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(100000), result.InitialBalancePence)
}

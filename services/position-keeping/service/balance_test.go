package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
)

// MockCurrentAccountClient is a mock implementation of CurrentAccountClient
type MockCurrentAccountClient struct {
	mock.Mock
}

func (m *MockCurrentAccountClient) GetActiveAmountBlocks(ctx context.Context, accountID string) ([]domain.AmountBlock, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.AmountBlock), args.Error(1)
}

// createTestLogWithEntries creates a test FinancialPositionLog with transaction entries.
// Uses DEBIT entries to add to the balance (since DEBIT = add, CREDIT = subtract in this model).
func createTestLogWithEntries(t *testing.T, accountID string, amount decimal.Decimal, currency domain.Currency) *domain.FinancialPositionLog {
	t.Helper()

	// For positive amounts, use DEBIT entries (which add to balance)
	// For negative amounts, use CREDIT entries (which subtract from balance)
	var direction domain.PostingDirection
	var entryAmount decimal.Decimal
	if amount.IsNegative() {
		direction = domain.PostingDirectionCredit
		entryAmount = amount.Neg()
	} else {
		direction = domain.PostingDirectionDebit
		entryAmount = amount
	}

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(entryAmount, currency),
		direction,
		time.Now(),
		"test transaction",
		"REF-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, entry, nil)
	require.NoError(t, err)
	return log
}

// TestGetAccountBalance_Success tests successful balance retrieval for all 7 balance types
func TestGetAccountBalance_Success(t *testing.T) {
	tests := []struct {
		name         string
		balanceType  positionkeepingv1.BalanceType
		setupMocks   func(*MockRepository, *MockCurrentAccountClient)
		expectAmount int64 // Expected units (simplified for testing)
	}{
		{
			name:        "returns opening balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
			setupMocks: func(repo *MockRepository, _ *MockCurrentAccountClient) {
				// Log with DEBIT 1000 entry (adds to balance)
				log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
				repo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
			},
			expectAmount: 0, // Opening balance is 0 (passed to LogBalanceComputer)
		},
		{
			name:        "returns current balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			setupMocks: func(repo *MockRepository, _ *MockCurrentAccountClient) {
				// Log with DEBIT 500 entry (adds to balance)
				log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(500), domain.CurrencyGBP)
				repo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
			},
			expectAmount: 500, // 0 (opening) + 500 (DEBIT entry) = 500
		},
		{
			name:        "returns closing balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING,
			setupMocks: func(repo *MockRepository, _ *MockCurrentAccountClient) {
				// Log with DEBIT 750 entry
				log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(750), domain.CurrencyGBP)
				repo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
			},
			expectAmount: 750, // Closing balance at current time
		},
		{
			name:        "returns ledger balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
			setupMocks: func(repo *MockRepository, _ *MockCurrentAccountClient) {
				// Log with DEBIT 800 entry
				log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(800), domain.CurrencyGBP)
				repo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
			},
			expectAmount: 800, // Sum of entries (ledger balance ignores opening)
		},
		{
			name:        "returns reserve balance with zero liens",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
			setupMocks: func(repo *MockRepository, client *MockCurrentAccountClient) {
				log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
				repo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
				client.On("GetActiveAmountBlocks", mock.Anything, "test-account").Return([]domain.AmountBlock{}, nil)
			},
			expectAmount: 0, // No liens
		},
		{
			name:        "returns available balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
			setupMocks: func(repo *MockRepository, client *MockCurrentAccountClient) {
				log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
				repo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
				client.On("GetActiveAmountBlocks", mock.Anything, "test-account").Return([]domain.AmountBlock{}, nil)
			},
			expectAmount: 1000, // Current (1000) - Reserve (0) + Overdraft (0)
		},
		{
			name:        "returns free balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
			setupMocks: func(repo *MockRepository, client *MockCurrentAccountClient) {
				log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
				repo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
				client.On("GetActiveAmountBlocks", mock.Anything, "test-account").Return([]domain.AmountBlock{}, nil)
			},
			expectAmount: 1000, // Current (1000) - Reserve (0)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockRepo := new(MockRepository)
			mockMeasurementRepo := new(MockMeasurementRepository)
			mockEventPublisher := domain.NewInMemoryEventPublisher()
			mockIdempotency := new(MockIdempotencyService)
			mockCurrentAccount := new(MockCurrentAccountClient)

			tt.setupMocks(mockRepo, mockCurrentAccount)

			svc, err := service.NewPositionKeepingService(
				mockRepo,
				mockMeasurementRepo,
				mockEventPublisher,
				mockIdempotency,
				newTestOutboxPublisher(t),
				service.WithCurrentAccountClient(mockCurrentAccount),
			)
			require.NoError(t, err)

			req := &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   "test-account",
				BalanceType: tt.balanceType,
			}

			// Act
			resp, err := svc.GetAccountBalance(context.Background(), req)

			// Assert
			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, "test-account", resp.AccountId)
			assert.Equal(t, tt.balanceType, resp.BalanceType)
			assert.NotNil(t, resp.Amount)
			assert.NotNil(t, resp.AsOf)
			// InstrumentAmount uses string representation, convert expected units for comparison
			expectedAmount := decimal.NewFromInt(tt.expectAmount).StringFixed(2)
			assert.Equal(t, expectedAmount, resp.Amount.Amount)
			mockRepo.AssertExpectations(t)
			mockCurrentAccount.AssertExpectations(t)
		})
	}
}

// TestGetAccountBalance_ValidationErrors tests validation error handling
func TestGetAccountBalance_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		req         *positionkeepingv1.GetAccountBalanceRequest
		expectedErr codes.Code
		errContains string
	}{
		{
			name: "returns InvalidArgument for empty account_id",
			req: &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   "",
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			},
			expectedErr: codes.InvalidArgument,
			errContains: "account_id",
		},
		{
			name: "returns InvalidArgument for UNSPECIFIED balance type",
			req: &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   "test-account",
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED,
			},
			expectedErr: codes.InvalidArgument,
			errContains: "balance_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockRepo := new(MockRepository)
			mockMeasurementRepo := new(MockMeasurementRepository)
			mockEventPublisher := domain.NewInMemoryEventPublisher()
			mockIdempotency := new(MockIdempotencyService)

			svc, err := service.NewPositionKeepingService(
				mockRepo,
				mockMeasurementRepo,
				mockEventPublisher,
				mockIdempotency,
				newTestOutboxPublisher(t),
			)
			require.NoError(t, err)

			// Act
			resp, err := svc.GetAccountBalance(context.Background(), tt.req)

			// Assert
			assert.Nil(t, resp)
			assert.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.expectedErr, st.Code())
			assert.Contains(t, st.Message(), tt.errContains)
		})
	}
}

// TestGetAccountBalance_NotFound tests account not found scenarios
func TestGetAccountBalance_NotFound(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockRepository)
	}{
		{
			name: "returns NotFound when repository returns ErrNotFound",
			setupMocks: func(repo *MockRepository) {
				repo.On("FindByAccountID", mock.Anything, "nonexistent-account").Return(nil, domain.ErrNotFound)
			},
		},
		{
			name: "returns NotFound when no logs exist for account",
			setupMocks: func(repo *MockRepository) {
				repo.On("FindByAccountID", mock.Anything, "nonexistent-account").Return([]*domain.FinancialPositionLog{}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockRepo := new(MockRepository)
			mockMeasurementRepo := new(MockMeasurementRepository)
			mockEventPublisher := domain.NewInMemoryEventPublisher()
			mockIdempotency := new(MockIdempotencyService)

			tt.setupMocks(mockRepo)

			svc, err := service.NewPositionKeepingService(
				mockRepo,
				mockMeasurementRepo,
				mockEventPublisher,
				mockIdempotency,
				newTestOutboxPublisher(t),
			)
			require.NoError(t, err)

			req := &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   "nonexistent-account",
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			}

			// Act
			resp, err := svc.GetAccountBalance(context.Background(), req)

			// Assert
			assert.Nil(t, resp)
			assert.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.NotFound, st.Code())
			mockRepo.AssertExpectations(t)
		})
	}
}

// TestGetAccountBalance_CurrencyFilter tests currency filtering
func TestGetAccountBalance_CurrencyFilter(t *testing.T) {
	t.Run("returns balance when currency matches", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockRepository)
		mockMeasurementRepo := new(MockMeasurementRepository)
		mockEventPublisher := domain.NewInMemoryEventPublisher()
		mockIdempotency := new(MockIdempotencyService)

		log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
		mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)

		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
		)
		require.NoError(t, err)

		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      "test-account",
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
			InstrumentCode: "GBP",
		}

		// Act
		resp, err := svc.GetAccountBalance(context.Background(), req)

		// Assert
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "GBP", resp.Amount.InstrumentCode)
	})

	t.Run("returns NotFound when instrument code does not match", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockRepository)
		mockMeasurementRepo := new(MockMeasurementRepository)
		mockEventPublisher := domain.NewInMemoryEventPublisher()
		mockIdempotency := new(MockIdempotencyService)

		log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
		mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)

		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
		)
		require.NoError(t, err)

		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      "test-account",
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
			InstrumentCode: "USD", // Different instrument
		}

		// Act
		resp, err := svc.GetAccountBalance(context.Background(), req)

		// Assert
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "USD")
	})
}

// TestGetAccountBalance_RepositoryFailure tests repository error handling
func TestGetAccountBalance_RepositoryFailure(t *testing.T) {
	// Arrange
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return(nil, assert.AnError)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:   "test-account",
		BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
	}

	// Act
	resp, err := svc.GetAccountBalance(context.Background(), req)

	// Assert
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// TestGetAccountBalance_NoCurrentAccountClient tests behavior when CurrentAccountClient is not configured
func TestGetAccountBalance_NoCurrentAccountClient(t *testing.T) {
	balanceTypesRequiringClient := []positionkeepingv1.BalanceType{
		positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
	}

	for _, bt := range balanceTypesRequiringClient {
		t.Run(bt.String(), func(t *testing.T) {
			// Arrange
			mockRepo := new(MockRepository)
			mockMeasurementRepo := new(MockMeasurementRepository)
			mockEventPublisher := domain.NewInMemoryEventPublisher()
			mockIdempotency := new(MockIdempotencyService)

			log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
			mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)

			// Create service WITHOUT CurrentAccountClient
			svc, err := service.NewPositionKeepingService(
				mockRepo,
				mockMeasurementRepo,
				mockEventPublisher,
				mockIdempotency,
				newTestOutboxPublisher(t),
			)
			require.NoError(t, err)

			req := &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   "test-account",
				BalanceType: bt,
			}

			// Act
			resp, err := svc.GetAccountBalance(context.Background(), req)

			// Assert
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.FailedPrecondition, st.Code())
			assert.Contains(t, st.Message(), "current account client")
		})
	}
}

// TestGetAccountBalances_Success tests successful retrieval of all balance types
func TestGetAccountBalances_Success(t *testing.T) {
	// Arrange
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCurrentAccount := new(MockCurrentAccountClient)

	log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
	mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)
	mockCurrentAccount.On("GetActiveAmountBlocks", mock.Anything, "test-account").Return([]domain.AmountBlock{}, nil)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithCurrentAccountClient(mockCurrentAccount),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: "test-account",
	}

	// Act
	resp, err := svc.GetAccountBalances(context.Background(), req)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "test-account", resp.AccountId)
	assert.NotNil(t, resp.AsOf)
	// Should return all 7 balance types when CurrentAccountClient is configured
	assert.Len(t, resp.Balances, 7)

	// Verify balance types are present
	balanceTypes := make(map[positionkeepingv1.BalanceType]bool)
	for _, b := range resp.Balances {
		balanceTypes[b.BalanceType] = true
	}
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_FREE])
}

// TestGetAccountBalances_WithoutCurrentAccountClient tests GetAccountBalances without CurrentAccountClient
func TestGetAccountBalances_WithoutCurrentAccountClient(t *testing.T) {
	// Arrange
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
	mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)

	// Create service WITHOUT CurrentAccountClient
	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: "test-account",
	}

	// Act
	resp, err := svc.GetAccountBalances(context.Background(), req)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, resp)
	// Should return only 4 balance types (Opening, Closing, Current, Ledger)
	// Reserve, Available, Free require CurrentAccountClient
	assert.Len(t, resp.Balances, 4)

	balanceTypes := make(map[positionkeepingv1.BalanceType]bool)
	for _, b := range resp.Balances {
		balanceTypes[b.BalanceType] = true
	}
	// These should be present
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT])
	assert.True(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER])
	// These should NOT be present (require CurrentAccountClient)
	assert.False(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE])
	assert.False(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE])
	assert.False(t, balanceTypes[positionkeepingv1.BalanceType_BALANCE_TYPE_FREE])
}

// TestGetAccountBalances_ValidationErrors tests validation error handling
func TestGetAccountBalances_ValidationErrors(t *testing.T) {
	// Arrange
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: "", // Empty account_id
	}

	// Act
	resp, err := svc.GetAccountBalances(context.Background(), req)

	// Assert
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id")
}

// TestGetAccountBalances_CurrencyFilter tests currency filtering for GetAccountBalances
func TestGetAccountBalances_CurrencyFilter(t *testing.T) {
	t.Run("returns balances when currency matches", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockRepository)
		mockMeasurementRepo := new(MockMeasurementRepository)
		mockEventPublisher := domain.NewInMemoryEventPublisher()
		mockIdempotency := new(MockIdempotencyService)

		log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
		mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)

		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
		)
		require.NoError(t, err)

		req := &positionkeepingv1.GetAccountBalancesRequest{
			AccountId:      "test-account",
			InstrumentCode: "GBP",
		}

		// Act
		resp, err := svc.GetAccountBalances(context.Background(), req)

		// Assert
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.NotEmpty(t, resp.Balances)
	})

	t.Run("returns NotFound when instrument code does not match", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockRepository)
		mockMeasurementRepo := new(MockMeasurementRepository)
		mockEventPublisher := domain.NewInMemoryEventPublisher()
		mockIdempotency := new(MockIdempotencyService)

		log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
		mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)

		svc, err := service.NewPositionKeepingService(
			mockRepo,
			mockMeasurementRepo,
			mockEventPublisher,
			mockIdempotency,
			newTestOutboxPublisher(t),
		)
		require.NoError(t, err)

		req := &positionkeepingv1.GetAccountBalancesRequest{
			AccountId:      "test-account",
			InstrumentCode: "EUR", // Different instrument
		}

		// Act
		resp, err := svc.GetAccountBalances(context.Background(), req)

		// Assert
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

// TestGetAccountBalances_NotFound tests not found scenarios for GetAccountBalances
func TestGetAccountBalances_NotFound(t *testing.T) {
	// Arrange
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	mockRepo.On("FindByAccountID", mock.Anything, "nonexistent-account").Return([]*domain.FinancialPositionLog{}, nil)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: "nonexistent-account",
	}

	// Act
	resp, err := svc.GetAccountBalances(context.Background(), req)

	// Assert
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestGetAccountBalance_WithLiens tests reserve balance computation with active liens
func TestGetAccountBalance_WithLiens(t *testing.T) {
	// Arrange
	mockRepo := new(MockRepository)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockCurrentAccount := new(MockCurrentAccountClient)

	log := createTestLogWithEntries(t, "test-account", decimal.NewFromInt(1000), domain.CurrencyGBP)
	mockRepo.On("FindByAccountID", mock.Anything, "test-account").Return([]*domain.FinancialPositionLog{log}, nil)

	// Configure mock to return liens totaling 200 GBP
	liens := []domain.AmountBlock{
		{
			BlockID:   "lien-1",
			Amount:    domain.MustNewMoney(decimal.NewFromInt(100), domain.CurrencyGBP),
			BlockType: domain.AmountBlockTypePending,
			Purpose:   "Payment Order hold",
		},
		{
			BlockID:   "lien-2",
			Amount:    domain.MustNewMoney(decimal.NewFromInt(100), domain.CurrencyGBP),
			BlockType: domain.AmountBlockTypeTemporary,
			Purpose:   "Authorization hold",
		},
	}
	mockCurrentAccount.On("GetActiveAmountBlocks", mock.Anything, "test-account").Return(liens, nil)

	svc, err := service.NewPositionKeepingService(
		mockRepo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithCurrentAccountClient(mockCurrentAccount),
	)
	require.NoError(t, err)

	req := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:   "test-account",
		BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
	}

	// Act
	resp, err := svc.GetAccountBalance(context.Background(), req)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "200.00", resp.Amount.Amount) // Total liens = 200
}

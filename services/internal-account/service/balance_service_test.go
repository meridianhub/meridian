package service

import (
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Balance service tests for Position Keeping integration as specified in subtask 15.3.
// These tests verify the interaction between InternalAccount service and Position Keeping client.

func TestBalanceService_GetBalance_ReturnsCurrentBalance(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "10000.50",
				},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "9500.00",
				},
			},
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create an active account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-USD-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_USD",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Query balance
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)

	// Verify current balance is returned (not available or other types)
	assert.NotNil(t, balanceResp.CurrentBalance)
	assert.Equal(t, "USD", balanceResp.CurrentBalance.InstrumentCode)
	assert.Equal(t, "10000.50", balanceResp.CurrentBalance.Amount)
	assert.NotNil(t, balanceResp.AsOf)
}

func TestBalanceService_GetBalance_MultiCurrency(t *testing.T) {
	// Test balance query for a EUR account
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "EUR",
					Amount:         "50000.00",
				},
			},
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create EUR account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-EUR-001",
		Name:            "EUR Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "EUR",
	})
	require.NoError(t, err)

	// Query balance
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)

	assert.Equal(t, "EUR", balanceResp.CurrentBalance.InstrumentCode)
	assert.Equal(t, "50000.00", balanceResp.CurrentBalance.Amount)
}

func TestBalanceService_GetBalance_NonCurrencyInstrument(t *testing.T) {
	// Test balance query for energy (KWH) - demonstrates multi-asset capability
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "KWH",
					Amount:         "150000.000",
				},
			},
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create energy holding account
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "ENERGY-001",
		Name:            "Energy Holding Account",
		ProductTypeCode: "HOLDING_GBP",
		InstrumentCode:  "KWH",
	})
	require.NoError(t, err)

	// Query balance
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)

	assert.Equal(t, "KWH", balanceResp.CurrentBalance.InstrumentCode)
	assert.Equal(t, "150000.000", balanceResp.CurrentBalance.Amount)
}

func TestBalanceService_GetBalance_RequiresActiveAccount(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{},
	}
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
		Reason:        "Suspended for balance query test",
	})
	require.NoError(t, err)

	// Balance query should fail for suspended account
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not active")
}

func TestBalanceService_GetBalance_ReactivatedAccountQueryable(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "5000.00",
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

	// Suspend account
	_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Maintenance",
	})
	require.NoError(t, err)

	// Reactivate account
	_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
	})
	require.NoError(t, err)

	// Balance should now be queryable
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)
	assert.NotNil(t, balanceResp.CurrentBalance)
	assert.Equal(t, "5000.00", balanceResp.CurrentBalance.Amount)
}

func TestBalanceService_GetBalance_ClosedAccountNotQueryable(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
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
		Reason:        "Account closure for test",
	})
	require.NoError(t, err)

	// Balance query should fail for closed account
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestBalanceService_GetBalance_HandlesPositionKeepingTimeout(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
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

	// Balance query with timeout should return Unavailable
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestBalanceService_GetBalance_HandlesPositionKeepingRateLimiting(t *testing.T) {
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

	// Rate limited requests should return Unavailable
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestBalanceService_GetBalance_HandlesPositionKeepingNotFound(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.NotFound, "position not found"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// NotFound from Position Keeping maps to Internal (indicates data inconsistency)
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "balance not found in position keeping")
}

func TestBalanceService_GetBalance_HandlesPositionKeepingUnavailable(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.Unavailable, "service temporarily unavailable"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Unavailable passes through as Unavailable
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
	assert.Contains(t, st.Message(), "position keeping service unavailable")
}

func TestBalanceService_GetBalance_HandlesPositionKeepingInternal(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.Internal, "internal server error"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Internal from Position Keeping maps to Internal (default case)
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to retrieve balance")
}

func TestBalanceService_GetBalance_HandlesPositionKeepingInvalidArgument(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		err: status.Error(codes.InvalidArgument, "invalid account_id format"),
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// InvalidArgument from Position Keeping maps to Internal (indicates a bug in our request)
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "invalid request to position keeping")
}

func TestBalanceService_RequiresPositionKeepingClient(t *testing.T) {
	// Service without Position Keeping client should return Unimplemented for balance queries
	repo := newMockRepository()
	svc, err := newTestServiceWithCache(repo) // No Position Keeping client
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

	// Balance query without client should return Unimplemented
	_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "position keeping service not configured")
}

func TestBalanceService_NostroAccountBalance(t *testing.T) {
	// Test balance query for NOSTRO account (with counterparty details)
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "GBP",
					Amount:         "1000000.00",
				},
			},
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	// Create NOSTRO account with counterparty
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "NOSTRO-GBP-HSBC",
		Name:            "HSBC GBP Nostro Account",
		ProductTypeCode: "NOSTRO_USD",
		InstrumentCode:  "GBP",
		CounterpartyDetails: &pb.CounterpartyDetails{
			CounterpartyId:          "HSBC001",
			CounterpartyName:        "HSBC UK",
			CounterpartyExternalRef: "GB29NWBK60161331926819",
		},
	})
	require.NoError(t, err)

	// Query balance
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)

	assert.Equal(t, "GBP", balanceResp.CurrentBalance.InstrumentCode)
	assert.Equal(t, "1000000.00", balanceResp.CurrentBalance.Amount)
}

func TestBalanceService_MultipleBalanceTypes(t *testing.T) {
	// Test that GetBalance returns only CURRENT balance even when multiple types exist
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "8000.00",
				},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "12500.00",
				},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "10000.00",
				},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "12500.00",
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

	// Query balance - should return CURRENT balance
	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)

	// Should return the CURRENT balance (12500.00), not opening or available
	assert.Equal(t, "12500.00", balanceResp.CurrentBalance.Amount)
}

func TestBalanceService_GetBalance_EmptyAccountIdReturnsInvalidArgument(t *testing.T) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	for _, accountID := range []string{"", "   ", "\t"} {
		_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountID,
		})
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "account_id is required")
	}
}

func TestBalanceService_GetBalance_MissingCurrentBalanceReturnsNilBalance(t *testing.T) {
	// Position Keeping returns balances but none is BALANCE_TYPE_CURRENT
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "5000.00",
				},
			},
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-USD-NOCUR",
		Name:            "No Current Balance Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)

	// When no CURRENT balance exists, response should have nil current_balance
	assert.Nil(t, balanceResp.CurrentBalance)
	// as_of should still be populated
	assert.NotNil(t, balanceResp.AsOf)
	assert.Equal(t, createResp.Facility.AccountCode, balanceResp.AccountId)
}

func TestBalanceService_GetBalance_NilAsOfTimestampReturnsFallback(t *testing.T) {
	// Position Keeping returns a CURRENT balance but no as_of timestamp
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "7500.00",
				},
			},
		},
		asOfSet: true,
		asOf:    nil, // Explicitly no as_of from Position Keeping
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	require.NoError(t, err)

	ctx := testCtx()

	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-USD-NOASOF",
		Name:            "No AsOf Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	balanceResp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	})
	require.NoError(t, err)

	// Balance should be present
	assert.NotNil(t, balanceResp.CurrentBalance)
	assert.Equal(t, "7500.00", balanceResp.CurrentBalance.Amount)
	// as_of should be populated with a fallback timestamp even when PK returns nil
	assert.NotNil(t, balanceResp.AsOf)
}

// ============================================================================
// Performance Benchmarks
// ============================================================================
//
// These benchmarks test performance for critical service operations.
// Performance targets:
//   - Balance query p99: <50ms
//   - Account creation p99: <50ms
//   - Account lookup (by ID) p99: <5ms
//   - Account lookup (by code) p99: <5ms
//
// Run with: go test -bench=. -benchmem ./services/internal-account/service/...

// BenchmarkGetBalance benchmarks the balance query operation.
// Target: P99 < 50ms
func BenchmarkGetBalance(b *testing.B) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "USD",
					Amount:         "10000.00",
				},
			},
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	ctx := testCtx()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "BENCH-BAL-001",
		Name:            "Benchmark Balance Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	if err != nil {
		b.Fatalf("failed to create account: %v", err)
	}
	accountCode := resp.Facility.AccountCode

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountCode,
		})
		if err != nil {
			b.Fatalf("GetBalance failed: %v", err)
		}
	}
}

// BenchmarkGetBalance_MultipleBalanceTypes benchmarks balance retrieval when
// position keeping returns multiple balance types.
func BenchmarkGetBalance_MultipleBalanceTypes(b *testing.B) {
	repo := newMockRepository()
	posClient := &mockPositionKeepingClient{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
				Amount:      &quantityv1.InstrumentAmount{InstrumentCode: "USD", Amount: "8000.00"},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount:      &quantityv1.InstrumentAmount{InstrumentCode: "USD", Amount: "12500.00"},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
				Amount:      &quantityv1.InstrumentAmount{InstrumentCode: "USD", Amount: "10000.00"},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
				Amount:      &quantityv1.InstrumentAmount{InstrumentCode: "USD", Amount: "12500.00"},
			},
		},
	}
	svc, err := newTestServiceWithCacheAndPosClient(repo, posClient)
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	ctx := testCtx()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "BENCH-MULTI-001",
		Name:            "Benchmark Multi Balance Account",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	if err != nil {
		b.Fatalf("failed to create account: %v", err)
	}
	accountCode := resp.Facility.AccountCode

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountCode,
		})
		if err != nil {
			b.Fatalf("GetBalance failed: %v", err)
		}
	}
}

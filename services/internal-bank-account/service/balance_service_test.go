package service

import (
	"context"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Balance service tests for Position Keeping integration as specified in subtask 15.3.
// These tests verify the interaction between InternalBankAccount service and Position Keeping client.

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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create an active account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-USD-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create EUR account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-EUR-001",
		Name:            "EUR Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create energy holding account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "ENERGY-001",
		Name:           "Energy Holding Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING,
		InstrumentCode: "KWH",
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create and suspend account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	// Suspend account
	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
		AccountId:     createResp.Facility.AccountCode,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Maintenance",
	})
	require.NoError(t, err)

	// Reactivate account
	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create and close account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	require.NoError(t, err)

	_, err = svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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

func TestBalanceService_RequiresPositionKeepingClient(t *testing.T) {
	// Service without Position Keeping client should return Unimplemented for balance queries
	repo := newMockRepository()
	svc, err := NewService(repo) // No Position Keeping client
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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
	// Test balance query for NOSTRO account (with correspondent details)
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create NOSTRO account with correspondent
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "NOSTRO-GBP-HSBC",
		Name:           "HSBC GBP Nostro Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO,
		InstrumentCode: "GBP",
		CorrespondentDetails: &pb.CorrespondentBankDetails{
			BankId:             "HSBC001",
			BankName:           "HSBC UK",
			ExternalAccountRef: "GB29NWBK60161331926819",
			SwiftCode:          "HSBCGB2L",
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Create account
	createResp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "CLR-001",
		Name:            "USD Clearing Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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
// Run with: go test -bench=. -benchmem ./services/internal-bank-account/service/...

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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	ctx := context.Background()
	resp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "BENCH-BAL-001",
		Name:            "Benchmark Balance Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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
	svc, err := NewServiceWithClients(repo, posClient, nil, nil, nil)
	if err != nil {
		b.Fatalf("failed to create service: %v", err)
	}

	ctx := context.Background()
	resp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
		AccountCode:     "BENCH-MULTI-001",
		Name:            "Benchmark Multi Balance Account",
		AccountType:     pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
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

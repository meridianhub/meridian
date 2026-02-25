//go:build integration

package e2e

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantitypb "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ============================================================================
// Configurable Mock Position Keeping Client
// ============================================================================

// configurablePKClient implements service.PositionKeepingClient with controllable behavior.
// It supports configurable errors, call counting, and per-account responses.
type configurablePKClient struct {
	mu             sync.Mutex
	accountResults map[string]*pkAccountResult
	defaultErr     error
	callCount      atomic.Int64
}

// pkAccountResult holds the configured response for a specific account+instrument key.
type pkAccountResult struct {
	balances []*positionkeepingv1.BalanceEntry
	err      error
}

func newConfigurablePKClient() *configurablePKClient {
	return &configurablePKClient{
		accountResults: make(map[string]*pkAccountResult),
	}
}

func (c *configurablePKClient) SetBalance(accountID, instrumentCode string, amount decimal.Decimal) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%s:%s", accountID, instrumentCode)
	c.accountResults[key] = &pkAccountResult{
		balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantitypb.InstrumentAmount{
					Amount:         amount.String(),
					InstrumentCode: instrumentCode,
				},
			},
		},
	}
}

func (c *configurablePKClient) SetError(accountID, instrumentCode string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%s:%s", accountID, instrumentCode)
	c.accountResults[key] = &pkAccountResult{err: err}
}

func (c *configurablePKClient) SetDefaultError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.defaultErr = err
}

func (c *configurablePKClient) ClearDefaultError() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.defaultErr = nil
}

func (c *configurablePKClient) GetAccountBalances(_ context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	c.callCount.Add(1)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.defaultErr != nil {
		return nil, c.defaultErr
	}

	key := fmt.Sprintf("%s:%s", req.AccountId, req.InstrumentCode)
	if result, ok := c.accountResults[key]; ok {
		if result.err != nil {
			return nil, result.err
		}
		return &positionkeepingv1.GetAccountBalancesResponse{
			AccountId: req.AccountId,
			Balances:  result.balances,
			AsOf:      timestamppb.Now(),
		}, nil
	}

	// Return zero balance if not configured
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantitypb.InstrumentAmount{
					Amount:         "0",
					InstrumentCode: req.InstrumentCode,
				},
			},
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (c *configurablePKClient) GetAccountBalance(_ context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId:   req.AccountId,
		BalanceType: req.BalanceType,
		Amount: &quantitypb.InstrumentAmount{
			Amount:         "0",
			InstrumentCode: req.InstrumentCode,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (c *configurablePKClient) Close() error {
	return nil
}

// ============================================================================
// E2E Test: GetBalance with Position Keeping Integration
// ============================================================================

// TestE2E_GetBalance_PositionKeepingIntegration verifies the GetBalance RPC works
// correctly when the Position Keeping client is properly wired and reachable.
// All subtests share a single testcontainer to minimize startup overhead.
func TestE2E_GetBalance_PositionKeepingIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)

	// ----------------------------------------------------------------
	// Group 1: GetBalance with PK client configured (happy + error paths)
	// ----------------------------------------------------------------
	t.Run("WithPKClient", func(t *testing.T) {
		ctx := setupTenantSchema(t, tc, "e2e_getbalance_pk_tenant")

		mockPK := newConfigurablePKClient()
		svc := createServiceWithConfigurablePK(t, tc.repo, mockPK)

		// Create a reusable active account
		accountCode := "GBP_CLEARING_BAL"
		var accountID string

		t.Run("setup: create active account", func(t *testing.T) {
			resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:     accountCode,
				Name:            "GBP Clearing Account for Balance Tests",
				ProductTypeCode: "CLEARING_GBP",
				InstrumentCode:  "GBP",
			})
			require.NoError(t, err)
			require.NotEmpty(t, resp.AccountId)
			accountID = resp.AccountId
		})

		t.Run("returns current balance when PK is available", func(t *testing.T) {
			mockPK.SetBalance(accountID, "GBP", decimal.NewFromFloat(25000.75))

			resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: accountCode,
			})
			require.NoError(t, err)

			assert.NotEmpty(t, resp.AccountId)
			require.NotNil(t, resp.CurrentBalance)

			actualBalance, err := decimal.NewFromString(resp.CurrentBalance.Amount)
			require.NoError(t, err)
			expectedBalance := decimal.NewFromFloat(25000.75)
			assert.True(t, expectedBalance.Equal(actualBalance),
				"Balance mismatch: expected %s, got %s", expectedBalance.String(), actualBalance.String())
			assert.Equal(t, "GBP", resp.CurrentBalance.InstrumentCode)
			assert.NotNil(t, resp.AsOf)
		})

		t.Run("returns zero balance when no position configured", func(t *testing.T) {
			newCode := "EUR_ZERO_BAL"
			_, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:     newCode,
				Name:            "EUR Account with Zero Balance",
				ProductTypeCode: "HOLDING_GBP",
				InstrumentCode:  "EUR",
			})
			require.NoError(t, err)

			resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: newCode,
			})
			require.NoError(t, err)
			require.NotNil(t, resp.CurrentBalance)

			actualBalance, err := decimal.NewFromString(resp.CurrentBalance.Amount)
			require.NoError(t, err)
			assert.True(t, decimal.Zero.Equal(actualBalance),
				"Expected zero balance, got %s", actualBalance.String())
		})

		t.Run("returns Unavailable when PK is down", func(t *testing.T) {
			mockPK.SetDefaultError(status.Error(codes.Unavailable, "connection refused"))
			defer mockPK.ClearDefaultError()

			_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: accountCode,
			})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.Unavailable, st.Code())
		})

		t.Run("returns Internal when PK returns NotFound", func(t *testing.T) {
			notFoundCode := "GBP_NO_POSITION"
			createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:     notFoundCode,
				Name:            "Account with No Position",
				ProductTypeCode: "SUSPENSE_GBP",
				InstrumentCode:  "GBP",
			})
			require.NoError(t, err)

			mockPK.SetError(createResp.AccountId, "GBP",
				status.Error(codes.NotFound, "no position found for account/instrument"))

			_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: notFoundCode,
			})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			// NotFound from PK is mapped to Internal (see mapPositionKeepingErrorToGRPC)
			assert.Equal(t, codes.Internal, st.Code())
		})

		t.Run("returns FailedPrecondition for SUSPENDED account", func(t *testing.T) {
			_, err := svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
				AccountId:     accountCode,
				ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:        "Testing suspended balance query",
			})
			require.NoError(t, err)

			_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: accountCode,
			})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.FailedPrecondition, st.Code())
			assert.Contains(t, st.Message(), "not active")

			// Reactivate for subsequent tests
			_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
				AccountId:     accountCode,
				ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
			})
			require.NoError(t, err)
		})

		t.Run("returns FailedPrecondition for CLOSED account", func(t *testing.T) {
			closedCode := "GBP_CLOSED_BAL"
			_, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
				AccountCode:     closedCode,
				Name:            "Account to Close",
				ProductTypeCode: "CLEARING_GBP",
				InstrumentCode:  "GBP",
			})
			require.NoError(t, err)

			_, err = svc.ControlInternalAccount(ctx, &pb.ControlInternalAccountRequest{
				AccountId:     closedCode,
				ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
				Reason:        "Closing for balance test",
			})
			require.NoError(t, err)

			_, err = svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: closedCode,
			})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.FailedPrecondition, st.Code())
		})

		t.Run("handles PK timeout gracefully", func(t *testing.T) {
			mockPK.SetDefaultError(status.Error(codes.DeadlineExceeded, "context deadline exceeded"))
			defer mockPK.ClearDefaultError()

			_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: accountCode,
			})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.Unavailable, st.Code())
		})

		t.Run("handles PK rate limiting", func(t *testing.T) {
			mockPK.SetDefaultError(status.Error(codes.ResourceExhausted, "rate limit exceeded"))
			defer mockPK.ClearDefaultError()

			_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: accountCode,
			})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.Unavailable, st.Code())
		})

		t.Run("returns NotFound for nonexistent account", func(t *testing.T) {
			_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: "NONEXISTENT_ACCOUNT",
			})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.NotFound, st.Code())
		})

		t.Run("recovers after PK becomes available again", func(t *testing.T) {
			mockPK.SetDefaultError(status.Error(codes.Unavailable, "service unavailable"))

			_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: accountCode,
			})
			require.Error(t, err)

			mockPK.ClearDefaultError()
			mockPK.SetBalance(accountID, "GBP", decimal.NewFromFloat(50000.00))

			resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: accountCode,
			})
			require.NoError(t, err)
			require.NotNil(t, resp.CurrentBalance)

			actualBalance, err := decimal.NewFromString(resp.CurrentBalance.Amount)
			require.NoError(t, err)
			expectedBalance := decimal.NewFromFloat(50000.00)
			assert.True(t, expectedBalance.Equal(actualBalance),
				"Balance mismatch after recovery: expected %s, got %s",
				expectedBalance.String(), actualBalance.String())
		})
	})

	// ----------------------------------------------------------------
	// Group 2: GetBalance without PK client (Unimplemented)
	// ----------------------------------------------------------------
	t.Run("WithoutPKClient", func(t *testing.T) {
		ctx := setupTenantSchema(t, tc, "e2e_getbalance_nopk_tenant")

		accountCode := "GBP_NOPK_ACC"
		_, err := tc.svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
			AccountCode:     accountCode,
			Name:            "Account Without PK Client",
			ProductTypeCode: "CLEARING_GBP",
			InstrumentCode:  "GBP",
		})
		require.NoError(t, err)

		_, err = tc.svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountCode,
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unimplemented, st.Code())
		assert.Contains(t, st.Message(), "position keeping service not configured")
	})

	// ----------------------------------------------------------------
	// Group 3: Multi-asset GetBalance
	// ----------------------------------------------------------------
	t.Run("MultiAsset", func(t *testing.T) {
		ctx := setupTenantSchema(t, tc, "e2e_getbalance_multiasset")

		mockPK := newConfigurablePKClient()
		svc := createServiceWithConfigurablePK(t, tc.repo, mockPK)

		testCases := []struct {
			code       string
			name       string
			instrument string
			balance    string
		}{
			{"GBP_BAL", "GBP Clearing", "GBP", "1000000.50"},
			{"KWH_BAL", "Energy Holding", "KWH", "50000.123"},
			{"GPU_BAL", "GPU Pool", "GPU_HOUR", "10000"},
			{"CO2_BAL", "Carbon Credits", "CARBON_TONNE", "5000.99"},
		}

		for _, tc := range testCases {
			t.Run(fmt.Sprintf("GetBalance for %s", tc.instrument), func(t *testing.T) {
				createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
					AccountCode:     tc.code,
					Name:            tc.name,
					ProductTypeCode: "HOLDING_GBP",
					InstrumentCode:  tc.instrument,
				})
				require.NoError(t, err)

				expectedBalance, err := decimal.NewFromString(tc.balance)
				require.NoError(t, err)
				mockPK.SetBalance(createResp.AccountId, tc.instrument, expectedBalance)

				resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
					AccountId: tc.code,
				})
				require.NoError(t, err)
				require.NotNil(t, resp.CurrentBalance)

				actualBalance, err := decimal.NewFromString(resp.CurrentBalance.Amount)
				require.NoError(t, err)
				assert.True(t, expectedBalance.Equal(actualBalance),
					"Balance mismatch for %s: expected %s, got %s",
					tc.instrument, expectedBalance.String(), actualBalance.String())
				assert.Equal(t, tc.instrument, resp.CurrentBalance.InstrumentCode)
			})
		}
	})
}

// ============================================================================
// Helper Functions
// ============================================================================

// createServiceWithConfigurablePK creates a service with the configurable PK client.
func createServiceWithConfigurablePK(t *testing.T, repo *persistence.Repository, pkClient *configurablePKClient) *service.Service {
	t.Helper()

	svc, err := service.NewServiceWithClients(repo, pkClient, nil, nil, nil)
	require.NoError(t, err)
	return svc
}

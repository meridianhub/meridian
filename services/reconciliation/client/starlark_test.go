package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/client"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

func newTestStarlarkContext() *saga.StarlarkContext {
	return &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		IdempotencyKey:  "saga_test_step_1",
	}
}

func TestRegisterStarlarkHandlers(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	err = client.RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	// Verify all expected handlers are registered
	expectedHandlers := []string{
		"reconciliation.initiate_run",
		"reconciliation.execute_run",
		"reconciliation.retrieve_run",
		"reconciliation.cancel_run",
		"reconciliation.assert_balance",
		"reconciliation.initiate_dispute",
	}
	for _, name := range expectedHandlers {
		handler, err := registry.Get(name)
		assert.NoError(t, err, "handler %s should be registered", name)
		assert.NotNil(t, handler, "handler %s should not be nil", name)
	}
}

func TestInitiateRunHandler_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateResp: &reconciliationv1.InitiateAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{
				RunId: "run-001",
			},
		},
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"account_id":      "ACC-001",
		"initiated_by":    "test-user",
		"scope":           "ACCOUNT",
		"settlement_type": "DAILY",
	})
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "run-001", resultMap["run_id"])
	assert.Equal(t, "PENDING", resultMap["status"])
}

func TestInitiateRunHandler_MissingAccountID(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"initiated_by": "test-user",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id")
}

func TestInitiateRunHandler_MissingInitiatedBy(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"account_id": "ACC-001",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initiated_by")
}

func TestInitiateRunHandler_GRPCError(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateErr: status.Error(codes.Internal, "server error"),
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"account_id":   "ACC-001",
		"initiated_by": "test-user",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation.initiate_run")
}

func TestInitiateRunHandler_ScopeVariants(t *testing.T) {
	scopes := []string{"ACCOUNT", "INSTRUMENT", "PORTFOLIO", "FULL", "UNKNOWN"}
	for _, scope := range scopes {
		t.Run(scope, func(t *testing.T) {
			mock := &mockReconciliationServer{
				initiateResp: &reconciliationv1.InitiateAccountReconciliationResponse{
					Run: &reconciliationv1.SettlementRunSummary{RunId: "run-001"},
				},
			}
			cfg, cleanup := setupTestServer(t, mock)
			defer cleanup()

			c, clientCleanup, err := client.New(cfg)
			require.NoError(t, err)
			defer clientCleanup()

			registry := saga.NewHandlerRegistry()
			require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

			handler, err := registry.Get("reconciliation.initiate_run")
			require.NoError(t, err)

			ctx := newTestStarlarkContext()
			result, err := handler(ctx, map[string]any{
				"account_id":   "ACC-001",
				"initiated_by": "test-user",
				"scope":        scope,
			})
			require.NoError(t, err)
			assert.NotNil(t, result)
		})
	}
}

func TestInitiateRunHandler_SettlementTypeVariants(t *testing.T) {
	types := []string{"DAILY", "WEEKLY", "MONTHLY", "ON_DEMAND", "END_OF_DAY", "REAL_TIME", "UNKNOWN", ""}
	for _, st := range types {
		t.Run(st, func(t *testing.T) {
			mock := &mockReconciliationServer{
				initiateResp: &reconciliationv1.InitiateAccountReconciliationResponse{
					Run: &reconciliationv1.SettlementRunSummary{RunId: "run-001"},
				},
			}
			cfg, cleanup := setupTestServer(t, mock)
			defer cleanup()

			c, clientCleanup, err := client.New(cfg)
			require.NoError(t, err)
			defer clientCleanup()

			registry := saga.NewHandlerRegistry()
			require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

			handler, err := registry.Get("reconciliation.initiate_run")
			require.NoError(t, err)

			ctx := newTestStarlarkContext()
			params := map[string]any{
				"account_id":   "ACC-001",
				"initiated_by": "test-user",
			}
			if st != "" {
				params["settlement_type"] = st
			}
			result, err := handler(ctx, params)
			require.NoError(t, err)
			assert.NotNil(t, result)
		})
	}
}

func TestExecuteRunHandler_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		executeResp: &reconciliationv1.ExecuteAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{RunId: "run-001"},
		},
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.execute_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{"run_id": "run-001"})
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "run-001", resultMap["run_id"])
	assert.Equal(t, "RUNNING", resultMap["status"])
}

func TestExecuteRunHandler_MissingRunID(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.execute_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run_id")
}

func TestExecuteRunHandler_GRPCError(t *testing.T) {
	mock := &mockReconciliationServer{
		executeErr: status.Error(codes.Internal, "execution failed"),
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.execute_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{"run_id": "run-001"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation.execute_run")
}

func TestRetrieveRunHandler_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		retrieveResp: &reconciliationv1.RetrieveAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{
				RunId:         "run-001",
				Status:        reconciliationv1.RunStatus_RUN_STATUS_COMPLETED,
				VarianceCount: 3,
			},
		},
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.retrieve_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{"run_id": "run-001"})
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "run-001", resultMap["run_id"])
	assert.Equal(t, int64(3), resultMap["variance_count"])
}

func TestRetrieveRunHandler_MissingRunID(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.retrieve_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
}

func TestRetrieveRunHandler_GRPCError(t *testing.T) {
	mock := &mockReconciliationServer{
		retrieveErr: status.Error(codes.Internal, "server error"),
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.retrieve_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{"run_id": "run-001"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation.retrieve_run")
}

func TestCancelRunHandler_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		controlResp: &reconciliationv1.ControlAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{RunId: "run-001"},
		},
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.cancel_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"run_id": "run-001",
		"reason": "compensation rollback",
	})
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "run-001", resultMap["run_id"])
	assert.Equal(t, "CANCELLED", resultMap["status"])
}

func TestCancelRunHandler_MissingRunID(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.cancel_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
}

func TestCancelRunHandler_GRPCError(t *testing.T) {
	mock := &mockReconciliationServer{
		controlErr: status.Error(codes.Internal, "cancel failed"),
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.cancel_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{"run_id": "run-001"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation.cancel_run")
}

func TestAssertBalanceHandler_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		assertBalanceResp: &reconciliationv1.AssertBalanceResponse{
			Assertion: &reconciliationv1.BalanceAssertionDetail{
				AssertionId: "assert-001",
				Status:      reconciliationv1.AssertionStatus_ASSERTION_STATUS_PASSED,
			},
		},
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.assert_balance")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"account_id":       "ACC-001",
		"instrument_code":  "GBP",
		"expression":       "DEBIT == CREDIT",
		"expected_balance": "100.00",
	})
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "assert-001", resultMap["assertion_id"])
}

func TestAssertBalanceHandler_MissingParams(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.assert_balance")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()

	// Missing account_id
	_, err = handler(ctx, map[string]any{
		"instrument_code":  "GBP",
		"expression":       "DEBIT == CREDIT",
		"expected_balance": "100.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id")

	// Missing instrument_code
	_, err = handler(ctx, map[string]any{
		"account_id":       "ACC-001",
		"expression":       "DEBIT == CREDIT",
		"expected_balance": "100.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instrument_code")

	// Missing expression
	_, err = handler(ctx, map[string]any{
		"account_id":       "ACC-001",
		"instrument_code":  "GBP",
		"expected_balance": "100.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expression")

	// Missing expected_balance
	_, err = handler(ctx, map[string]any{
		"account_id":      "ACC-001",
		"instrument_code": "GBP",
		"expression":      "DEBIT == CREDIT",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected_balance")
}

func TestAssertBalanceHandler_GRPCError(t *testing.T) {
	mock := &mockReconciliationServer{
		assertBalanceErr: status.Error(codes.Internal, "assertion failed"),
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.assert_balance")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"account_id":       "ACC-001",
		"instrument_code":  "GBP",
		"expression":       "DEBIT == CREDIT",
		"expected_balance": "100.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation.assert_balance")
}

func TestInitiateDisputeHandler_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateDisputeResp: &reconciliationv1.InitiateDisputeResponse{
			Dispute: &reconciliationv1.DisputeDetail{
				DisputeId: "disp-001",
			},
		},
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_dispute")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"variance_id": "var-001",
		"run_id":      "run-001",
		"account_id":  "ACC-001",
		"reason":      "incorrect amount",
		"raised_by":   "user-001",
	})
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "disp-001", resultMap["dispute_id"])
	assert.Equal(t, "OPEN", resultMap["status"])
}

func TestInitiateDisputeHandler_MissingParams(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_dispute")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()

	// Missing variance_id
	_, err = handler(ctx, map[string]any{
		"run_id":     "run-001",
		"account_id": "ACC-001",
		"reason":     "incorrect",
		"raised_by":  "user-001",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "variance_id")

	// Missing run_id
	_, err = handler(ctx, map[string]any{
		"variance_id": "var-001",
		"account_id":  "ACC-001",
		"reason":      "incorrect",
		"raised_by":   "user-001",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run_id")

	// Missing account_id
	_, err = handler(ctx, map[string]any{
		"variance_id": "var-001",
		"run_id":      "run-001",
		"reason":      "incorrect",
		"raised_by":   "user-001",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id")

	// Missing reason
	_, err = handler(ctx, map[string]any{
		"variance_id": "var-001",
		"run_id":      "run-001",
		"account_id":  "ACC-001",
		"raised_by":   "user-001",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason")

	// Missing raised_by
	_, err = handler(ctx, map[string]any{
		"variance_id": "var-001",
		"run_id":      "run-001",
		"account_id":  "ACC-001",
		"reason":      "incorrect",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "raised_by")
}

func TestInitiateDisputeHandler_GRPCError(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateDisputeErr: status.Error(codes.Internal, "dispute creation failed"),
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_dispute")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"variance_id": "var-001",
		"run_id":      "run-001",
		"account_id":  "ACC-001",
		"reason":      "incorrect",
		"raised_by":   "user-001",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation.initiate_dispute")
}

func TestOptionalString_NonStringType(t *testing.T) {
	// Test the optionalString path where value is not a string
	// This is exercised indirectly via scope/settlement_type with non-string values
	mock := &mockReconciliationServer{
		initiateResp: &reconciliationv1.InitiateAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{RunId: "run-001"},
		},
	}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, client.RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("reconciliation.initiate_run")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	// Pass non-string values for optional params to exercise optionalString fallback
	result, err := handler(ctx, map[string]any{
		"account_id":      "ACC-001",
		"initiated_by":    "test-user",
		"scope":           42,    // not a string
		"settlement_type": false, // not a string
	})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

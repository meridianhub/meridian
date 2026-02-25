package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// --- Mock implementations ---

type mockSagaAdminClient struct {
	getCausationTreeFn func(ctx context.Context, req *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error)
}

func (m *mockSagaAdminClient) GetCausationTree(ctx context.Context, req *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
	return m.getCausationTreeFn(ctx, req)
}

type mockPositionKeepingClient struct {
	listLogsFn func(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error)
}

func (m *mockPositionKeepingClient) ListFinancialPositionLogs(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	return m.listLogsFn(ctx, req)
}

type mockFinancialAccountingClient struct {
	listPostingsFn func(ctx context.Context, req *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error)
}

func (m *mockFinancialAccountingClient) ListLedgerPostings(ctx context.Context, req *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
	return m.listPostingsFn(ctx, req)
}

type mockAuditSagaRegistryClient struct {
	listSagasFn func(ctx context.Context, req *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error)
}

func (m *mockAuditSagaRegistryClient) ListSagas(ctx context.Context, req *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
	return m.listSagasFn(ctx, req)
}

type mockReconciliationClient struct {
	listRunsFn    func(ctx context.Context, req *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error)
	listResultsFn func(ctx context.Context, req *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error)
}

func (m *mockReconciliationClient) ListAccountReconciliations(ctx context.Context, req *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
	return m.listRunsFn(ctx, req)
}

func (m *mockReconciliationClient) ListReconciliationResults(ctx context.Context, req *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error) {
	return m.listResultsFn(ctx, req)
}

// --- AuditClients helper ---

func newAuditClients(
	sagaAdmin tools.SagaAdminQuerier,
	positionKeeping tools.PositionQuerier,
	financialAccounting tools.PostingQuerier,
	sagaRegistry tools.SagaExecutionQuerier,
	reconciliation tools.ReconciliationQuerier,
) tools.AuditClients {
	return tools.AuditClients{
		SagaAdmin:           sagaAdmin,
		PositionKeeping:     positionKeeping,
		FinancialAccounting: financialAccounting,
		SagaRegistry:        sagaRegistry,
		Reconciliation:      reconciliation,
	}
}

// --- meridian_causation_tree tests ---

func TestCausationTree_ValidParams_ReturnTree(t *testing.T) {
	sagaID := "550e8400-e29b-41d4-a716-446655440000"

	mock := &mockSagaAdminClient{
		getCausationTreeFn: func(_ context.Context, req *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
			if req.SagaId != sagaID {
				return nil, fmt.Errorf("unexpected saga_id: %s", req.SagaId)
			}
			return &sagav1.GetCausationTreeResponse{
				Tree: &sagav1.CausationTreeNode{
					SagaId:   sagaID,
					SagaName: "current_account_withdrawal",
					Status:   "COMPLETED",
					Steps: []*sagav1.StepNode{
						{Index: 0, Name: "debit_account", Status: "COMPLETED"},
					},
				},
				Depth: 1,
			}, nil
		},
	}

	clients := newAuditClients(mock, nil, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(fmt.Sprintf(`{"saga_id": %q}`, sagaID))
	result, err := r.Call(context.Background(), "meridian_causation_tree", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCausationTree_MissingSagaID_ValidationError(t *testing.T) {
	mock := &mockSagaAdminClient{
		getCausationTreeFn: func(_ context.Context, _ *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
			t.Fatal("handler should not be called for missing saga_id")
			return nil, nil
		},
	}
	clients := newAuditClients(mock, nil, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	_, err := r.Call(context.Background(), "meridian_causation_tree", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected validation error for missing saga_id")
	}
}

func TestCausationTree_GRPCError_FormattedResponse(t *testing.T) {
	sagaID := "550e8400-e29b-41d4-a716-446655440000"

	mock := &mockSagaAdminClient{
		getCausationTreeFn: func(_ context.Context, _ *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
			return nil, status.Errorf(codes.NotFound, "saga not found: %s", sagaID)
		},
	}

	clients := newAuditClients(mock, nil, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(fmt.Sprintf(`{"saga_id": %q}`, sagaID))
	result, err := r.Call(context.Background(), "meridian_causation_tree", params)
	if err != nil {
		t.Fatalf("expected handler to return formatted error as result, not error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for gRPC error")
	}
}

func TestCausationTree_EmptyTree_MeaningfulResponse(t *testing.T) {
	sagaID := "550e8400-e29b-41d4-a716-446655440000"

	mock := &mockSagaAdminClient{
		getCausationTreeFn: func(_ context.Context, _ *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
			return &sagav1.GetCausationTreeResponse{
				Tree:  nil,
				Depth: 0,
			}, nil
		},
	}

	clients := newAuditClients(mock, nil, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(fmt.Sprintf(`{"saga_id": %q}`, sagaID))
	result, err := r.Call(context.Background(), "meridian_causation_tree", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- meridian_positions_query tests ---

func TestPositionsQuery_ValidParams_ReturnLogs(t *testing.T) {
	mock := &mockPositionKeepingClient{
		listLogsFn: func(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
			return &positionkeepingv1.ListFinancialPositionLogsResponse{
				Logs: []*positionkeepingv1.FinancialPositionLog{
					{
						LogId:     "log-001",
						AccountId: "acc-001",
						StatusTracking: &positionkeepingv1.StatusTracking{
							StatusUpdatedAt: timestamppb.Now(),
						},
						CreatedAt: timestamppb.Now(),
						UpdatedAt: timestamppb.Now(),
					},
				},
			}, nil
		},
	}

	clients := newAuditClients(nil, mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(`{"account_id": "acc-001"}`)
	result, err := r.Call(context.Background(), "meridian_positions_query", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestPositionsQuery_NoParams_DefaultQuery(t *testing.T) {
	called := false
	mock := &mockPositionKeepingClient{
		listLogsFn: func(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
			called = true
			return &positionkeepingv1.ListFinancialPositionLogsResponse{}, nil
		},
	}

	clients := newAuditClients(nil, mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_positions_query", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Fatal("expected handler to call gRPC client")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestPositionsQuery_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockPositionKeepingClient{
		listLogsFn: func(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
			return nil, status.Errorf(codes.Internal, "position service unavailable")
		},
	}

	clients := newAuditClients(nil, mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_positions_query", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestPositionsQuery_EmptyResults_MeaningfulResponse(t *testing.T) {
	mock := &mockPositionKeepingClient{
		listLogsFn: func(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
			return &positionkeepingv1.ListFinancialPositionLogsResponse{
				Logs: []*positionkeepingv1.FinancialPositionLog{},
			}, nil
		},
	}

	clients := newAuditClients(nil, mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_positions_query", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- meridian_postings_query tests ---

func TestPostingsQuery_ValidParams_ReturnPostings(t *testing.T) {
	mock := &mockFinancialAccountingClient{
		listPostingsFn: func(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			return &financialaccountingv1.ListLedgerPostingsResponse{
				LedgerPostings: []*financialaccountingv1.LedgerPosting{
					{
						Id:                    "posting-001",
						FinancialBookingLogId: "log-001",
						AccountId:             "acc-001",
						ValueDate:             timestamppb.Now(),
						CreatedAt:             timestamppb.Now(),
					},
				},
			}, nil
		},
	}

	clients := newAuditClients(nil, nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(`{"account_id": "acc-001"}`)
	result, err := r.Call(context.Background(), "meridian_postings_query", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestPostingsQuery_DateRangeFilter_PassedToClient(t *testing.T) {
	var capturedReq *financialaccountingv1.ListLedgerPostingsRequest

	mock := &mockFinancialAccountingClient{
		listPostingsFn: func(_ context.Context, req *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			capturedReq = req
			return &financialaccountingv1.ListLedgerPostingsResponse{}, nil
		},
	}

	clients := newAuditClients(nil, nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(`{"date_from": "2024-01-01T00:00:00Z", "date_to": "2024-01-31T23:59:59Z", "page_size": 50}`)
	_, err := r.Call(context.Background(), "meridian_postings_query", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	if capturedReq.ValueDateFrom == nil {
		t.Error("expected value_date_from to be set")
	}
	if capturedReq.ValueDateTo == nil {
		t.Error("expected value_date_to to be set")
	}
	if capturedReq.Pagination == nil || capturedReq.Pagination.PageSize != 50 {
		t.Errorf("expected page_size=50 to be propagated, got pagination=%v", capturedReq.Pagination)
	}
}

func TestPostingsQuery_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockFinancialAccountingClient{
		listPostingsFn: func(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			return nil, status.Errorf(codes.Unavailable, "financial accounting service unavailable")
		},
	}

	clients := newAuditClients(nil, nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_postings_query", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestPostingsQuery_DateFromAfterDateTo_ReturnsError(t *testing.T) {
	mock := &mockFinancialAccountingClient{
		listPostingsFn: func(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			t.Fatal("handler should not be called for invalid date range")
			return nil, nil
		},
	}

	clients := newAuditClients(nil, nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	// date_from is after date_to — should return an error map, not call the backend.
	params := json.RawMessage(`{"date_from": "2024-02-01T00:00:00Z", "date_to": "2024-01-01T00:00:00Z"}`)
	result, err := r.Call(context.Background(), "meridian_postings_query", params)
	if err != nil {
		t.Fatalf("unexpected error return: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if _, hasErr := m["error"]; !hasErr {
		t.Errorf("expected 'error' key in result, got keys: %v", m)
	}
}

func TestPostingsQuery_EmptyResults_MeaningfulResponse(t *testing.T) {
	mock := &mockFinancialAccountingClient{
		listPostingsFn: func(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			return &financialaccountingv1.ListLedgerPostingsResponse{
				LedgerPostings: []*financialaccountingv1.LedgerPosting{},
			}, nil
		},
	}

	clients := newAuditClients(nil, nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_postings_query", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- meridian_saga_executions tests ---

func TestSagaExecutions_ValidParams_ReturnSagas(t *testing.T) {
	mock := &mockAuditSagaRegistryClient{
		listSagasFn: func(_ context.Context, _ *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
			return &sagav1.ListSagasResponse{
				Sagas: []*sagav1.SagaDefinition{
					{
						Id:     "def-001",
						Name:   "current_account_withdrawal",
						Status: sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
					},
				},
			}, nil
		},
	}

	clients := newAuditClients(nil, nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_saga_executions", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSagaExecutions_StatusFilter_PassedToClient(t *testing.T) {
	var capturedReq *sagav1.ListSagasRequest

	mock := &mockAuditSagaRegistryClient{
		listSagasFn: func(_ context.Context, req *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
			capturedReq = req
			return &sagav1.ListSagasResponse{}, nil
		},
	}

	clients := newAuditClients(nil, nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(`{"status": "ACTIVE"}`)
	_, err := r.Call(context.Background(), "meridian_saga_executions", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	if capturedReq.StatusFilter != sagav1.SagaStatus_SAGA_STATUS_ACTIVE {
		t.Errorf("expected status ACTIVE, got %v", capturedReq.StatusFilter)
	}
}

func TestSagaExecutions_InvalidStatus_ValidationError(t *testing.T) {
	mock := &mockAuditSagaRegistryClient{
		listSagasFn: func(_ context.Context, _ *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
			t.Fatal("handler should not be called for invalid status")
			return nil, nil
		},
	}
	clients := newAuditClients(nil, nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	// "INVALID_STATUS_VALUE" is rejected by the JSON schema enum constraint,
	// so r.Call returns a non-nil error (schema validation, not tool-not-found).
	params := json.RawMessage(`{"status": "INVALID_STATUS_VALUE"}`)
	_, err := r.Call(context.Background(), "meridian_saga_executions", params)
	if err == nil {
		t.Fatal("expected validation error for invalid status")
	}
}

func TestSagaExecutions_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockAuditSagaRegistryClient{
		listSagasFn: func(_ context.Context, _ *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
			return nil, errors.New("saga registry unavailable")
		},
	}

	clients := newAuditClients(nil, nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_saga_executions", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- meridian_reconciliation_status tests ---

func TestReconciliationStatus_ValidParams_ReturnRuns(t *testing.T) {
	mock := &mockReconciliationClient{
		listRunsFn: func(_ context.Context, _ *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
			return &reconciliationv1.ListAccountReconciliationsResponse{
				Runs: []*reconciliationv1.SettlementRunSummary{
					{
						RunId:         "run-001",
						AccountId:     "acc-001",
						Status:        reconciliationv1.RunStatus_RUN_STATUS_COMPLETED,
						VarianceCount: 0,
						TotalVariance: "0.00",
						PeriodStart:   timestamppb.Now(),
						PeriodEnd:     timestamppb.Now(),
						InitiatedBy:   "system",
						CreatedAt:     timestamppb.Now(),
						UpdatedAt:     timestamppb.Now(),
					},
				},
			}, nil
		},
		listResultsFn: func(_ context.Context, _ *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error) {
			return &reconciliationv1.ListReconciliationResultsResponse{}, nil
		},
	}

	clients := newAuditClients(nil, nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_reconciliation_status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestReconciliationStatus_WithRunID_FetchesVariances(t *testing.T) {
	runID := "550e8400-e29b-41d4-a716-446655440001"
	variancesFetched := false

	mock := &mockReconciliationClient{
		listRunsFn: func(_ context.Context, _ *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
			return &reconciliationv1.ListAccountReconciliationsResponse{
				Runs: []*reconciliationv1.SettlementRunSummary{
					{
						RunId:       runID,
						AccountId:   "acc-001",
						Status:      reconciliationv1.RunStatus_RUN_STATUS_FAILED,
						PeriodStart: timestamppb.Now(),
						PeriodEnd:   timestamppb.Now(),
						InitiatedBy: "system",
						CreatedAt:   timestamppb.Now(),
						UpdatedAt:   timestamppb.Now(),
					},
				},
			}, nil
		},
		listResultsFn: func(_ context.Context, req *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error) {
			variancesFetched = true
			if req.RunId != runID {
				return nil, fmt.Errorf("unexpected run_id: %s", req.RunId)
			}
			return &reconciliationv1.ListReconciliationResultsResponse{
				Variances: []*reconciliationv1.VarianceDetail{
					{
						VarianceId:     "var-001",
						RunId:          runID,
						AccountId:      "acc-001",
						InstrumentCode: "GBP",
						ExpectedAmount: "100.00",
						ActualAmount:   "99.50",
						VarianceAmount: "-0.50",
						CreatedAt:      timestamppb.Now(),
						UpdatedAt:      timestamppb.Now(),
					},
				},
			}, nil
		},
	}

	clients := newAuditClients(nil, nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	params := json.RawMessage(fmt.Sprintf(`{"run_id": %q}`, runID))
	result, err := r.Call(context.Background(), "meridian_reconciliation_status", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !variancesFetched {
		t.Error("expected variances to be fetched when run_id is provided")
	}
}

func TestReconciliationStatus_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockReconciliationClient{
		listRunsFn: func(_ context.Context, _ *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
			return nil, status.Errorf(codes.Unavailable, "reconciliation service unavailable")
		},
		listResultsFn: nil,
	}

	clients := newAuditClients(nil, nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_reconciliation_status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestReconciliationStatus_EmptyResults_MeaningfulResponse(t *testing.T) {
	mock := &mockReconciliationClient{
		listRunsFn: func(_ context.Context, _ *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
			return &reconciliationv1.ListAccountReconciliationsResponse{
				Runs: []*reconciliationv1.SettlementRunSummary{},
			}, nil
		},
		listResultsFn: func(_ context.Context, _ *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error) {
			return &reconciliationv1.ListReconciliationResultsResponse{}, nil
		},
	}

	clients := newAuditClients(nil, nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	result, err := r.Call(context.Background(), "meridian_reconciliation_status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// noopMocks returns a full set of non-nil mock clients that return empty responses.
func noopMocks() (tools.SagaAdminQuerier, tools.PositionQuerier, tools.PostingQuerier, tools.SagaExecutionQuerier, tools.ReconciliationQuerier) {
	sagaAdmin := &mockSagaAdminClient{
		getCausationTreeFn: func(_ context.Context, _ *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
			return &sagav1.GetCausationTreeResponse{}, nil
		},
	}
	positionKeeping := &mockPositionKeepingClient{
		listLogsFn: func(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
			return &positionkeepingv1.ListFinancialPositionLogsResponse{}, nil
		},
	}
	financialAccounting := &mockFinancialAccountingClient{
		listPostingsFn: func(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			return &financialaccountingv1.ListLedgerPostingsResponse{}, nil
		},
	}
	sagaRegistry := &mockAuditSagaRegistryClient{
		listSagasFn: func(_ context.Context, _ *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
			return &sagav1.ListSagasResponse{}, nil
		},
	}
	reconciliation := &mockReconciliationClient{
		listRunsFn: func(_ context.Context, _ *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
			return &reconciliationv1.ListAccountReconciliationsResponse{}, nil
		},
		listResultsFn: func(_ context.Context, _ *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error) {
			return &reconciliationv1.ListReconciliationResultsResponse{}, nil
		},
	}
	return sagaAdmin, positionKeeping, financialAccounting, sagaRegistry, reconciliation
}

// --- Audit log entries tests (via AuditService) ---

func TestAuditLogEntries_ToolRegistered(t *testing.T) {
	clients := newAuditClients(noopMocks())
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	listed := r.List()
	names := make(map[string]bool)
	for _, tool := range listed {
		names[tool.Name] = true
	}

	required := []string{
		"meridian_causation_tree",
		"meridian_positions_query",
		"meridian_postings_query",
		"meridian_saga_executions",
		"meridian_reconciliation_status",
	}
	for _, name := range required {
		if !names[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestAuditTools_AllCategoryRead(t *testing.T) {
	clients := newAuditClients(noopMocks())
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	for _, tool := range r.List() {
		if tool.Category != tools.CategoryRead {
			t.Errorf("expected tool %q to be CategoryRead, got %v", tool.Name, tool.Category)
		}
	}
}

func TestAuditTools_NilClient_SkipsRegistration(t *testing.T) {
	// Only SagaAdmin is configured; the other 4 tools must not be registered.
	clients := newAuditClients(
		&mockSagaAdminClient{
			getCausationTreeFn: func(_ context.Context, _ *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
				return &sagav1.GetCausationTreeResponse{}, nil
			},
		},
		nil, nil, nil, nil,
	)
	r := tools.NewRegistry()
	tools.RegisterAuditTools(r, clients)

	listed := r.List()
	if len(listed) != 1 {
		t.Fatalf("expected 1 registered tool, got %d", len(listed))
	}
	if listed[0].Name != "meridian_causation_tree" {
		t.Errorf("expected meridian_causation_tree, got %q", listed[0].Name)
	}
}

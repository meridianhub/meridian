package worker

import (
	"context"
	"testing"
	"time"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockAccountReconciliationServiceClient implements the generated gRPC client interface.
type mockAccountReconciliationServiceClient struct {
	reconciliationv1.AccountReconciliationServiceClient
	resp *reconciliationv1.InitiateAccountReconciliationResponse
	err  error
}

func (m *mockAccountReconciliationServiceClient) InitiateAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.InitiateAccountReconciliationRequest,
	_ ...grpc.CallOption,
) (*reconciliationv1.InitiateAccountReconciliationResponse, error) {
	return m.resp, m.err
}

func TestGrpcReconciliationClient_InitiateReconciliation_Success(t *testing.T) {
	mock := &mockAccountReconciliationServiceClient{
		resp: &reconciliationv1.InitiateAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{
				RunId: "run-123",
			},
		},
	}

	client := NewGrpcReconciliationClient(mock)
	runID, err := client.InitiateReconciliation(context.Background(), InitiateRequest{
		AccountID:      "ACC-001",
		Scope:          "ACCOUNT",
		SettlementType: "DAILY",
		PeriodStart:    time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
		PeriodEnd:      time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC),
		InitiatedBy:    "settlement-scheduler",
	})

	require.NoError(t, err)
	assert.Equal(t, "run-123", runID)
}

func TestGrpcReconciliationClient_InitiateReconciliation_AlreadyExists(t *testing.T) {
	mock := &mockAccountReconciliationServiceClient{
		err: status.Errorf(codes.AlreadyExists, "run already exists"),
	}

	client := NewGrpcReconciliationClient(mock)
	_, err := client.InitiateReconciliation(context.Background(), InitiateRequest{
		AccountID:      "ACC-001",
		Scope:          "ACCOUNT",
		SettlementType: "DAILY",
		PeriodStart:    time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
		PeriodEnd:      time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC),
		InitiatedBy:    "settlement-scheduler",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRunAlreadyExists)
}

func TestGrpcReconciliationClient_InitiateReconciliation_InvalidScope(t *testing.T) {
	mock := &mockAccountReconciliationServiceClient{}

	client := NewGrpcReconciliationClient(mock)
	_, err := client.InitiateReconciliation(context.Background(), InitiateRequest{
		AccountID:      "ACC-001",
		Scope:          "BOGUS",
		SettlementType: "DAILY",
		PeriodStart:    time.Now().UTC(),
		PeriodEnd:      time.Now().UTC(),
		InitiatedBy:    "test",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownScope)
}

func TestGrpcReconciliationClient_InitiateReconciliation_InvalidSettlementType(t *testing.T) {
	mock := &mockAccountReconciliationServiceClient{}

	client := NewGrpcReconciliationClient(mock)
	_, err := client.InitiateReconciliation(context.Background(), InitiateRequest{
		AccountID:      "ACC-001",
		Scope:          "ACCOUNT",
		SettlementType: "BOGUS",
		PeriodStart:    time.Now().UTC(),
		PeriodEnd:      time.Now().UTC(),
		InitiatedBy:    "test",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownSettlementType)
}

func TestParseScope(t *testing.T) {
	tests := []struct {
		input    string
		expected reconciliationv1.ReconciliationScope
		wantErr  bool
	}{
		{"ACCOUNT", reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT, false},
		{"INSTRUMENT", reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT, false},
		{"PORTFOLIO", reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO, false},
		{"FULL", reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL, false},
		{"UNKNOWN", reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseScope(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, got)
			}
		})
	}
}

func TestParseSettlementType(t *testing.T) {
	tests := []struct {
		input    string
		expected reconciliationv1.SettlementType
		wantErr  bool
	}{
		{"DAILY", reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY, false},
		{"WEEKLY", reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY, false},
		{"MONTHLY", reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY, false},
		{"ON_DEMAND", reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND, false},
		{"UNKNOWN", reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseSettlementType(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, got)
			}
		})
	}
}

// Verify InitiateAccountReconciliation constructs the correct proto request.
func TestGrpcReconciliationClient_InitiateReconciliation_NilResponse(t *testing.T) {
	mock := &mockAccountReconciliationServiceClient{
		resp: &reconciliationv1.InitiateAccountReconciliationResponse{
			Run: nil,
		},
	}

	client := NewGrpcReconciliationClient(mock)
	_, err := client.InitiateReconciliation(context.Background(), InitiateRequest{
		AccountID:      "ACC-001",
		Scope:          "ACCOUNT",
		SettlementType: "DAILY",
		PeriodStart:    time.Now().UTC(),
		PeriodEnd:      time.Now().UTC(),
		InitiatedBy:    "test",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilRunResponse)
}

// capturingMockClient captures the request for verification.
type capturingMockClient struct {
	reconciliationv1.AccountReconciliationServiceClient
	capturedReq *reconciliationv1.InitiateAccountReconciliationRequest
}

func (m *capturingMockClient) InitiateAccountReconciliation(
	_ context.Context,
	req *reconciliationv1.InitiateAccountReconciliationRequest,
	_ ...grpc.CallOption,
) (*reconciliationv1.InitiateAccountReconciliationResponse, error) {
	m.capturedReq = req
	return &reconciliationv1.InitiateAccountReconciliationResponse{
		Run: &reconciliationv1.SettlementRunSummary{RunId: "captured-run"},
	}, nil
}

func TestGrpcReconciliationClient_CorrectProtoMapping(t *testing.T) {
	mock := &capturingMockClient{}
	client := NewGrpcReconciliationClient(mock)

	periodStart := time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC)

	_, err := client.InitiateReconciliation(context.Background(), InitiateRequest{
		AccountID:      "ACC-001",
		Scope:          "INSTRUMENT",
		SettlementType: "WEEKLY",
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		InitiatedBy:    "settlement-scheduler",
	})
	require.NoError(t, err)

	req := mock.capturedReq
	require.NotNil(t, req)
	assert.Equal(t, "ACC-001", req.AccountId)
	assert.Equal(t, reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT, req.Scope)
	assert.Equal(t, reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY, req.SettlementType)
	assert.Equal(t, timestamppb.New(periodStart).AsTime(), req.PeriodStart.AsTime())
	assert.Equal(t, timestamppb.New(periodEnd).AsTime(), req.PeriodEnd.AsTime())
	assert.Equal(t, "settlement-scheduler", req.InitiatedBy)
}

package admin

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
)

func TestBalanceSheetHandler_GetBalanceSheet_InvalidRequest(t *testing.T) {
	svc := NewBalanceSheetService(&mockPKClient{}, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.GetBalanceSheetRequest{
		TenantId: "",
	}

	_, err := handler.GetBalanceSheet(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "tenant_id is required")
}

func TestBalanceSheetHandler_GetBalanceSheet_Success(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("acme_CASH_001", "log-1", "GBP",
			txnEntry{units: 5000, nanos: 0, direction: "DEBIT"},
		),
		makeLog("acme_ACCOUNTS_PAYABLE_001", "log-2", "GBP",
			txnEntry{units: 2000, nanos: 0, direction: "DEBIT"},
		),
		makeLog("acme_RETAINED_EARNINGS_001", "log-3", "GBP",
			txnEntry{units: 3000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.GetBalanceSheetRequest{
		TenantId: "acme",
	}

	resp, err := handler.GetBalanceSheet(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "acme", resp.TenantId)
	require.Len(t, resp.Sections, 3)

	// ASSETS
	assert.Equal(t, controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_ASSETS, resp.Sections[0].Classification)
	require.Len(t, resp.Sections[0].LineItems, 1)
	assert.Equal(t, "CASH", resp.Sections[0].LineItems[0].AccountType)
	assert.Equal(t, "GBP", resp.Sections[0].LineItems[0].Instrument)
	assert.Equal(t, "5000", resp.Sections[0].LineItems[0].Quantity)
	assert.Equal(t, controlplanev1.NormalBalanceDirection_NORMAL_BALANCE_DIRECTION_DEBIT, resp.Sections[0].LineItems[0].NormalBalance)

	// LIABILITIES
	assert.Equal(t, controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_LIABILITIES, resp.Sections[1].Classification)
	require.Len(t, resp.Sections[1].LineItems, 1)
	assert.Equal(t, "ACCOUNTS_PAYABLE", resp.Sections[1].LineItems[0].AccountType)

	// EQUITY
	assert.Equal(t, controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_EQUITY, resp.Sections[2].Classification)
	require.Len(t, resp.Sections[2].LineItems, 1)
	assert.Equal(t, "RETAINED_EARNINGS", resp.Sections[2].LineItems[0].AccountType)
}

func TestBalanceSheetHandler_GetPositionDetails_InvalidRequest(t *testing.T) {
	svc := NewBalanceSheetService(&mockPKClient{}, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	tests := []struct {
		name   string
		req    *controlplanev1.GetPositionDetailsRequest
		errMsg string
	}{
		{
			name: "empty tenant_id",
			req: &controlplanev1.GetPositionDetailsRequest{
				TenantId:    "",
				AccountType: "CASH",
				Instrument:  "GBP",
			},
			errMsg: "tenant_id is required",
		},
		{
			name: "empty account_type",
			req: &controlplanev1.GetPositionDetailsRequest{
				TenantId:    "acme",
				AccountType: "",
				Instrument:  "GBP",
			},
			errMsg: "account_type is required",
		},
		{
			name: "empty instrument",
			req: &controlplanev1.GetPositionDetailsRequest{
				TenantId:    "acme",
				AccountType: "CASH",
				Instrument:  "",
			},
			errMsg: "instrument is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := handler.GetPositionDetails(context.Background(), tt.req)
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
			assert.Contains(t, st.Message(), tt.errMsg)
		})
	}
}

func TestBalanceSheetHandler_GetPositionDetails_Success(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("tenant_CASH_001", "log-1", "GBP",
			txnEntry{units: 5000, nanos: 0, direction: "DEBIT"},
		),
		makeLog("tenant_CASH_002", "log-2", "GBP",
			txnEntry{units: 3000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.GetPositionDetailsRequest{
		TenantId:    "tenant",
		AccountType: "CASH",
		Instrument:  "GBP",
	}

	resp, err := handler.GetPositionDetails(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "CASH", resp.AccountType)
	assert.Equal(t, "GBP", resp.Instrument)
	assert.Len(t, resp.Positions, 2)
	assert.Equal(t, "8000", resp.Total)
}

func TestBalanceSheetHandler_ExportBalanceSheetCSV_InvalidRequest(t *testing.T) {
	svc := NewBalanceSheetService(&mockPKClient{}, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.ExportBalanceSheetCSVRequest{
		TenantId: "",
	}

	_, err := handler.ExportBalanceSheetCSV(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestBalanceSheetHandler_ExportBalanceSheetCSV_Success(t *testing.T) {
	logs := []*positionkeepingv1.FinancialPositionLog{
		makeLog("acme_CASH_001", "log-1", "GBP",
			txnEntry{units: 5000, nanos: 0, direction: "DEBIT"},
		),
	}

	client := &mockPKClient{logs: logs}
	svc := NewBalanceSheetService(client, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.ExportBalanceSheetCSVRequest{
		TenantId: "acme",
	}

	resp, err := handler.ExportBalanceSheetCSV(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.NotEmpty(t, resp.CsvContent)
	assert.Contains(t, resp.CsvContent, "CASH")
	assert.Contains(t, resp.CsvContent, "GBP")
	assert.Equal(t, "acme", resp.TenantId)
	assert.NotNil(t, resp.GeneratedAt)
}

func TestClassificationToProto(t *testing.T) {
	tests := []struct {
		input    BalanceSheetClassification
		expected controlplanev1.BalanceSheetClassification
	}{
		{ClassificationAssets, controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_ASSETS},
		{ClassificationLiabilities, controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_LIABILITIES},
		{ClassificationEquity, controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_EQUITY},
		{"UNKNOWN", controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			result := classificationToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBalanceSheetHandler_GetBalanceSheet_ServiceError(t *testing.T) {
	client := &mockPKClient{err: fmt.Errorf("database unavailable")}
	svc := NewBalanceSheetService(client, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.GetBalanceSheetRequest{
		TenantId: "acme",
	}

	_, err := handler.GetBalanceSheet(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to generate balance sheet")
}

func TestBalanceSheetHandler_GetPositionDetails_ServiceError(t *testing.T) {
	client := &mockPKClient{err: fmt.Errorf("connection refused")}
	svc := NewBalanceSheetService(client, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.GetPositionDetailsRequest{
		TenantId:    "acme",
		AccountType: "CASH",
		Instrument:  "GBP",
	}

	_, err := handler.GetPositionDetails(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to get position details")
}

func TestBalanceSheetHandler_ExportBalanceSheetCSV_ServiceError(t *testing.T) {
	client := &mockPKClient{err: fmt.Errorf("timeout")}
	svc := NewBalanceSheetService(client, nil)
	handler := NewBalanceSheetHandler(svc, nil)

	req := &controlplanev1.ExportBalanceSheetCSVRequest{
		TenantId: "acme",
	}

	_, err := handler.ExportBalanceSheetCSV(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to export balance sheet CSV")
}

func TestNormalBalanceToProto(t *testing.T) {
	tests := []struct {
		input    NormalBalance
		expected controlplanev1.NormalBalanceDirection
	}{
		{NormalBalanceDebit, controlplanev1.NormalBalanceDirection_NORMAL_BALANCE_DIRECTION_DEBIT},
		{NormalBalanceCredit, controlplanev1.NormalBalanceDirection_NORMAL_BALANCE_DIRECTION_CREDIT},
		{"UNKNOWN", controlplanev1.NormalBalanceDirection_NORMAL_BALANCE_DIRECTION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			result := normalBalanceToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

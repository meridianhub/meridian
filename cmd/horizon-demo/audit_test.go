package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	money "google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Test constants for audit tests.
const (
	auditTestAccountID       = "audit-test-account-123"
	auditTestInitialBalance  = int64(100000) // GBP 1,000.00
	auditTestPaymentAmount   = int64(10000)  // GBP 100.00
	auditTestExpectedBalance = int64(90000)  // GBP 900.00 (single payment)
	auditTestDoubleSpendBal  = int64(80000)  // GBP 800.00 (double payment)
)

// Static errors for audit tests.
var (
	errAuditServiceUnavailable = errors.New("current account service unavailable")
)

// mockAuditCurrentAccountClient implements CurrentAccountServiceClient for audit tests.
type mockAuditCurrentAccountClient struct {
	currentaccountv1.CurrentAccountServiceClient
	retrieveFunc func(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest, opts ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error)
}

func (m *mockAuditCurrentAccountClient) RetrieveCurrentAccount(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest, opts ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
	if m.retrieveFunc != nil {
		return m.retrieveFunc(ctx, req, opts...)
	}
	return nil, errNotImplemented
}

// createMockBalanceResponse creates a RetrieveCurrentAccountResponse with the given balance in pence.
func createMockBalanceResponse(balancePence int64) *currentaccountv1.RetrieveCurrentAccountResponse {
	units := balancePence / 100
	nanos := int32((balancePence % 100) * 10000000)

	return &currentaccountv1.RetrieveCurrentAccountResponse{
		Facility: &currentaccountv1.CurrentAccountFacility{
			CurrentBalance: &currentaccountv1.AccountBalance{
				CurrentBalance: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        units,
						Nanos:        nanos,
					},
				},
				AvailableBalance: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        units,
						Nanos:        nanos,
					},
				},
				LastUpdated: timestamppb.Now(),
			},
		},
	}
}

func TestValidateAuditConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *AuditConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
			errMsg:  "config is nil",
		},
		{
			name: "missing account ID",
			cfg: &AuditConfig{
				InitialBalancePence: auditTestInitialBalance,
				PaymentAmountPence:  auditTestPaymentAmount,
			},
			wantErr: true,
			errMsg:  "AccountID is required",
		},
		{
			name: "zero initial balance",
			cfg: &AuditConfig{
				AccountID:           auditTestAccountID,
				InitialBalancePence: 0,
				PaymentAmountPence:  auditTestPaymentAmount,
			},
			wantErr: true,
			errMsg:  "InitialBalancePence must be positive",
		},
		{
			name: "negative initial balance",
			cfg: &AuditConfig{
				AccountID:           auditTestAccountID,
				InitialBalancePence: -1000,
				PaymentAmountPence:  auditTestPaymentAmount,
			},
			wantErr: true,
			errMsg:  "InitialBalancePence must be positive",
		},
		{
			name: "zero payment amount",
			cfg: &AuditConfig{
				AccountID:           auditTestAccountID,
				InitialBalancePence: auditTestInitialBalance,
				PaymentAmountPence:  0,
			},
			wantErr: true,
			errMsg:  "PaymentAmountPence must be positive",
		},
		{
			name: "payment exceeds balance",
			cfg: &AuditConfig{
				AccountID:           auditTestAccountID,
				InitialBalancePence: auditTestPaymentAmount,
				PaymentAmountPence:  auditTestInitialBalance,
			},
			wantErr: true,
			errMsg:  "PaymentAmountPence",
		},
		{
			name: "valid config",
			cfg: &AuditConfig{
				AccountID:           auditTestAccountID,
				InitialBalancePence: auditTestInitialBalance,
				PaymentAmountPence:  auditTestPaymentAmount,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuditConfig(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
					return
				}
				if !errors.Is(err, ErrAuditConfigInvalid) {
					t.Errorf("expected ErrAuditConfigInvalid, got %v", err)
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunAudit_CorrectSinglePayment(t *testing.T) {
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			if req.GetAccountId() != auditTestAccountID {
				t.Errorf("expected account ID %q, got %q", auditTestAccountID, req.GetAccountId())
			}
			// Return correct balance (single payment executed)
			return createMockBalanceResponse(auditTestExpectedBalance), nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
		PaymentAmountPence:  auditTestPaymentAmount,
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Verdict != AuditVerdictPass {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictPass, result.Verdict)
	}

	if !result.BalanceCorrect {
		t.Error("expected BalanceCorrect to be true")
	}

	if result.BalanceStatus != BalanceStatusCorrect {
		t.Errorf("expected BalanceStatus %v, got %v", BalanceStatusCorrect, result.BalanceStatus)
	}

	if result.TransactionsRecorded != 1 {
		t.Errorf("expected TransactionsRecorded 1, got %d", result.TransactionsRecorded)
	}

	if !result.NoDoubleSpend {
		t.Error("expected NoDoubleSpend to be true")
	}

	if result.FinalBalancePence != auditTestExpectedBalance {
		t.Errorf("expected FinalBalancePence %d, got %d", auditTestExpectedBalance, result.FinalBalancePence)
	}
}

func TestRunAudit_DoubleSpendDetected(t *testing.T) {
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			// Return double-spend balance (two payments executed)
			return createMockBalanceResponse(auditTestDoubleSpendBal), nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
		PaymentAmountPence:  auditTestPaymentAmount,
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error for double-spend, got nil")
	}

	if !errors.Is(err, ErrAuditDoubleSpend) {
		t.Errorf("expected ErrAuditDoubleSpend, got %v", err)
	}

	if result.Verdict != AuditVerdictFail {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictFail, result.Verdict)
	}

	if result.BalanceCorrect {
		t.Error("expected BalanceCorrect to be false")
	}

	if result.BalanceStatus != BalanceStatusDoubleSpend {
		t.Errorf("expected BalanceStatus %v, got %v", BalanceStatusDoubleSpend, result.BalanceStatus)
	}

	if result.TransactionsRecorded != 2 {
		t.Errorf("expected TransactionsRecorded 2, got %d", result.TransactionsRecorded)
	}

	if result.NoDoubleSpend {
		t.Error("expected NoDoubleSpend to be false")
	}
}

func TestRunAudit_NoPaymentExecuted(t *testing.T) {
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			// Return initial balance (no payment executed)
			return createMockBalanceResponse(auditTestInitialBalance), nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
		PaymentAmountPence:  auditTestPaymentAmount,
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error for no payment, got nil")
	}

	if !errors.Is(err, ErrAuditNoPayment) {
		t.Errorf("expected ErrAuditNoPayment, got %v", err)
	}

	if result.Verdict != AuditVerdictFail {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictFail, result.Verdict)
	}

	if result.BalanceStatus != BalanceStatusNoPayment {
		t.Errorf("expected BalanceStatus %v, got %v", BalanceStatusNoPayment, result.BalanceStatus)
	}

	if result.TransactionsRecorded != 0 {
		t.Errorf("expected TransactionsRecorded 0, got %d", result.TransactionsRecorded)
	}
}

func TestRunAudit_UnexpectedBalance(t *testing.T) {
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			// Return unexpected balance (e.g., partial payment or other anomaly)
			return createMockBalanceResponse(95000), nil // GBP 950.00 - doesn't match any expected
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
		PaymentAmountPence:  auditTestPaymentAmount,
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error for unexpected balance, got nil")
	}

	if !errors.Is(err, ErrAuditUnexpectedState) {
		t.Errorf("expected ErrAuditUnexpectedState, got %v", err)
	}

	if result.Verdict != AuditVerdictFail {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictFail, result.Verdict)
	}

	if result.BalanceStatus != BalanceStatusUnexpected {
		t.Errorf("expected BalanceStatus %v, got %v", BalanceStatusUnexpected, result.BalanceStatus)
	}

	if result.TransactionsRecorded != -1 {
		t.Errorf("expected TransactionsRecorded -1 (unknown), got %d", result.TransactionsRecorded)
	}
}

func TestRunAudit_ServiceError(t *testing.T) {
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, errAuditServiceUnavailable
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
		PaymentAmountPence:  auditTestPaymentAmount,
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrAuditBalanceRetrieval) {
		t.Errorf("expected ErrAuditBalanceRetrieval, got %v", err)
	}

	if result.Verdict != AuditVerdictError {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictError, result.Verdict)
	}
}

func TestRunAudit_NilFacility(t *testing.T) {
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			// Return response with nil facility
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: nil,
			}, nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
		PaymentAmountPence:  auditTestPaymentAmount,
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error for nil facility, got nil")
	}

	if !errors.Is(err, ErrAuditBalanceRetrieval) {
		t.Errorf("expected ErrAuditBalanceRetrieval, got %v", err)
	}

	if result.Verdict != AuditVerdictError {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictError, result.Verdict)
	}
}

func TestRunAudit_NilCurrentBalance(t *testing.T) {
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			// Return response with nil current balance
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					CurrentBalance: nil,
				},
			}, nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
		PaymentAmountPence:  auditTestPaymentAmount,
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)

	if err == nil {
		t.Fatal("expected error for nil current balance, got nil")
	}

	if !errors.Is(err, ErrAuditBalanceRetrieval) {
		t.Errorf("expected ErrAuditBalanceRetrieval, got %v", err)
	}

	if result.Verdict != AuditVerdictError {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictError, result.Verdict)
	}
}

func TestRunAudit_InvalidConfig(t *testing.T) {
	clients := &Clients{}

	result, err := RunAudit(context.Background(), clients, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if result != nil {
		t.Error("expected nil result for invalid config")
	}
}

func TestDefaultAuditConfig(t *testing.T) {
	cfg := DefaultAuditConfig()

	if cfg.InitialBalancePence != 100000 {
		t.Errorf("expected InitialBalancePence 100000, got %d", cfg.InitialBalancePence)
	}

	if cfg.PaymentAmountPence != 10000 {
		t.Errorf("expected PaymentAmountPence 10000, got %d", cfg.PaymentAmountPence)
	}

	if cfg.Logger == nil {
		t.Error("expected Logger to be set")
	}
}

func TestNewAuditConfigFromPreFlight(t *testing.T) {
	preflight := &PreFlightResult{
		AccountID:           auditTestAccountID,
		InitialBalancePence: auditTestInitialBalance,
	}

	cfg := NewAuditConfigFromPreFlight(preflight, auditTestPaymentAmount, nil)

	if cfg.AccountID != auditTestAccountID {
		t.Errorf("expected AccountID %q, got %q", auditTestAccountID, cfg.AccountID)
	}

	if cfg.InitialBalancePence != auditTestInitialBalance {
		t.Errorf("expected InitialBalancePence %d, got %d", auditTestInitialBalance, cfg.InitialBalancePence)
	}

	if cfg.PaymentAmountPence != auditTestPaymentAmount {
		t.Errorf("expected PaymentAmountPence %d, got %d", auditTestPaymentAmount, cfg.PaymentAmountPence)
	}

	if cfg.Logger == nil {
		t.Error("expected Logger to be set")
	}
}

func TestNewAuditConfigFromPreFlight_NilPreflight(t *testing.T) {
	cfg := NewAuditConfigFromPreFlight(nil, auditTestPaymentAmount, nil)

	if cfg != nil {
		t.Errorf("expected nil config for nil preflight, got %+v", cfg)
	}
}

func TestAuditResult_ToVerificationReport(t *testing.T) {
	result := &AuditResult{
		BalanceCorrect:       true,
		TransactionsRecorded: 1,
		NoDoubleSpend:        true,
	}

	report := result.ToVerificationReport(2)

	if report.RequestsSent != 2 {
		t.Errorf("expected RequestsSent 2, got %d", report.RequestsSent)
	}

	if report.TransactionsRecorded != 1 {
		t.Errorf("expected TransactionsRecorded 1, got %d", report.TransactionsRecorded)
	}

	if !report.BalanceCorrect {
		t.Error("expected BalanceCorrect to be true")
	}

	if !report.NoDoubleSpend {
		t.Error("expected NoDoubleSpend to be true")
	}
}

func TestBalanceStatus_String(t *testing.T) {
	tests := []struct {
		status   BalanceStatus
		expected string
	}{
		{BalanceStatusCorrect, "CORRECT"},
		{BalanceStatusDoubleSpend, "DOUBLE_SPEND"},
		{BalanceStatusNoPayment, "NO_PAYMENT"},
		{BalanceStatusUnexpected, "UNEXPECTED"},
		{BalanceStatus(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestAuditVerdict_String(t *testing.T) {
	tests := []struct {
		verdict  AuditVerdict
		expected string
	}{
		{AuditVerdictPass, "PASS"},
		{AuditVerdictFail, "FAIL"},
		{AuditVerdictError, "ERROR"},
		{AuditVerdict(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.verdict.String(); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestRunAudit_WithFractionalBalance(t *testing.T) {
	// Test with a balance that has fractional pence (e.g., 90050 pence = GBP 900.50)
	mockClient := &mockAuditCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest, _ ...grpc.CallOption) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			// GBP 900.50 = 90050 pence
			return createMockBalanceResponse(90050), nil
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
	}

	cfg := &AuditConfig{
		AccountID:           auditTestAccountID,
		InitialBalancePence: 100050, // GBP 1,000.50
		PaymentAmountPence:  10000,  // GBP 100.00
		Logger:              slog.Default(),
	}

	ctx := context.Background()
	result, err := RunAudit(ctx, clients, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Verdict != AuditVerdictPass {
		t.Errorf("expected Verdict %v, got %v", AuditVerdictPass, result.Verdict)
	}

	if result.FinalBalancePence != 90050 {
		t.Errorf("expected FinalBalancePence 90050, got %d", result.FinalBalancePence)
	}
}

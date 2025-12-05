package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Test errors for lien audit tests.
var errLienServiceUnavailable = errors.New("lien service unavailable")

// mockCurrentAccountLienClient implements CurrentAccountServiceClient for lien audit testing.
type mockCurrentAccountLienClient struct {
	currentaccountv1.CurrentAccountServiceClient
	retrieveLienResponse *currentaccountv1.RetrieveLienResponse
	retrieveLienError    error
}

func (m *mockCurrentAccountLienClient) RetrieveLien(
	_ context.Context,
	_ *currentaccountv1.RetrieveLienRequest,
	_ ...grpc.CallOption,
) (*currentaccountv1.RetrieveLienResponse, error) {
	if m.retrieveLienError != nil {
		return nil, m.retrieveLienError
	}
	return m.retrieveLienResponse, nil
}

func TestValidateLienAuditConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *LienAuditConfig
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
			name: "empty LienID",
			cfg: &LienAuditConfig{
				AccountID: "test-account",
			},
			wantErr: true,
			errMsg:  "LienID is required",
		},
		{
			name: "empty AccountID",
			cfg: &LienAuditConfig{
				LienID: "test-lien",
			},
			wantErr: true,
			errMsg:  "AccountID is required",
		},
		{
			name: "valid config",
			cfg: &LienAuditConfig{
				LienID:    "test-lien",
				AccountID: "test-account",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLienAuditConfig(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.True(t, errors.Is(err, ErrLienAuditConfigInvalid))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRunLienAudit_LienExecuted(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienResponse: &currentaccountv1.RetrieveLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId:    "lien-123",
				AccountId: "test-account",
				Status:    currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED,
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
			},
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         logger,
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    logger,
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "lien-123", result.LienID)
	assert.Equal(t, "test-account", result.AccountID)
	assert.Equal(t, "LIEN_STATUS_EXECUTED", result.LienStatus)
	assert.False(t, result.IsOrphaned)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
	assert.Nil(t, result.Error)
}

func TestRunLienAudit_LienActive_Orphaned(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienResponse: &currentaccountv1.RetrieveLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId:    "lien-123",
				AccountId: "test-account",
				Status:    currentaccountv1.LienStatus_LIEN_STATUS_ACTIVE,
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
			},
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         logger,
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    logger,
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	// Non-blocking check returns nil error but sets IsOrphaned
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "LIEN_STATUS_ACTIVE", result.LienStatus)
	assert.True(t, result.IsOrphaned)
	assert.Equal(t, AuditVerdictPass, result.Verdict) // Non-blocking
	assert.True(t, errors.Is(result.Error, ErrLienAuditOrphaned))
}

func TestRunLienAudit_LienTerminated(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienResponse: &currentaccountv1.RetrieveLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId:    "lien-123",
				AccountId: "test-account",
				Status:    currentaccountv1.LienStatus_LIEN_STATUS_TERMINATED,
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
			},
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         logger,
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    logger,
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "LIEN_STATUS_TERMINATED", result.LienStatus)
	assert.False(t, result.IsOrphaned)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
	assert.Nil(t, result.Error)
}

func TestRunLienAudit_RetrieveError(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienError: errLienServiceUnavailable,
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         logger,
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    logger,
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictError, result.Verdict)
	assert.True(t, errors.Is(result.Error, ErrLienAuditRetrieveFailed))
}

func TestRunLienAudit_NilLienInResponse(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienResponse: &currentaccountv1.RetrieveLienResponse{
			Lien: nil, // nil lien
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         logger,
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    logger,
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictError, result.Verdict)
	assert.True(t, errors.Is(result.Error, ErrLienAuditRetrieveFailed))
}

func TestRunLienAudit_AccountMismatch(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienResponse: &currentaccountv1.RetrieveLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId:    "lien-123",
				AccountId: "different-account", // Different from config
				Status:    currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED,
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
			},
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         logger,
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    logger,
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	// Should still pass but logs a warning about account mismatch
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
}

func TestRunLienAudit_NilConfig(t *testing.T) {
	ctx := context.Background()
	clients := &Clients{
		logger: slog.Default(),
	}

	result, err := RunLienAudit(ctx, clients, nil)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, errors.Is(err, ErrLienAuditConfigInvalid))
}

func TestRunLienAudit_NilLogger(t *testing.T) {
	ctx := context.Background()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienResponse: &currentaccountv1.RetrieveLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId:    "lien-123",
				AccountId: "test-account",
				Status:    currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED,
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
			},
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         slog.Default(),
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    nil, // nil logger should default to slog.Default()
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, AuditVerdictPass, result.Verdict)
}

func TestNewLienAuditConfig(t *testing.T) {
	logger := slog.Default()

	cfg := NewLienAuditConfig("lien-123", "account-456", logger)

	require.NotNil(t, cfg)
	assert.Equal(t, "lien-123", cfg.LienID)
	assert.Equal(t, "account-456", cfg.AccountID)
	assert.Equal(t, logger, cfg.Logger)
}

func TestNewLienAuditConfig_NilLogger(t *testing.T) {
	cfg := NewLienAuditConfig("lien-123", "account-456", nil)

	require.NotNil(t, cfg)
	assert.Equal(t, "lien-123", cfg.LienID)
	assert.Equal(t, "account-456", cfg.AccountID)
	assert.NotNil(t, cfg.Logger) // Should default to slog.Default()
}

func TestNewLienAuditConfigFromOrderAudit(t *testing.T) {
	logger := slog.Default()

	orderResult := &OrderAuditResult{
		AccountID: "test-account",
		MatchingOrders: []PaymentOrderSummary{
			{
				PaymentOrderID: "order-123",
				LienID:         "lien-456",
			},
		},
	}

	cfg := NewLienAuditConfigFromOrderAudit(orderResult, logger)

	require.NotNil(t, cfg)
	assert.Equal(t, "lien-456", cfg.LienID)
	assert.Equal(t, "test-account", cfg.AccountID)
	assert.Equal(t, logger, cfg.Logger)
}

func TestNewLienAuditConfigFromOrderAudit_NilResult(t *testing.T) {
	cfg := NewLienAuditConfigFromOrderAudit(nil, slog.Default())
	assert.Nil(t, cfg)
}

func TestNewLienAuditConfigFromOrderAudit_NoMatchingOrders(t *testing.T) {
	orderResult := &OrderAuditResult{
		AccountID:      "test-account",
		MatchingOrders: []PaymentOrderSummary{},
	}

	cfg := NewLienAuditConfigFromOrderAudit(orderResult, slog.Default())
	assert.Nil(t, cfg)
}

func TestNewLienAuditConfigFromOrderAudit_EmptyLienID(t *testing.T) {
	orderResult := &OrderAuditResult{
		AccountID: "test-account",
		MatchingOrders: []PaymentOrderSummary{
			{
				PaymentOrderID: "order-123",
				LienID:         "", // Empty lien ID
			},
		},
	}

	cfg := NewLienAuditConfigFromOrderAudit(orderResult, slog.Default())
	assert.Nil(t, cfg)
}

func TestRunLienAudit_UnexpectedStatus(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	mockClient := &mockCurrentAccountLienClient{
		retrieveLienResponse: &currentaccountv1.RetrieveLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId:    "lien-123",
				AccountId: "test-account",
				Status:    currentaccountv1.LienStatus_LIEN_STATUS_UNSPECIFIED, // Unexpected
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
			},
		},
	}

	clients := &Clients{
		CurrentAccount: mockClient,
		logger:         logger,
	}

	cfg := &LienAuditConfig{
		LienID:    "lien-123",
		AccountID: "test-account",
		Logger:    logger,
	}

	result, err := RunLienAudit(ctx, clients, cfg)

	require.NoError(t, err) // Non-blocking
	require.NotNil(t, result)
	assert.Equal(t, "LIEN_STATUS_UNSPECIFIED", result.LienStatus)
	assert.False(t, result.IsOrphaned)
	assert.Equal(t, AuditVerdictPass, result.Verdict) // Non-blocking
	assert.True(t, errors.Is(result.Error, ErrLienAuditUnexpectedStatus))
}

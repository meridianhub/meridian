// Package main provides pre-flight setup for the Horizon Integrity Proof demo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	money "google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PreFlightConfig holds configuration for the pre-flight setup phase.
type PreFlightConfig struct {
	// InitialDepositPence is the amount to deposit in the test account (default: 100000 = GBP 1,000.00)
	InitialDepositPence int64
	// Logger for structured logging
	Logger *slog.Logger
}

// PreFlightResult captures the results of the pre-flight setup.
type PreFlightResult struct {
	// AccountID is the unique identifier of the created test account
	AccountID string
	// AccountIdentification is the IBAN-style identifier
	AccountIdentification string
	// InitialBalancePence is the balance after deposit (should equal InitialDepositPence)
	InitialBalancePence int64
	// DepositTransactionID is the ID of the deposit transaction
	DepositTransactionID string
	// CreatedAt is when the account was created
	CreatedAt time.Time
}

// Pre-flight errors.
var (
	ErrAccountCreationFailed     = errors.New("failed to create test account")
	ErrDepositFailed             = errors.New("failed to deposit funds")
	ErrBalanceVerificationFailed = errors.New("balance verification failed")
	ErrInvalidBalanceAmount      = errors.New("balance does not match expected amount")
)

// GenerateTestAccountID creates a unique account ID for the demo.
// Format: HORIZON-TEST-{unix-timestamp}
func GenerateTestAccountID() string {
	return fmt.Sprintf("HORIZON-TEST-%d", time.Now().Unix())
}

// GenerateTestIBAN creates a test IBAN for the demo account.
// Uses a GB test IBAN format.
func GenerateTestIBAN(timestamp int64) string {
	// GB82 WEST 1234 5698 7654 32 is a valid test IBAN format
	// We append timestamp digits to make it unique
	return fmt.Sprintf("GB82WEST%014d", timestamp%100000000000000)
}

// RunPreFlight executes the pre-flight setup phase of the demo.
// This creates a test account and deposits baseline funds.
//
// Steps:
// 1. Generate unique account ID (HORIZON-TEST-{timestamp})
// 2. Call InitiateCurrentAccount to create the account
// 3. Call ExecuteDeposit to add baseline funds (GBP 1,000.00)
// 4. Call RetrieveCurrentAccount to verify the balance
func RunPreFlight(ctx context.Context, clients *Clients, cfg *PreFlightConfig) (*PreFlightResult, error) {
	if clients == nil {
		return nil, fmt.Errorf("%w: clients is nil", ErrAccountCreationFailed)
	}
	if cfg == nil {
		cfg = &PreFlightConfig{
			InitialDepositPence: 100000, // GBP 1,000.00
			Logger:              slog.Default(),
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.InitialDepositPence <= 0 {
		cfg.InitialDepositPence = 100000
	}

	logger := cfg.Logger
	timestamp := time.Now().Unix()

	// Step 1: Generate unique identifiers
	accountID := GenerateTestAccountID()
	iban := GenerateTestIBAN(timestamp)

	logger.Info("pre-flight: creating test account",
		"account_id", accountID,
		"iban", iban,
	)

	// Step 2: Create the test account
	createResp, err := clients.CurrentAccount.InitiateCurrentAccount(ctx, &currentaccountv1.InitiateCurrentAccountRequest{
		PartyId:            accountID, // Use account ID as party ID for demo
		ExternalIdentifier: iban,
		InstrumentCode:     "GBP",
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccountCreationFailed, err)
	}

	createdAccountID := createResp.GetAccountId()
	if createdAccountID == "" {
		createdAccountID = accountID
	}

	logger.Info("pre-flight: account created",
		"account_id", createdAccountID,
		"status", createResp.GetFacility().GetAccountStatus().String(),
	)

	// Step 3: Deposit baseline funds
	depositAmount := penceToPounds(cfg.InitialDepositPence)
	logger.Info("pre-flight: depositing funds",
		"account_id", createdAccountID,
		"amount_pence", cfg.InitialDepositPence,
		"amount_gbp", fmt.Sprintf("%.2f", float64(cfg.InitialDepositPence)/100),
	)

	depositResp, err := clients.CurrentAccount.ExecuteDeposit(ctx, &currentaccountv1.ExecuteDepositRequest{
		AccountId: createdAccountID,
		Amount: &commonv1.MoneyAmount{
			Amount: depositAmount,
		},
		Description: "Horizon demo initial deposit",
		Reference:   fmt.Sprintf("HORIZON-DEPOSIT-%d", timestamp),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDepositFailed, err)
	}

	if depositResp.GetStatus() != currentaccountv1.TransactionStatus_TRANSACTION_STATUS_COMPLETED {
		return nil, fmt.Errorf("%w: deposit status is %s, expected COMPLETED",
			ErrDepositFailed, depositResp.GetStatus().String())
	}

	logger.Info("pre-flight: deposit completed",
		"transaction_id", depositResp.GetTransactionId(),
		"new_balance_units", depositResp.GetNewBalance().GetAmount().GetUnits(),
	)

	// Step 4: Verify the balance
	retrieveResp, err := clients.CurrentAccount.RetrieveCurrentAccount(ctx, &currentaccountv1.RetrieveCurrentAccountRequest{
		AccountId: createdAccountID,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBalanceVerificationFailed, err)
	}

	actualBalancePence := moneyToPence(retrieveResp.GetFacility().GetCurrentBalance().GetCurrentBalance().GetAmount())
	if actualBalancePence != cfg.InitialDepositPence {
		return nil, fmt.Errorf("%w: expected %d pence, got %d pence",
			ErrInvalidBalanceAmount, cfg.InitialDepositPence, actualBalancePence)
	}

	logger.Info("pre-flight: balance verified",
		"account_id", createdAccountID,
		"balance_pence", actualBalancePence,
		"balance_gbp", fmt.Sprintf("%.2f", float64(actualBalancePence)/100),
	)

	createdAt := time.Now()
	if retrieveResp.GetFacility().GetCreatedAt() != nil {
		createdAt = retrieveResp.GetFacility().GetCreatedAt().AsTime()
	}

	return &PreFlightResult{
		AccountID:             createdAccountID,
		AccountIdentification: iban,
		InitialBalancePence:   actualBalancePence,
		DepositTransactionID:  depositResp.GetTransactionId(),
		CreatedAt:             createdAt,
	}, nil
}

// penceToPounds converts pence to google.type.Money with GBP currency.
// For example, 100000 pence becomes Money{CurrencyCode: "GBP", Units: 1000, Nanos: 0}
func penceToPounds(pence int64) *money.Money {
	units := pence / 100
	// pence % 100 is always 0-99, so max value is 99 * 10000000 = 990000000
	// which safely fits in int32 (max ~2.1 billion)
	fractionalPence := pence % 100
	nanos := int32(fractionalPence) * 10000000 // #nosec G115 - safe: fractionalPence is 0-99

	return &money.Money{
		CurrencyCode: "GBP",
		Units:        units,
		Nanos:        nanos,
	}
}

// moneyToPence converts google.type.Money to pence.
// Assumes GBP currency where 1 unit = 100 pence.
func moneyToPence(m *money.Money) int64 {
	if m == nil {
		return 0
	}
	// Units are whole pounds, nanos are fractional
	// 1 nano = 0.000000001 pounds
	// 1 pence = 0.01 pounds = 10,000,000 nanos
	penceFromUnits := m.GetUnits() * 100
	penceFromNanos := int64(m.GetNanos() / 10000000)
	return penceFromUnits + penceFromNanos
}

// IsRetryableError checks if a gRPC error is transient and should be retried.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	st, ok := status.FromError(err)
	if !ok {
		return false
	}

	code := st.Code()
	return code == codes.Unavailable ||
		code == codes.ResourceExhausted ||
		code == codes.Aborted ||
		code == codes.DeadlineExceeded
}

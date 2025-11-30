package service

import (
	"context"
	"strings"
	"testing"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/platform/testdb"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// mustNewMoney is a test helper that creates Money or panics
func mustNewMoney(currency string, amountCents int64) domain.Money {
	m, err := domain.NewMoney(currency, amountCents)
	if err != nil {
		panic(err)
	}
	return m
}

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupPostgres(t, []interface{}{&persistence.CurrentAccountEntity{}})
}

func TestInitiateCurrentAccount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	req := &pb.InitiateCurrentAccountRequest{
		AccountIdentification: "GB82WEST12345698765432",
		CustomerId:            "CUST-001",
		BaseCurrency:          commonpb.Currency_CURRENCY_GBP,
	}

	resp, err := svc.InitiateCurrentAccount(context.Background(), req)
	if err != nil {
		t.Fatalf("InitiateCurrentAccount failed: %v", err)
	}

	if resp.AccountId == "" {
		t.Error("Expected non-empty account ID")
	}

	if resp.Facility == nil {
		t.Fatal("Expected facility in response")
	}

	if resp.Facility.AccountIdentification != req.AccountIdentification {
		t.Errorf("Expected IBAN %s, got %s", req.AccountIdentification, resp.Facility.AccountIdentification)
	}

	if resp.Facility.AccountStatus != pb.AccountStatus_ACCOUNT_STATUS_ACTIVE {
		t.Errorf("Expected ACTIVE status, got %v", resp.Facility.AccountStatus)
	}
}

func TestExecuteDeposit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	// Create account first
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	if err := repo.Save(account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Execute deposit
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        500000000, // £100.50
			},
		},
		Description: "Test deposit",
	}

	resp, err := svc.ExecuteDeposit(context.Background(), req)
	if err != nil {
		t.Fatalf("ExecuteDeposit failed: %v", err)
	}

	if resp.AccountId != "ACC-001" {
		t.Errorf("Expected account ID ACC-001, got %s", resp.AccountId)
	}

	if resp.TransactionId == "" {
		t.Error("Expected non-empty transaction ID")
	}

	if resp.Status != pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED {
		t.Errorf("Expected COMPLETED status, got %v", resp.Status)
	}

	// Verify balance
	if resp.NewBalance == nil {
		t.Fatal("Expected new balance in response")
	}

	expectedUnits := int64(100)
	if resp.NewBalance.Amount.Units != expectedUnits {
		t.Errorf("Expected balance units %d, got %d", expectedUnits, resp.NewBalance.Amount.Units)
	}
}

func TestExecuteDepositAccountNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-NONEXISTENT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
	}

	_, err := svc.ExecuteDeposit(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error for non-existent account")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected NotFound code, got %v", st.Code())
	}
}

func TestExecuteDepositInvalidAmount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	// Create account first
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	if err := repo.Save(account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Try deposit with zero amount
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        0,
				Nanos:        0,
			},
		},
	}

	_, err = svc.ExecuteDeposit(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error for zero amount")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument code, got %v", st.Code())
	}
}

func TestRetrieveCurrentAccount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	// Create account first
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	if err := repo.Save(account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Retrieve account
	req := &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-001",
	}

	resp, err := svc.RetrieveCurrentAccount(context.Background(), req)
	if err != nil {
		t.Fatalf("RetrieveCurrentAccount failed: %v", err)
	}

	if resp.Facility == nil {
		t.Fatal("Expected facility in response")
	}

	if resp.Facility.AccountId != "ACC-001" {
		t.Errorf("Expected account ID ACC-001, got %s", resp.Facility.AccountId)
	}

	if resp.Facility.AccountStatus != pb.AccountStatus_ACCOUNT_STATUS_ACTIVE {
		t.Errorf("Expected ACTIVE status, got %v", resp.Facility.AccountStatus)
	}
}

func TestRetrieveCurrentAccountNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	req := &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-NONEXISTENT",
	}

	_, err := svc.RetrieveCurrentAccount(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error for non-existent account")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected NotFound code, got %v", st.Code())
	}
}

func TestCurrencyMapping(t *testing.T) {
	tests := []struct {
		name     string
		currency commonpb.Currency
		expected string
	}{
		{"GBP", commonpb.Currency_CURRENCY_GBP, "GBP"},
		{"USD", commonpb.Currency_CURRENCY_USD, "USD"},
		{"EUR", commonpb.Currency_CURRENCY_EUR, "EUR"},
		{"Unspecified returns empty", commonpb.Currency_CURRENCY_UNSPECIFIED, ""},
		{"Unsupported JPY returns empty", commonpb.Currency_CURRENCY_JPY, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapCurrency(tt.currency)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestExecuteDepositCurrencyMismatch(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	// Create GBP account
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	if err := repo.Save(account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Try to deposit USD to GBP account
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        100,
				Nanos:        0,
			},
		},
	}

	_, err = svc.ExecuteDeposit(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error for currency mismatch")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument code, got %v", st.Code())
	}

	if !strings.Contains(st.Message(), "currency mismatch") {
		t.Errorf("Expected 'currency mismatch' in error message, got: %s", st.Message())
	}
}

func TestInitiateCurrentAccountUnsupportedCurrency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	req := &pb.InitiateCurrentAccountRequest{
		AccountIdentification: "GB82WEST12345698765432",
		CustomerId:            "CUST-001",
		BaseCurrency:          commonpb.Currency_CURRENCY_JPY,
	}

	_, err := svc.InitiateCurrentAccount(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error for unsupported currency")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument code, got %v", st.Code())
	}
}

func TestToMoneyAmount(t *testing.T) {
	tests := []struct {
		name          string
		input         domain.Money
		expectedUnits int64
		expectedNanos int32
	}{
		{
			name:          "Positive amount",
			input:         mustNewMoney("GBP", 12345), // £123.45
			expectedUnits: 123,
			expectedNanos: 450000000,
		},
		{
			name:          "Negative amount",
			input:         mustNewMoney("GBP", -12345), // -£123.45
			expectedUnits: -123,
			expectedNanos: -450000000, // Nanos must share sign per google.type.Money spec
		},
		{
			name:          "Zero amount",
			input:         mustNewMoney("USD", 0),
			expectedUnits: 0,
			expectedNanos: 0,
		},
		{
			name:          "Whole units (no fractional)",
			input:         mustNewMoney("EUR", 10000), // €100.00
			expectedUnits: 100,
			expectedNanos: 0,
		},
		{
			name:          "Negative whole units",
			input:         mustNewMoney("EUR", -10000), // -€100.00
			expectedUnits: -100,
			expectedNanos: 0,
		},
		{
			name:          "Small negative amount",
			input:         mustNewMoney("GBP", -123), // -£1.23
			expectedUnits: -1,
			expectedNanos: -230000000, // Nanos must share sign per google.type.Money spec
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toMoneyAmount(tt.input)

			if result.Amount.CurrencyCode != tt.input.Currency() {
				t.Errorf("Expected currency %s, got %s", tt.input.Currency(), result.Amount.CurrencyCode)
			}

			if result.Amount.Units != tt.expectedUnits {
				t.Errorf("Expected units %d, got %d", tt.expectedUnits, result.Amount.Units)
			}

			if result.Amount.Nanos != tt.expectedNanos {
				t.Errorf("Expected nanos %d, got %d", tt.expectedNanos, result.Amount.Nanos)
			}
		})
	}
}

// Defensive tests for overflow scenarios per ADR-008

func TestExecuteDeposit_OverflowPrevention_UnitsTooCents(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	// Create account
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(account))

	// Test: Units value that would overflow when multiplied by 100
	tests := []struct {
		name      string
		units     int64
		wantErr   bool
		rationale string
	}{
		{
			name:      "max safe units",
			units:     92233720368547758, // MaxInt64/100
			wantErr:   false,
			rationale: "Boundary value: should succeed at conversion",
		},
		{
			name:      "overflow positive units",
			units:     92233720368547759, // MaxInt64/100 + 1
			wantErr:   true,
			rationale: "Units * 100 would overflow int64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &pb.ExecuteDepositRequest{
				AccountId: "ACC-001",
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        tt.units,
						Nanos:        0,
					},
				},
			}

			_, err := svc.ExecuteDeposit(context.Background(), req)

			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				st, ok := status.FromError(err)
				require.True(t, ok, "Expected gRPC status error")
				if st.Code() != codes.InvalidArgument {
					t.Errorf("Expected InvalidArgument, got %v", st.Code())
				}
				if !strings.Contains(st.Message(), "overflow") {
					t.Errorf("Error should mention overflow, got: %s", st.Message())
				}
			} else {
				require.NoError(t, err, tt.rationale)
			}
		})
	}
}

func TestExecuteDeposit_SafeAddition_UnitsAndNanos(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo, nil)

	// Create account
	account, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(account))

	// Test: Large units + nanos uses Money.Add() safely
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        92233720368547758, // MaxInt64/100
				Nanos:        990000000,         // 99 cents when rounded
			},
		},
	}

	// This should fail with overflow error from Money.Add, not panic or succeed
	_, err = svc.ExecuteDeposit(context.Background(), req)
	require.Error(t, err, "overflow scenario should surface an error, not succeed")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument, got %v", st.Code())
	}
	if !strings.Contains(st.Message(), "overflow") {
		t.Errorf("Error should mention overflow, got: %s", st.Message())
	}
}

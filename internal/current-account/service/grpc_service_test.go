package service

import (
	"context"
	"strings"
	"testing"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Run migrations
	if err := db.AutoMigrate(&persistence.CurrentAccountEntity{}); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return db, cleanup
}

func TestInitiateCurrentAccount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := NewService(repo)

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
	svc := NewService(repo)

	// Create account first
	account := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
	svc := NewService(repo)

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
	svc := NewService(repo)

	// Create account first
	account := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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

	_, err := svc.ExecuteDeposit(context.Background(), req)
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
	svc := NewService(repo)

	// Create account first
	account := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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
	svc := NewService(repo)

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
	svc := NewService(repo)

	// Create GBP account
	account := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", "CUST-001", "GBP")
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

	_, err := svc.ExecuteDeposit(context.Background(), req)
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
	svc := NewService(repo)

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

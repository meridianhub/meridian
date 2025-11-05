package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/internal/financial-accounting/domain"
	"github.com/meridianhub/meridian/internal/platform/testdb"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _ := testdb.SetupPostgres(t, &persistence.LedgerPostingEntity{}, &persistence.FinancialBookingLogEntity{})
	return db
}

func TestProcessDeposit(t *testing.T) {
	db := setupTestDB(t)
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:     "ACC-123",
		AmountCents:   10000, // £100.00
		Currency:      "GBP",
		CorrelationID: "deposit-001",
		ValueDate:     time.Now(),
	}

	ctx := context.Background()
	err := service.ProcessDeposit(ctx, event)
	if err != nil {
		t.Fatalf("ProcessDeposit failed: %v", err)
	}

	// Verify two postings were created
	var count int64
	db.Model(&persistence.LedgerPostingEntity{}).Count(&count)
	if count != 2 {
		t.Errorf("Expected 2 postings, got %d", count)
	}

	// Verify debit posting
	var debitEntity persistence.LedgerPostingEntity
	err = db.Where("account_id = ? AND posting_direction = ?", "ACC-123", "DEBIT").First(&debitEntity).Error
	if err != nil {
		t.Errorf("Failed to find debit posting: %v", err)
	}

	if debitEntity.AmountCents != 10000 {
		t.Errorf("Expected debit amount 10000, got %d", debitEntity.AmountCents)
	}

	if debitEntity.Status != "POSTED" {
		t.Errorf("Expected debit status POSTED, got %s", debitEntity.Status)
	}

	// Verify credit posting
	var creditEntity persistence.LedgerPostingEntity
	err = db.Where("account_id = ? AND posting_direction = ?", "BANK-CASH-001", "CREDIT").First(&creditEntity).Error
	if err != nil {
		t.Errorf("Failed to find credit posting: %v", err)
	}

	if creditEntity.AmountCents != 10000 {
		t.Errorf("Expected credit amount 10000, got %d", creditEntity.AmountCents)
	}

	// Verify same booking log ID
	if debitEntity.FinancialBookingLogID != creditEntity.FinancialBookingLogID {
		t.Error("Expected both postings to have same booking log ID")
	}
}

func TestValidateDoubleEntry(t *testing.T) {
	db := setupTestDB(t)
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")
	ctx := context.Background()

	// Process a deposit (creates balanced entries)
	event := DepositEvent{
		AccountID:     "ACC-456",
		AmountCents:   50000,
		Currency:      "GBP",
		CorrelationID: "deposit-002",
		ValueDate:     time.Now(),
	}

	err := service.ProcessDeposit(ctx, event)
	if err != nil {
		t.Fatalf("ProcessDeposit failed: %v", err)
	}

	// Get the booking log ID
	var entity persistence.LedgerPostingEntity
	db.First(&entity)

	// Validate double entry
	balanced, err := service.ValidateDoubleEntry(ctx, entity.FinancialBookingLogID)
	if err != nil {
		t.Errorf("ValidateDoubleEntry failed: %v", err)
	}

	if !balanced {
		t.Error("Expected double entry to be balanced")
	}
}

func TestProcessDeposit_InvalidAmount(t *testing.T) {
	db := setupTestDB(t)
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:     "ACC-789",
		AmountCents:   0, // Invalid
		Currency:      "GBP",
		CorrelationID: "deposit-003",
		ValueDate:     time.Now(),
	}

	ctx := context.Background()
	err := service.ProcessDeposit(ctx, event)
	if err == nil {
		t.Error("Expected error for zero amount, got nil")
	}
}

func TestGetPostingsByBookingLog(t *testing.T) {
	db := setupTestDB(t)
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")
	ctx := context.Background()

	// Create some postings
	event := DepositEvent{
		AccountID:     "ACC-999",
		AmountCents:   25000,
		Currency:      "GBP",
		CorrelationID: "deposit-004",
		ValueDate:     time.Now(),
	}

	err := service.ProcessDeposit(ctx, event)
	if err != nil {
		t.Fatalf("ProcessDeposit failed: %v", err)
	}

	// Get booking log ID
	var entity persistence.LedgerPostingEntity
	db.First(&entity)

	// Retrieve postings
	postings, err := service.GetPostingsByBookingLog(ctx, entity.FinancialBookingLogID)
	if err != nil {
		t.Fatalf("GetPostingsByBookingLog failed: %v", err)
	}

	if len(postings) != 2 {
		t.Errorf("Expected 2 postings, got %d", len(postings))
	}

	// Verify one debit and one credit
	var debitCount, creditCount int
	for _, p := range postings {
		switch p.Direction {
		case domain.PostingDirectionDebit:
			debitCount++
		case domain.PostingDirectionCredit:
			creditCount++
		}
	}

	if debitCount != 1 || creditCount != 1 {
		t.Errorf("Expected 1 debit and 1 credit, got %d debits and %d credits", debitCount, creditCount)
	}
}

func TestValidateDoubleEntry_Unbalanced(t *testing.T) {
	db := setupTestDB(t)
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")
	ctx := context.Background()

	bookingLogID := uuid.New()

	// Create unbalanced entries manually
	debitMoney, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	debit, _ := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionDebit,
		debitMoney,
		"ACC-001",
		time.Now(),
		"test-001",
	)
	_ = debit.Post("test")
	_ = repo.SavePosting(ctx, debit)

	creditMoney, _ := domain.NewMoney(decimal.NewFromInt(50), domain.CurrencyGBP) // Different amount
	credit, _ := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionCredit,
		creditMoney,
		"ACC-002",
		time.Now(),
		"test-001",
	)
	_ = credit.Post("test")
	_ = repo.SavePosting(ctx, credit)

	// Validate - should be unbalanced
	balanced, err := service.ValidateDoubleEntry(ctx, bookingLogID)
	if err != nil {
		t.Errorf("ValidateDoubleEntry failed: %v", err)
	}

	if balanced {
		t.Error("Expected double entry to be unbalanced, but was balanced")
	}
}

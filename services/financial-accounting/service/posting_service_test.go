package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.LedgerPostingEntity{},
		&persistence.FinancialBookingLogEntity{},
		&persistence.AuditOutbox{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create tables in tenant schema (singular names to match production)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.financial_booking_log (
		id UUID PRIMARY KEY,
		financial_account_type VARCHAR(50) NOT NULL,
		product_service_reference VARCHAR(255) NOT NULL,
		business_unit_reference VARCHAR(255) NOT NULL,
		chart_of_accounts_rules TEXT NOT NULL,
		base_currency VARCHAR(3) NOT NULL,
		status VARCHAR(20) NOT NULL,
		idempotency_key VARCHAR(255) NOT NULL UNIQUE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		version BIGINT NOT NULL DEFAULT 1,
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.ledger_posting (
		id UUID PRIMARY KEY,
		financial_booking_log_id UUID NOT NULL,
		posting_direction VARCHAR(20) NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		account_id VARCHAR(255) NOT NULL,
		value_date TIMESTAMP WITH TIME ZONE NOT NULL,
		posting_result TEXT,
		status VARCHAR(20) NOT NULL,
		correlation_id VARCHAR(255),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create audit_outbox table for GORM hooks
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
		record_id UUID NOT NULL,
		old_values JSONB,
		new_values JSONB,
		status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip INET,
		user_agent TEXT
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestProcessDeposit(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:     "ACC-123",
		AmountCents:   10000, // £100.00
		Currency:      "GBP",
		CorrelationID: "deposit-001",
		ValueDate:     time.Now(),
	}
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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:     "ACC-789",
		AmountCents:   0, // Invalid
		Currency:      "GBP",
		CorrelationID: "deposit-003",
		ValueDate:     time.Now(),
	}
	err := service.ProcessDeposit(ctx, event)
	if err == nil {
		t.Error("Expected error for zero amount, got nil")
	}
}

func TestGetPostingsByBookingLog(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

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

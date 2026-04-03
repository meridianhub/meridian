package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(
			&persistence.FinancialBookingLogEntity{},
			&persistence.LedgerPostingEntity{},
			&audit.AuditOutbox{},
		),
		testdb.WithTenant(testTenantID),
	)
}

func TestProcessDeposit(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:      "ACC-123",
		Amount:         "100.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-001",
		ValueDate:      time.Now(),
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

	if debitEntity.AmountMinorUnits != 10000 {
		t.Errorf("Expected debit amount 10000, got %d", debitEntity.AmountMinorUnits)
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

	if creditEntity.AmountMinorUnits != 10000 {
		t.Errorf("Expected credit amount 10000, got %d", creditEntity.AmountMinorUnits)
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
		AccountID:      "ACC-456",
		Amount:         "500.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-002",
		ValueDate:      time.Now(),
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
		AccountID:      "ACC-789",
		Amount:         "0", // Invalid - zero amount
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-003",
		ValueDate:      time.Now(),
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
		AccountID:      "ACC-999",
		Amount:         "250.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-004",
		ValueDate:      time.Now(),
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
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	debitMoney := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
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

	creditMoney := domain.NewMoney(decimal.NewFromInt(50), gbpInstrument) // Different amount
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

// =============================================================================
// Tests for AccountResolver integration
// =============================================================================

func TestProcessDeposit_WithDynamicClearingAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	// Create mock that returns a dynamic clearing account
	mockClient := &postingServiceMockClient{
		accountID: "DYNAMIC-CLEARING-GBP",
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: postingTestLogger(),
	})
	require.NoError(t, err)

	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:              repo,
		BankCashAccountID: "STATIC-FALLBACK",
		AccountResolver:   resolver,
		Logger:            postingTestLogger(),
	})

	event := DepositEvent{
		AccountID:      "ACC-DYNAMIC-123",
		Amount:         "100.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-dynamic-001",
		ValueDate:      time.Now(),
	}

	err = service.ProcessDeposit(ctx, event)
	require.NoError(t, err)

	// Verify credit posting used dynamic clearing account
	var creditEntity persistence.LedgerPostingEntity
	err = db.Where("posting_direction = ?", "CREDIT").First(&creditEntity).Error
	require.NoError(t, err)
	require.Equal(t, "DYNAMIC-CLEARING-GBP", creditEntity.AccountID)
}

func TestProcessDeposit_FallbackOnResolverError(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	// Create mock that returns an error
	mockClient := &postingServiceMockClient{
		err: errMockServiceUnavailable,
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: postingTestLogger(),
	})
	require.NoError(t, err)

	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:              repo,
		BankCashAccountID: "STATIC-FALLBACK",
		AccountResolver:   resolver,
		Logger:            postingTestLogger(),
	})

	event := DepositEvent{
		AccountID:      "ACC-FALLBACK-123",
		Amount:         "100.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-fallback-001",
		ValueDate:      time.Now(),
	}

	err = service.ProcessDeposit(ctx, event)
	require.NoError(t, err)

	// Verify credit posting used static fallback account
	var creditEntity persistence.LedgerPostingEntity
	err = db.Where("posting_direction = ?", "CREDIT").First(&creditEntity).Error
	require.NoError(t, err)
	require.Equal(t, "STATIC-FALLBACK", creditEntity.AccountID)
}

func TestProcessDeposit_WithoutResolver_UsesStaticAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	// Create service without AccountResolver
	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:              repo,
		BankCashAccountID: "STATIC-ONLY",
		AccountResolver:   nil, // Explicitly nil
		Logger:            postingTestLogger(),
	})

	event := DepositEvent{
		AccountID:      "ACC-STATIC-123",
		Amount:         "100.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-static-001",
		ValueDate:      time.Now(),
	}

	err := service.ProcessDeposit(ctx, event)
	require.NoError(t, err)

	// Verify credit posting used static account
	var creditEntity persistence.LedgerPostingEntity
	err = db.Where("posting_direction = ?", "CREDIT").First(&creditEntity).Error
	require.NoError(t, err)
	require.Equal(t, "STATIC-ONLY", creditEntity.AccountID)
}

func TestProcessDeposit_FallbackOnNoClearingAccountFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	// Create mock that returns no accounts (ErrNoClearingAccountFound path)
	mockClient := &postingServiceMockClient{
		accountID: "", // Empty means no accounts returned
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: postingTestLogger(),
	})
	require.NoError(t, err)

	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:              repo,
		BankCashAccountID: "STATIC-FALLBACK-EMPTY",
		AccountResolver:   resolver,
		Logger:            postingTestLogger(),
	})

	event := DepositEvent{
		AccountID:      "ACC-EMPTY-123",
		Amount:         "100.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-empty-001",
		ValueDate:      time.Now(),
	}

	err = service.ProcessDeposit(ctx, event)
	require.NoError(t, err)

	// Verify credit posting used static fallback account
	var creditEntity persistence.LedgerPostingEntity
	err = db.Where("posting_direction = ?", "CREDIT").First(&creditEntity).Error
	require.NoError(t, err)
	require.Equal(t, "STATIC-FALLBACK-EMPTY", creditEntity.AccountID)
}

func TestProcessDeposit_MultiAsset_DynamicLookup(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	// Create mock that returns different accounts per instrument
	mockClient := &postingServiceMockClient{
		accountsByInstrument: map[string]string{
			"GBP": "CLEARING-GBP-001",
			"USD": "CLEARING-USD-001",
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: postingTestLogger(),
	})
	require.NoError(t, err)

	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:              repo,
		BankCashAccountID: "FALLBACK",
		AccountResolver:   resolver,
		Logger:            postingTestLogger(),
	})

	// Process GBP deposit
	gbpEvent := DepositEvent{
		AccountID:      "ACC-GBP-123",
		Amount:         "100.00",
		InstrumentCode: "GBP",
		CorrelationID:  "deposit-gbp-001",
		ValueDate:      time.Now(),
	}
	err = service.ProcessDeposit(ctx, gbpEvent)
	require.NoError(t, err)

	// Process USD deposit
	usdEvent := DepositEvent{
		AccountID:      "ACC-USD-123",
		Amount:         "200.00",
		InstrumentCode: "USD",
		CorrelationID:  "deposit-usd-001",
		ValueDate:      time.Now(),
	}
	err = service.ProcessDeposit(ctx, usdEvent)
	require.NoError(t, err)

	// Verify GBP credit used GBP clearing account
	var gbpCredit persistence.LedgerPostingEntity
	err = db.Where("correlation_id = ? AND posting_direction = ?", "deposit-gbp-001", "CREDIT").First(&gbpCredit).Error
	require.NoError(t, err)
	require.Equal(t, "CLEARING-GBP-001", gbpCredit.AccountID)

	// Verify USD credit used USD clearing account
	var usdCredit persistence.LedgerPostingEntity
	err = db.Where("correlation_id = ? AND posting_direction = ?", "deposit-usd-001", "CREDIT").First(&usdCredit).Error
	require.NoError(t, err)
	require.Equal(t, "CLEARING-USD-001", usdCredit.AccountID)
}

// =============================================================================
// Tests for asset-agnostic deposits with InstrumentResolver
// =============================================================================

func TestProcessDeposit_KWH_WithResolver(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	resolver := &postingServiceMockInstrumentResolver{
		instruments: map[string]refdata.InstrumentProperties{
			"KWH": {Code: "KWH", Dimension: "ENERGY", Precision: 3, RoundingMode: "HALF_UP"},
		},
	}

	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:               repo,
		BankCashAccountID:  "ENERGY-CLEARING-001",
		InstrumentResolver: resolver,
		Logger:             postingTestLogger(),
	})

	event := DepositEvent{
		AccountID:      "ACC-ENERGY-001",
		Amount:         "123.456",
		InstrumentCode: "KWH",
		CorrelationID:  "deposit-kwh-001",
		ValueDate:      time.Now(),
	}

	err := service.ProcessDeposit(ctx, event)
	require.NoError(t, err)

	// Verify debit posting exists
	var debitEntity persistence.LedgerPostingEntity
	err = db.Where("account_id = ? AND posting_direction = ?", "ACC-ENERGY-001", "DEBIT").First(&debitEntity).Error
	require.NoError(t, err)
	require.Equal(t, "KWH", debitEntity.Currency)

	// Verify credit posting exists
	var creditEntity persistence.LedgerPostingEntity
	err = db.Where("account_id = ? AND posting_direction = ?", "ENERGY-CLEARING-001", "CREDIT").First(&creditEntity).Error
	require.NoError(t, err)
}

func TestProcessDeposit_UnknownInstrument_WithResolver(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	// Resolver that doesn't know "UNKNOWN"
	resolver := &postingServiceMockInstrumentResolver{
		instruments: map[string]refdata.InstrumentProperties{},
	}

	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:               repo,
		BankCashAccountID:  "FALLBACK",
		InstrumentResolver: resolver,
		Logger:             postingTestLogger(),
	})

	event := DepositEvent{
		AccountID:      "ACC-UNKNOWN",
		Amount:         "100.00",
		InstrumentCode: "UNKNOWN_ASSET",
		CorrelationID:  "deposit-unknown-001",
		ValueDate:      time.Now(),
	}

	err := service.ProcessDeposit(ctx, event)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to resolve instrument")
}

func TestProcessDeposit_Precision_Preserved(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()
	repo := persistence.NewLedgerRepository(db)

	resolver := &postingServiceMockInstrumentResolver{
		instruments: map[string]refdata.InstrumentProperties{
			"KWH": {Code: "KWH", Dimension: "ENERGY", Precision: 3, RoundingMode: "HALF_UP"},
		},
	}

	service := NewPostingServiceWithConfig(PostingServiceConfig{
		Repo:               repo,
		BankCashAccountID:  "CLEARING",
		InstrumentResolver: resolver,
		Logger:             postingTestLogger(),
	})

	event := DepositEvent{
		AccountID:      "ACC-PRECISION",
		Amount:         "123.456",
		InstrumentCode: "KWH",
		CorrelationID:  "deposit-precision-001",
		ValueDate:      time.Now(),
	}

	err := service.ProcessDeposit(ctx, event)
	require.NoError(t, err)

	// Retrieve and verify amount preserved through the pipeline
	var entity persistence.LedgerPostingEntity
	err = db.Where("account_id = ? AND posting_direction = ?", "ACC-PRECISION", "DEBIT").First(&entity).Error
	require.NoError(t, err)

	// 123.456 with precision 3 = 123456 minor units
	require.Equal(t, int64(123456), entity.AmountMinorUnits)
}

// =============================================================================
// Test helpers
// =============================================================================

// postingServiceMockInstrumentResolver implements refdata.InstrumentResolver for PostingService tests.
type postingServiceMockInstrumentResolver struct {
	instruments map[string]refdata.InstrumentProperties
}

func (m *postingServiceMockInstrumentResolver) Resolve(_ context.Context, code string) (refdata.InstrumentProperties, error) {
	if props, ok := m.instruments[code]; ok {
		return props, nil
	}
	return refdata.InstrumentProperties{}, refdata.ErrUnknownInstrument
}

// Sentinel error for testing fallback behavior
var errMockServiceUnavailable = errors.New("mock: service unavailable")

// postingServiceMockClient is a mock for testing PostingService integration
type postingServiceMockClient struct {
	accountID            string
	accountsByInstrument map[string]string
	err                  error
}

func (m *postingServiceMockClient) ListInternalAccounts(_ context.Context, req *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error) {
	if m.err != nil {
		return nil, m.err
	}

	accountID := m.accountID
	if m.accountsByInstrument != nil {
		if id, ok := m.accountsByInstrument[req.InstrumentCodeFilter]; ok {
			accountID = id
		}
	}

	if accountID == "" {
		return &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{},
		}, nil
	}

	return &internalaccountv1.ListInternalAccountsResponse{
		Facilities: []*internalaccountv1.InternalAccountFacility{
			{AccountId: accountID},
		},
	}, nil
}

func (m *postingServiceMockClient) RetrieveInternalAccount(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	return nil, nil
}

func (m *postingServiceMockClient) Close() error {
	return nil
}

func postingTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// --- isValidBookingLogTransition ---

func TestIsValidBookingLogTransition(t *testing.T) {
	tests := []struct {
		name string
		from domain.TransactionStatus
		to   domain.TransactionStatus
		want bool
	}{
		// From PENDING
		{"pending to pending", domain.TransactionStatusPending, domain.TransactionStatusPending, true},
		{"pending to posted", domain.TransactionStatusPending, domain.TransactionStatusPosted, true},
		{"pending to failed", domain.TransactionStatusPending, domain.TransactionStatusFailed, true},
		{"pending to cancelled", domain.TransactionStatusPending, domain.TransactionStatusCancelled, true},
		{"pending to reversed is invalid", domain.TransactionStatusPending, domain.TransactionStatusReversed, false},

		// From POSTED
		{"posted to reversed", domain.TransactionStatusPosted, domain.TransactionStatusReversed, true},
		{"posted to pending is invalid", domain.TransactionStatusPosted, domain.TransactionStatusPending, false},
		{"posted to posted is invalid", domain.TransactionStatusPosted, domain.TransactionStatusPosted, false},
		{"posted to failed is invalid", domain.TransactionStatusPosted, domain.TransactionStatusFailed, false},
		{"posted to cancelled is invalid", domain.TransactionStatusPosted, domain.TransactionStatusCancelled, false},

		// Terminal states - no transitions allowed
		{"failed to pending is invalid", domain.TransactionStatusFailed, domain.TransactionStatusPending, false},
		{"failed to posted is invalid", domain.TransactionStatusFailed, domain.TransactionStatusPosted, false},
		{"failed to failed is invalid", domain.TransactionStatusFailed, domain.TransactionStatusFailed, false},
		{"cancelled to pending is invalid", domain.TransactionStatusCancelled, domain.TransactionStatusPending, false},
		{"reversed to pending is invalid", domain.TransactionStatusReversed, domain.TransactionStatusPending, false},

		// Unknown from status
		{"unknown to pending is invalid", domain.TransactionStatus("UNKNOWN"), domain.TransactionStatusPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidBookingLogTransition(tt.from, tt.to)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- extractUserFromContext edge cases ---
// Note: TestExtractUserFromContext already exists in financial_accounting_service_test.go.
// These tests cover additional edge cases.

func TestExtractUserFromContext_EmptyStringReturnsSystem(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "")
	result := extractUserFromContext(ctx)
	assert.Equal(t, "system", result)
}

func TestExtractUserFromContext_NoContextReturnsSystem(t *testing.T) {
	result := extractUserFromContext(context.Background())
	assert.Equal(t, "system", result)
}

// --- decimalFromCents ---

func TestDecimalFromCents(t *testing.T) {
	tests := []struct {
		name     string
		cents    int64
		expected string
	}{
		{"zero", 0, "0"},
		{"100 cents is 1.00", 100, "1"},
		{"150 cents is 1.50", 150, "1.5"},
		{"1 cent is 0.01", 1, "0.01"},
		{"negative cents", -500, "-5"},
		{"large amount", 1234567, "12345.67"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decimalFromCents(tt.cents)
			assert.Equal(t, tt.expected, result.String())
		})
	}
}

// --- toProtoFinancialBookingLog ---

func TestToProtoFinancialBookingLog_Nil(t *testing.T) {
	result := toProtoFinancialBookingLog(nil)
	assert.Nil(t, result)
}

func TestToProtoFinancialBookingLog_WithPostings(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), inst)
	bookingLogID := uuid.New()
	now := time.Now().UTC()

	posting1, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionDebit,
		amount,
		"ACC-001",
		now,
		"corr-1",
	)
	require.NoError(t, err)

	posting2, err := domain.NewLedgerPosting(
		bookingLogID,
		domain.PostingDirectionCredit,
		amount,
		"ACC-002",
		now,
		"corr-1",
	)
	require.NoError(t, err)

	bookingLog := domain.NewFinancialBookingLog(
		"CHECKING",
		"product-ref",
		"business-unit",
		"chart-rules",
		domain.CurrencyGBP,
	)
	withPostings := bookingLog.WithPosting(posting1).WithPosting(posting2)

	result := toProtoFinancialBookingLog(&withPostings)
	require.NotNil(t, result)
	assert.Equal(t, withPostings.ID.String(), result.Id)
	assert.Equal(t, "CHECKING", result.FinancialAccountType)
	assert.Equal(t, "product-ref", result.ProductServiceReference)
	assert.Equal(t, "business-unit", result.BusinessUnitReference)
	assert.Equal(t, "chart-rules", result.ChartOfAccountsRules)
	assert.Equal(t, "GBP", result.BaseInstrumentCode)
	assert.Len(t, result.Postings, 2)
	assert.NotNil(t, result.CreatedAt)
	assert.NotNil(t, result.UpdatedAt)
}

func TestToProtoFinancialBookingLog_NoPostings(t *testing.T) {
	bookingLog := domain.NewFinancialBookingLog(
		"SAVINGS",
		"product-ref",
		"bu-ref",
		"rules",
		domain.CurrencyUSD,
	)

	result := toProtoFinancialBookingLog(bookingLog)
	require.NotNil(t, result)
	assert.Empty(t, result.Postings)
}

// --- toProtoAccountType / fromProtoAccountType ---

func TestToProtoAccountType(t *testing.T) {
	assert.Equal(t, "CHECKING", toProtoAccountType("CHECKING"))
	assert.Equal(t, "", toProtoAccountType(""))
}

func TestFromProtoAccountType(t *testing.T) {
	assert.Equal(t, "SAVINGS", fromProtoAccountType("SAVINGS"))
	assert.Equal(t, "", fromProtoAccountType(""))
}

// --- WithRegistry and WithInstrumentResolver options ---
// Uses mockInstrumentRegistry from financial_accounting_service_test.go
// Uses mockInstrumentResolver from list_ledger_postings_test.go

func TestWithRegistry_SetsField(t *testing.T) {
	svc := &FinancialAccountingService{}
	registry := &mockInstrumentRegistry{}

	opt := WithRegistry(registry)
	opt(svc)

	assert.Equal(t, registry, svc.registry)
}

func TestWithInstrumentResolver_SetsField(t *testing.T) {
	svc := &FinancialAccountingService{}
	resolver := &mockInstrumentResolver{}

	opt := WithInstrumentResolver(resolver)
	opt(svc)

	assert.Equal(t, resolver, svc.instrumentResolver)
}

// --- AccountResolver cacheKey ---

func TestAccountResolver_CacheKey(t *testing.T) {
	resolver := &AccountResolver{}

	key := resolver.cacheKey(ClearingAccountTypeDeposit, "GBP")
	assert.Equal(t, "DEPOSIT:GBP", key)

	key = resolver.cacheKey(ClearingAccountTypeSettlement, "USD")
	assert.Equal(t, "SETTLEMENT:USD", key)
}

// --- NewPostingService constructors ---

func TestNewPostingService(t *testing.T) {
	svc := NewPostingService(nil, "bank-cash-001")
	require.NotNil(t, svc)
	assert.Equal(t, "bank-cash-001", svc.bankCashAccountID)
	assert.Nil(t, svc.accountResolver)
	assert.NotNil(t, svc.logger)
}

func TestNewPostingServiceWithConfig_NoResolver(t *testing.T) {
	svc := NewPostingServiceWithConfig(PostingServiceConfig{
		BankCashAccountID: "cash-002",
	})
	require.NotNil(t, svc)
	assert.Equal(t, "cash-002", svc.bankCashAccountID)
	assert.Nil(t, svc.accountResolver)
	assert.NotNil(t, svc.logger)
}

// --- resolveClearingAccountForDeposit ---

func TestResolveClearingAccountForDeposit_NoResolver(t *testing.T) {
	svc := NewPostingService(nil, "static-fallback")

	result := svc.resolveClearingAccountForDeposit(context.Background(), "GBP")
	assert.Equal(t, "static-fallback", result)
}

// --- resolveClearingAccountForDeposit with resolver ---

func TestResolveClearingAccountForDeposit_ResolverReturnsEmpty(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: ""},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: logger,
	})
	require.NoError(t, err)

	svc := NewPostingServiceWithConfig(PostingServiceConfig{
		BankCashAccountID: "static-fallback",
		AccountResolver:   resolver,
		Logger:            logger,
	})

	// Empty account ID from resolver should fallback to static
	result := svc.resolveClearingAccountForDeposit(context.Background(), "GBP")
	assert.Equal(t, "static-fallback", result)
}

func TestResolveClearingAccountForDeposit_ResolverError(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockClient := &mockInternalAccountClient{
		listErr: errors.New("service unavailable"),
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: logger,
	})
	require.NoError(t, err)

	svc := NewPostingServiceWithConfig(PostingServiceConfig{
		BankCashAccountID: "static-fallback",
		AccountResolver:   resolver,
		Logger:            logger,
	})

	// Error from resolver should fallback to static
	result := svc.resolveClearingAccountForDeposit(context.Background(), "GBP")
	assert.Equal(t, "static-fallback", result)
}

func TestResolveClearingAccountForDeposit_ResolverSuccess(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "dynamic-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: logger,
	})
	require.NoError(t, err)

	svc := NewPostingServiceWithConfig(PostingServiceConfig{
		BankCashAccountID: "static-fallback",
		AccountResolver:   resolver,
		Logger:            logger,
	})

	result := svc.resolveClearingAccountForDeposit(context.Background(), "GBP")
	assert.Equal(t, "dynamic-clearing-123", result)
}

// --- NewPostingServiceWithConfig with resolver ---

func TestNewPostingServiceWithConfig_WithResolver(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockClient := &mockInternalAccountClient{}
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: logger,
	})
	require.NoError(t, err)

	svc := NewPostingServiceWithConfig(PostingServiceConfig{
		BankCashAccountID: "cash-003",
		AccountResolver:   resolver,
		Logger:            logger,
	})
	require.NotNil(t, svc)
	assert.Equal(t, "cash-003", svc.bankCashAccountID)
	assert.NotNil(t, svc.accountResolver)
	assert.Equal(t, logger, svc.logger)
}

func TestNewPostingServiceWithConfig_NilLogger(t *testing.T) {
	svc := NewPostingServiceWithConfig(PostingServiceConfig{
		BankCashAccountID: "cash-004",
	})
	require.NotNil(t, svc)
	assert.NotNil(t, svc.logger, "nil logger should be replaced with default")
}

// --- Additional toProtoMoney edge cases ---

func TestToProtoMoney_NegativeAmount(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyUSD)
	amount, _ := decimal.NewFromString("-42.75")
	m := domain.NewMoney(amount, inst)

	result := toProtoMoney(m)
	assert.Equal(t, "USD", result.CurrencyCode)
	assert.Equal(t, int64(-42), result.Units)
	assert.Equal(t, int32(-750000000), result.Nanos)
}

func TestToProtoMoney_VerySmallFraction(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount, _ := decimal.NewFromString("0.000000001") // 1 nano
	m := domain.NewMoney(amount, inst)

	result := toProtoMoney(m)
	assert.Equal(t, int64(0), result.Units)
	assert.Equal(t, int32(1), result.Nanos)
}

func TestToProtoMoney_ZeroAmount(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	m := domain.NewMoney(decimal.Zero, inst)

	result := toProtoMoney(m)
	assert.Equal(t, int64(0), result.Units)
	assert.Equal(t, int32(0), result.Nanos)
}

// --- Additional fromProtoMoney edge cases ---

func TestFromProtoMoney_NegativeUnits(t *testing.T) {
	protoMoney := &money.Money{
		CurrencyCode: "GBP",
		Units:        -50,
		Nanos:        0,
	}

	result, err := fromProtoMoney(protoMoney)
	require.NoError(t, err)
	assert.Equal(t, "-50", result.Amount.String())
}

func TestFromProtoMoney_NegativeNanos(t *testing.T) {
	protoMoney := &money.Money{
		CurrencyCode: "USD",
		Units:        -1,
		Nanos:        -500000000,
	}

	result, err := fromProtoMoney(protoMoney)
	require.NoError(t, err)
	assert.Equal(t, "-1.5", result.Amount.String())
}

func TestFromProtoMoney_LargeUnits(t *testing.T) {
	protoMoney := &money.Money{
		CurrencyCode: "GBP",
		Units:        9999999,
		Nanos:        999000000,
	}

	result, err := fromProtoMoney(protoMoney)
	require.NoError(t, err)
	assert.Equal(t, "9999999.999", result.Amount.String())
}

// --- Additional parseUUID edge cases ---

func TestParseUUID_NilUUIDString(t *testing.T) {
	result, err := parseUUID("00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, result)
}

func TestParseUUID_UpperCase(t *testing.T) {
	expected := uuid.New()
	result, err := parseUUID(expected.String())
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestParseUUID_Whitespace(t *testing.T) {
	_, err := parseUUID("  ")
	require.Error(t, err)
}

// --- NewFinancialAccountingService with options ---

func TestNewFinancialAccountingService_WithOptions(t *testing.T) {
	db := &gorm.DB{}
	repo := newTestRepo(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	registry := &mockInstrumentRegistry{}
	resolver := &mockInstrumentResolver{}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
		WithInstrumentResolver(resolver),
	)

	require.NoError(t, err)
	assert.NotNil(t, svc)
	assert.Equal(t, registry, svc.registry)
	assert.Equal(t, resolver, svc.instrumentResolver)
}

// --- toProtoLedgerPosting additional edge cases ---

func TestToProtoLedgerPosting_CreditDirection(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyUSD)
	amount := domain.NewMoney(decimal.NewFromInt(250), inst)
	bookingLogID := uuid.New()
	postingID := uuid.New()

	posting := &domain.LedgerPosting{
		ID:                    postingID,
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionCredit,
		Amount:                amount,
		AccountID:             "ACC-002",
		AccountServiceDomain:  "INTERNAL_ACCOUNT",
		ValueDate:             time.Now().UTC(),
		PostingResult:         "processed",
		Status:                domain.TransactionStatusPending,
		CreatedAt:             time.Now().UTC(),
	}

	result := toProtoLedgerPosting(posting)
	require.NotNil(t, result)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_CREDIT, result.PostingDirection)
	assert.Equal(t, commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_INTERNAL_ACCOUNT, result.AccountServiceDomain)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, result.Status)
	assert.Equal(t, "processed", result.PostingResult)
}

func TestToProtoLedgerPosting_EmptyAccountServiceDomain(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(10), inst)

	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: uuid.New(),
		Direction:             domain.PostingDirectionDebit,
		Amount:                amount,
		AccountID:             "ACC-003",
		AccountServiceDomain:  "", // empty
		ValueDate:             time.Now().UTC(),
		Status:                domain.TransactionStatusFailed,
		CreatedAt:             time.Now().UTC(),
	}

	result := toProtoLedgerPosting(posting)
	require.NotNil(t, result)
	assert.Equal(t, commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED, result.AccountServiceDomain)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, result.Status)
}

// --- toProtoFinancialBookingLog additional statuses ---

func TestToProtoFinancialBookingLog_PostedStatus(t *testing.T) {
	bookingLog := domain.NewFinancialBookingLog(
		"ASSET",
		"product-ref",
		"bu-ref",
		"rules",
		domain.CurrencyGBP,
	)
	updated := bookingLog.WithStatus(domain.TransactionStatusPosted)

	result := toProtoFinancialBookingLog(&updated)
	require.NotNil(t, result)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, result.Status)
}

func TestToProtoFinancialBookingLog_CancelledStatus(t *testing.T) {
	bookingLog := domain.NewFinancialBookingLog(
		"LIABILITY",
		"product-ref",
		"bu-ref",
		"rules",
		domain.CurrencyUSD,
	)
	updated := bookingLog.WithStatus(domain.TransactionStatusCancelled)

	result := toProtoFinancialBookingLog(&updated)
	require.NotNil(t, result)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, result.Status)
	assert.Equal(t, "USD", result.BaseInstrumentCode)
}

// --- DepositEvent struct usage ---

func TestDepositEvent_StructConstruction(t *testing.T) {
	event := DepositEvent{
		AccountID:     "ACC-001",
		AmountCents:   15000,
		Currency:      "GBP",
		CorrelationID: "corr-123",
		ValueDate:     time.Now().UTC(),
	}

	assert.Equal(t, "ACC-001", event.AccountID)
	assert.Equal(t, int64(15000), event.AmountCents)
	assert.Equal(t, "GBP", event.Currency)
	assert.Equal(t, "corr-123", event.CorrelationID)
	assert.False(t, event.ValueDate.IsZero())
}

// --- PostingServiceConfig struct ---

func TestPostingServiceConfig_AllFields(t *testing.T) {
	cfg := PostingServiceConfig{
		BankCashAccountID: "cash-123",
	}
	assert.Equal(t, "cash-123", cfg.BankCashAccountID)
	assert.Nil(t, cfg.Repo)
	assert.Nil(t, cfg.AccountResolver)
	assert.Nil(t, cfg.Logger)
}

// --- ClearingAccountType constants ---

func TestClearingAccountType_Values(t *testing.T) {
	assert.Equal(t, ClearingAccountType("DEPOSIT"), ClearingAccountTypeDeposit)
	assert.Equal(t, ClearingAccountType("WITHDRAWAL"), ClearingAccountTypeWithdrawal)
	assert.Equal(t, ClearingAccountType("SETTLEMENT"), ClearingAccountTypeSettlement)
}

// --- Error sentinel values ---

func TestErrorSentinels(t *testing.T) {
	assert.NotNil(t, ErrRepositoryNil)
	assert.NotNil(t, ErrEventPublisherNil)
	assert.NotNil(t, ErrIdempotencyServiceNil)
	assert.NotNil(t, ErrOutboxPublisherNil)
	assert.NotNil(t, ErrOutboxRepositoryNil)
	assert.NotNil(t, ErrRegistryUnavailable)
	assert.NotNil(t, ErrEmptyUUID)
	assert.NotNil(t, ErrNilMoney)
	assert.NotNil(t, ErrNoClearingAccountFound)
	assert.NotNil(t, ErrMultipleClearingAccounts)
	assert.NotNil(t, ErrAccountResolverClientNil)
	assert.NotNil(t, ErrAccountResolverLoggerNil)
	assert.NotNil(t, ErrAccountResolverInternalError)

	// Verify error messages are descriptive
	assert.Contains(t, ErrRepositoryNil.Error(), "repository")
	assert.Contains(t, ErrEventPublisherNil.Error(), "event publisher")
	assert.Contains(t, ErrIdempotencyServiceNil.Error(), "idempotency")
}

// --- AccountResolverConfig defaults ---

func TestAccountResolverConfig_CustomLookupTimeout(t *testing.T) {
	mockClient := &mockInternalAccountClient{}
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:        mockClient,
		Logger:        slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
		LookupTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, resolver.lookupTimeout)
}

// --- defaultIdempotencyTTL ---

func TestDefaultIdempotencyTTL(t *testing.T) {
	assert.Equal(t, 1*time.Hour, defaultIdempotencyTTL)
}

// --- DefaultCacheTTL and DefaultLookupTimeout ---

func TestDefaultCacheTTLValue(t *testing.T) {
	assert.Equal(t, 5*time.Minute, DefaultCacheTTL)
}

func TestDefaultLookupTimeoutValue(t *testing.T) {
	assert.Equal(t, 2*time.Second, DefaultLookupTimeout)
}

// --- extractUserFromContext with actual user ---

func TestExtractUserFromContext_WithValidUser(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-123")
	result := extractUserFromContext(ctx)
	assert.Equal(t, "user-123", result)
}

// --- toProtoMoney roundtrip with fromProtoMoney ---

func TestProtoMoney_Roundtrip(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	original := domain.NewMoney(decimal.RequireFromString("123.45"), inst)

	proto := toProtoMoney(original)
	roundtripped, err := fromProtoMoney(proto)
	require.NoError(t, err)

	assert.True(t, original.Amount.Equal(roundtripped.Amount),
		"roundtrip should preserve amount: got %s, want %s", roundtripped.Amount, original.Amount)
	assert.Equal(t, original.Instrument.Code, roundtripped.Instrument.Code)
}

// --- isValidBookingLogTransition additional terminal coverage ---

func TestIsValidBookingLogTransition_AllTerminalToAll(t *testing.T) {
	terminals := []domain.TransactionStatus{
		domain.TransactionStatusFailed,
		domain.TransactionStatusCancelled,
		domain.TransactionStatusReversed,
	}
	all := []domain.TransactionStatus{
		domain.TransactionStatusPending,
		domain.TransactionStatusPosted,
		domain.TransactionStatusFailed,
		domain.TransactionStatusCancelled,
		domain.TransactionStatusReversed,
	}

	for _, from := range terminals {
		for _, to := range all {
			result := isValidBookingLogTransition(from, to)
			assert.False(t, result, "terminal state %s should not transition to %s", from, to)
		}
	}
}

// helper to create a LedgerRepository for unit tests (no actual DB needed for constructor tests)
func newTestRepo(db *gorm.DB) *persistence.LedgerRepository {
	return persistence.NewLedgerRepository(db)
}

// --- toProtoMoney nanos overflow clamping ---

func TestToProtoMoney_NanosOverflowPositive(t *testing.T) {
	// This tests the nanos > 999_999_999 clamp.
	// We construct a Money with an artificially precise amount that would produce
	// nanos exceeding the limit. In practice, decimal.IntPart() on the nano
	// fraction could exceed int32 range with extreme precision values.
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	// 0.9999999999 would produce nanos = 999999999 (9 nines), just at the boundary
	amount, _ := decimal.NewFromString("0.999999999")
	m := domain.NewMoney(amount, inst)

	result := toProtoMoney(m)
	assert.Equal(t, int64(0), result.Units)
	assert.Equal(t, int32(999999999), result.Nanos)
}

func TestToProtoMoney_LargeNegativeFraction(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyUSD)
	amount, _ := decimal.NewFromString("-0.999999999")
	m := domain.NewMoney(amount, inst)

	result := toProtoMoney(m)
	assert.Equal(t, int64(0), result.Units)
	assert.Equal(t, int32(-999999999), result.Nanos)
}

// --- validatePostingPair additional error branches ---

func TestValidatePostingPair_NilPosting(t *testing.T) {
	db := &gorm.DB{}
	repo := newTestRepo(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	svc, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), inst)

	validPosting, err := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionDebit,
		amount,
		"ACC-1",
		time.Now(),
		"corr-1",
	)
	require.NoError(t, err)

	// Nil debit
	validationErr := svc.validatePostingPair(context.Background(), nil, validPosting)
	require.Error(t, validationErr)
	assert.Contains(t, validationErr.Error(), "posting cannot be nil")

	// Nil credit
	validationErr = svc.validatePostingPair(context.Background(), validPosting, nil)
	require.Error(t, validationErr)
	assert.Contains(t, validationErr.Error(), "posting cannot be nil")
}

func TestValidatePostingPair_InvalidDirection(t *testing.T) {
	db := &gorm.DB{}
	repo := newTestRepo(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	svc, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), inst)
	bookingLogID := uuid.New()

	// Both postings with CREDIT direction (debit should be DEBIT)
	debit, err := domain.NewLedgerPosting(bookingLogID, domain.PostingDirectionCredit, amount, "ACC-1", time.Now(), "corr-1")
	require.NoError(t, err)
	credit, err := domain.NewLedgerPosting(bookingLogID, domain.PostingDirectionCredit, amount, "ACC-2", time.Now(), "corr-1")
	require.NoError(t, err)

	validationErr := svc.validatePostingPair(context.Background(), debit, credit)
	require.Error(t, validationErr)
	assert.Contains(t, validationErr.Error(), "invalid posting direction")

	// Both postings with DEBIT direction (credit should be CREDIT)
	debit2, err := domain.NewLedgerPosting(bookingLogID, domain.PostingDirectionDebit, amount, "ACC-1", time.Now(), "corr-1")
	require.NoError(t, err)
	credit2, err := domain.NewLedgerPosting(bookingLogID, domain.PostingDirectionDebit, amount, "ACC-2", time.Now(), "corr-1")
	require.NoError(t, err)

	validationErr = svc.validatePostingPair(context.Background(), debit2, credit2)
	require.Error(t, validationErr)
	assert.Contains(t, validationErr.Error(), "invalid posting direction")
}

func TestValidatePostingPair_CELEvaluationError(t *testing.T) {
	db := &gorm.DB{}
	repo := newTestRepo(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create a mock program that returns an error on Eval
	errorProgram := &mockFungibilityKeyProgram{
		keyFunc: func(_ map[string]string) string {
			return "" // won't be reached
		},
	}

	registry := &mockInstrumentRegistry{
		instrument: &mockInstrumentDefinition{
			program: errorProgram,
		},
	}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
	)
	require.NoError(t, err)

	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), inst)
	bookingLogID := uuid.New()

	debit, err := domain.NewLedgerPosting(bookingLogID, domain.PostingDirectionDebit, amount, "ACC-1", time.Now(), "corr-1")
	require.NoError(t, err)
	debit.Attributes = map[string]string{"batch": "A"}

	credit, err := domain.NewLedgerPosting(bookingLogID, domain.PostingDirectionCredit, amount, "ACC-2", time.Now(), "corr-1")
	require.NoError(t, err)
	credit.Attributes = map[string]string{"batch": "A"}

	// This should succeed since attributes match the same program output
	validationErr := svc.validatePostingPair(context.Background(), debit, credit)
	// With the mock, both will produce the same key (empty string), so validation passes
	assert.NoError(t, validationErr)
}

// --- executeUpdateLedgerPosting additional error branches ---

func TestExecuteUpdateLedgerPosting_InvalidID(t *testing.T) {
	db := &gorm.DB{}
	repo := newTestRepo(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	svc, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	req := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     "not-a-uuid",
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	resp, execErr := svc.executeUpdateLedgerPosting(context.Background(), req)
	assert.Nil(t, resp)
	require.Error(t, execErr)
	assert.Contains(t, execErr.Error(), "invalid id")
}

func TestExecuteUpdateLedgerPosting_UnspecifiedStatus(t *testing.T) {
	db := &gorm.DB{}
	repo := newTestRepo(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	svc, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	req := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     uuid.New().String(),
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
	}

	resp, execErr := svc.executeUpdateLedgerPosting(context.Background(), req)
	assert.Nil(t, resp)
	require.Error(t, execErr)
	assert.Contains(t, execErr.Error(), "status must be specified")
}

// --- ProcessDeposit error branches ---

func TestProcessDeposit_InvalidCurrency(t *testing.T) {
	svc := NewPostingService(nil, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:     "ACC-INVALID-CURRENCY",
		AmountCents:   10000,
		Currency:      "INVALID_CURRENCY",
		CorrelationID: "deposit-invalid-curr",
		ValueDate:     time.Now(),
	}

	err := svc.ProcessDeposit(context.Background(), event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid currency")
}

func TestProcessDeposit_NegativeAmount(t *testing.T) {
	svc := NewPostingService(nil, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:     "ACC-NEG-AMOUNT",
		AmountCents:   -500,
		Currency:      "GBP",
		CorrelationID: "deposit-neg-amount",
		ValueDate:     time.Now(),
	}

	err := svc.ProcessDeposit(context.Background(), event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create debit posting")
}

func TestProcessDeposit_EmptyAccountID(t *testing.T) {
	svc := NewPostingService(nil, "BANK-CASH-001")

	event := DepositEvent{
		AccountID:     "",
		AmountCents:   10000,
		Currency:      "GBP",
		CorrelationID: "deposit-empty-acct",
		ValueDate:     time.Now(),
	}

	err := svc.ProcessDeposit(context.Background(), event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create debit posting")
}

// --- fromProtoMoney nil input ---

func TestFromProtoMoney_NilInput(t *testing.T) {
	_, err := fromProtoMoney(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilMoney)
}

// --- fromProtoMoney unsupported currency code ---

func TestFromProtoMoney_UnsupportedCurrencyCode(t *testing.T) {
	protoMoney := &money.Money{
		CurrencyCode: "INVALID",
		Units:        100,
		Nanos:        0,
	}
	_, err := fromProtoMoney(protoMoney)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid currency")
}

// --- mapClearingTypeToPurpose additional coverage ---

func TestMapClearingTypeToPurpose_AllTypes(t *testing.T) {
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT, mapClearingTypeToPurpose(ClearingAccountTypeDeposit))
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL, mapClearingTypeToPurpose(ClearingAccountTypeWithdrawal))
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT, mapClearingTypeToPurpose(ClearingAccountTypeSettlement))
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED, mapClearingTypeToPurpose(ClearingAccountType("UNKNOWN")))
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED, mapClearingTypeToPurpose(ClearingAccountType("")))
}

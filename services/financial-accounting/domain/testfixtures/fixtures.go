// Package testfixtures provides reusable test data for domain tests.
//
// These fixtures follow established patterns from the codebase and provide
// consistent test data for financial-accounting domain models.
package testfixtures

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/shopspring/decimal"
)

// Standard test values for consistent test data
const (
	TestAccountType          = "ASSET"
	TestProductServiceRef    = "PROD-001"
	TestBusinessUnitRef      = "BU-TREASURY"
	TestChartOfAccountsRules = "UK-GAAP-2024"
	TestAccountID            = "ACC-001"
	TestCorrelationID        = "corr-001"
)

// BookingLogBuilder provides a fluent interface for creating test booking logs.
type BookingLogBuilder struct {
	accountType          string
	productServiceRef    string
	businessUnitRef      string
	chartOfAccountsRules string
	baseCurrency         domain.Currency
}

// NewBookingLogBuilder creates a builder with sensible defaults.
func NewBookingLogBuilder() *BookingLogBuilder {
	return &BookingLogBuilder{
		accountType:          TestAccountType,
		productServiceRef:    TestProductServiceRef,
		businessUnitRef:      TestBusinessUnitRef,
		chartOfAccountsRules: TestChartOfAccountsRules,
		baseCurrency:         domain.CurrencyGBP,
	}
}

// WithAccountType sets the account type.
func (b *BookingLogBuilder) WithAccountType(accountType string) *BookingLogBuilder {
	b.accountType = accountType
	return b
}

// WithProductServiceRef sets the product service reference.
func (b *BookingLogBuilder) WithProductServiceRef(ref string) *BookingLogBuilder {
	b.productServiceRef = ref
	return b
}

// WithBusinessUnitRef sets the business unit reference.
func (b *BookingLogBuilder) WithBusinessUnitRef(ref string) *BookingLogBuilder {
	b.businessUnitRef = ref
	return b
}

// WithChartOfAccountsRules sets the chart of accounts rules.
func (b *BookingLogBuilder) WithChartOfAccountsRules(rules string) *BookingLogBuilder {
	b.chartOfAccountsRules = rules
	return b
}

// WithCurrency sets the base currency.
func (b *BookingLogBuilder) WithCurrency(currency domain.Currency) *BookingLogBuilder {
	b.baseCurrency = currency
	return b
}

// Build creates the FinancialBookingLog.
func (b *BookingLogBuilder) Build() *domain.FinancialBookingLog {
	return domain.NewFinancialBookingLog(
		b.accountType,
		b.productServiceRef,
		b.businessUnitRef,
		b.chartOfAccountsRules,
		b.baseCurrency,
	)
}

// PostingBuilder provides a fluent interface for creating test ledger postings.
type PostingBuilder struct {
	bookingLogID  uuid.UUID
	direction     domain.PostingDirection
	amount        domain.Money
	accountID     string
	valueDate     time.Time
	correlationID string
}

// NewPostingBuilder creates a builder with sensible defaults.
func NewPostingBuilder() *PostingBuilder {
	return &PostingBuilder{
		bookingLogID:  uuid.New(),
		direction:     domain.PostingDirectionDebit,
		amount:        mustMoney(100, domain.CurrencyGBP),
		accountID:     TestAccountID,
		valueDate:     time.Now().UTC(),
		correlationID: TestCorrelationID,
	}
}

// mustMoney creates a Money value from amount and currency, panicking on error.
// This is a test helper - use only in test fixtures.
func mustMoney(amount int64, currency domain.Currency) domain.Money {
	inst := domain.MustCurrencyToInstrument(currency)
	return domain.NewMoney(decimal.NewFromInt(amount), inst)
}

// WithBookingLogID sets the booking log ID.
func (b *PostingBuilder) WithBookingLogID(id uuid.UUID) *PostingBuilder {
	b.bookingLogID = id
	return b
}

// WithDirection sets the posting direction.
func (b *PostingBuilder) WithDirection(direction domain.PostingDirection) *PostingBuilder {
	b.direction = direction
	return b
}

// WithAmount sets the posting amount.
func (b *PostingBuilder) WithAmount(amount domain.Money) *PostingBuilder {
	b.amount = amount
	return b
}

// WithAmountCents sets the posting amount from cents (assumes GBP).
func (b *PostingBuilder) WithAmountCents(cents int64) *PostingBuilder {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	b.amount = domain.NewMoney(decimal.NewFromInt(cents).Div(decimal.NewFromInt(100)), inst)
	return b
}

// WithAccountID sets the account ID.
func (b *PostingBuilder) WithAccountID(accountID string) *PostingBuilder {
	b.accountID = accountID
	return b
}

// WithValueDate sets the value date.
func (b *PostingBuilder) WithValueDate(date time.Time) *PostingBuilder {
	b.valueDate = date
	return b
}

// WithCorrelationID sets the correlation ID.
func (b *PostingBuilder) WithCorrelationID(correlationID string) *PostingBuilder {
	b.correlationID = correlationID
	return b
}

// Build creates the LedgerPosting.
// Returns the posting and any error (for validation failures).
func (b *PostingBuilder) Build() (*domain.LedgerPosting, error) {
	return domain.NewLedgerPosting(
		b.bookingLogID,
		b.direction,
		b.amount,
		b.accountID,
		b.valueDate,
		b.correlationID,
	)
}

// MustBuild creates the LedgerPosting or panics on error.
// Use only in tests where errors indicate test setup bugs.
func (b *PostingBuilder) MustBuild() *domain.LedgerPosting {
	posting, err := b.Build()
	if err != nil {
		panic("testfixtures: failed to build posting: " + err.Error())
	}
	return posting
}

// MoneyFixture creates Money values for tests.
type MoneyFixture struct{}

// NewMoneyFixture returns a MoneyFixture helper.
func NewMoneyFixture() *MoneyFixture {
	return &MoneyFixture{}
}

// GBP creates Money in GBP.
func (m *MoneyFixture) GBP(amount string) domain.Money {
	dec, err := decimal.NewFromString(amount)
	if err != nil {
		panic("testfixtures: invalid GBP amount " + amount + ": " + err.Error())
	}
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	return domain.NewMoney(dec, inst)
}

// USD creates Money in USD.
func (m *MoneyFixture) USD(amount string) domain.Money {
	dec, err := decimal.NewFromString(amount)
	if err != nil {
		panic("testfixtures: invalid USD amount " + amount + ": " + err.Error())
	}
	inst := domain.MustCurrencyToInstrument(domain.CurrencyUSD)
	return domain.NewMoney(dec, inst)
}

// EUR creates Money in EUR.
func (m *MoneyFixture) EUR(amount string) domain.Money {
	dec, err := decimal.NewFromString(amount)
	if err != nil {
		panic("testfixtures: invalid EUR amount " + amount + ": " + err.Error())
	}
	inst := domain.MustCurrencyToInstrument(domain.CurrencyEUR)
	return domain.NewMoney(dec, inst)
}

// GBPCents creates Money in GBP from cents.
func (m *MoneyFixture) GBPCents(cents int64) domain.Money {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	return domain.NewMoney(decimal.NewFromInt(cents).Div(decimal.NewFromInt(100)), inst)
}

// USDCents creates Money in USD from cents.
func (m *MoneyFixture) USDCents(cents int64) domain.Money {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyUSD)
	return domain.NewMoney(decimal.NewFromInt(cents).Div(decimal.NewFromInt(100)), inst)
}

// BalancedPostingPair creates a debit and credit posting that balance.
func BalancedPostingPair(bookingLogID uuid.UUID, amount domain.Money) (*domain.LedgerPosting, *domain.LedgerPosting, error) {
	debit, err := NewPostingBuilder().
		WithBookingLogID(bookingLogID).
		WithDirection(domain.PostingDirectionDebit).
		WithAmount(amount).
		WithAccountID("ACC-DEBIT").
		Build()
	if err != nil {
		return nil, nil, err
	}

	credit, err := NewPostingBuilder().
		WithBookingLogID(bookingLogID).
		WithDirection(domain.PostingDirectionCredit).
		WithAmount(amount).
		WithAccountID("ACC-CREDIT").
		Build()
	if err != nil {
		return nil, nil, err
	}

	return debit, credit, nil
}

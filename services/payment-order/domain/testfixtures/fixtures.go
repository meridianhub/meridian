// Package testfixtures provides factory functions and fixtures for creating
// test data consistently across the payment-order test suite.
//
// Usage:
//
//	// Create a default payment order for testing
//	po := testfixtures.NewPaymentOrder(t)
//
//	// Create with custom options
//	po := testfixtures.NewPaymentOrder(t,
//	    testfixtures.WithDebtorAccountID("ACC-123"),
//	    testfixtures.WithAmountCents(50000),
//	)
//
//	// Create a payment order in a specific state
//	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved)
package testfixtures

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
)

// PaymentOrderOption is a function that configures a PaymentOrder for testing.
type PaymentOrderOption func(*paymentOrderConfig)

type paymentOrderConfig struct {
	id                uuid.UUID
	debtorAccountID   string
	creditorReference string
	amountCents       int64
	currency          string
	idempotencyKey    string
	correlationID     string
	lienID            string
	gatewayRefID      string
	ledgerBookingID   string
	failureReason     string
	errorCode         string
}

// WithID sets a specific payment order ID.
func WithID(id uuid.UUID) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.id = id
	}
}

// WithDebtorAccountID sets the debtor account ID.
func WithDebtorAccountID(accountID string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.debtorAccountID = accountID
	}
}

// WithCreditorReference sets the creditor reference.
func WithCreditorReference(ref string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.creditorReference = ref
	}
}

// WithAmountCents sets the payment amount in cents.
func WithAmountCents(cents int64) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.amountCents = cents
	}
}

// WithCurrency sets the currency code.
func WithCurrency(currency string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.currency = currency
	}
}

// WithIdempotencyKey sets the idempotency key.
func WithIdempotencyKey(key string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.idempotencyKey = key
	}
}

// WithCorrelationID sets the correlation ID.
func WithCorrelationID(id string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.correlationID = id
	}
}

// WithLienID sets the lien ID.
func WithLienID(lienID string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.lienID = lienID
	}
}

// WithGatewayReferenceID sets the gateway reference ID.
func WithGatewayReferenceID(refID string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.gatewayRefID = refID
	}
}

// WithLedgerBookingID sets the ledger booking ID.
func WithLedgerBookingID(bookingID string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.ledgerBookingID = bookingID
	}
}

// WithFailureReason sets the failure reason.
func WithFailureReason(reason string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.failureReason = reason
	}
}

// WithErrorCode sets the error code.
func WithErrorCode(code string) PaymentOrderOption {
	return func(cfg *paymentOrderConfig) {
		cfg.errorCode = code
	}
}

// NewPaymentOrder creates a PaymentOrder in INITIATED status with sensible defaults for testing.
// Use options to customize specific fields.
func NewPaymentOrder(t *testing.T, opts ...PaymentOrderOption) *domain.PaymentOrder {
	t.Helper()

	// Defaults
	cfg := &paymentOrderConfig{
		debtorAccountID:   "TEST-DEBTOR-001",
		creditorReference: "GB82WEST12345698765432",
		amountCents:       10000, // £100.00
		currency:          "GBP",
		idempotencyKey:    "idem-" + uuid.New().String(),
		correlationID:     "corr-" + uuid.New().String(),
	}

	// Apply options
	for _, opt := range opts {
		opt(cfg)
	}

	// Create money
	amount, err := domain.NewMoney(cfg.currency, cfg.amountCents)
	if err != nil {
		t.Fatalf("Failed to create money: %v", err)
	}

	// Create payment order using domain constructor
	po, err := domain.NewPaymentOrder(
		cfg.debtorAccountID,
		cfg.creditorReference,
		amount,
		cfg.idempotencyKey,
		cfg.correlationID,
	)
	if err != nil {
		t.Fatalf("Failed to create payment order: %v", err)
	}

	// Override ID if specified
	if cfg.id != uuid.Nil {
		po.ID = cfg.id
	}

	return po
}

// NewPaymentOrderInStatus creates a PaymentOrder in the specified status with all
// required fields populated for that state. Use this to create payment orders
// in non-initial states for testing specific scenarios.
func NewPaymentOrderInStatus(t *testing.T, status domain.PaymentOrderStatus, opts ...PaymentOrderOption) *domain.PaymentOrder {
	t.Helper()

	cfg := &paymentOrderConfig{
		debtorAccountID:   "TEST-DEBTOR-001",
		creditorReference: "GB82WEST12345698765432",
		amountCents:       10000,
		currency:          "GBP",
		idempotencyKey:    "idem-" + uuid.New().String(),
		correlationID:     "corr-" + uuid.New().String(),
		lienID:            "lien-" + uuid.New().String(),
		gatewayRefID:      "gw-ref-" + uuid.New().String(),
		ledgerBookingID:   "ledger-" + uuid.New().String(),
		failureReason:     "Test failure",
		errorCode:         "TEST_ERROR",
	}

	for _, opt := range opts {
		opt(cfg)
	}

	amount, err := domain.NewMoney(cfg.currency, cfg.amountCents)
	if err != nil {
		t.Fatalf("Failed to create money: %v", err)
	}

	now := time.Now()
	po := &domain.PaymentOrder{
		ID:                uuid.New(),
		DebtorAccountID:   cfg.debtorAccountID,
		CreditorReference: cfg.creditorReference,
		Amount:            amount,
		Status:            status,
		IdempotencyKey:    cfg.idempotencyKey,
		CorrelationID:     cfg.correlationID,
		Version:           1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if cfg.id != uuid.Nil {
		po.ID = cfg.id
	}

	applyStatusFields(po, status, cfg, now)

	return po
}

// applyStatusFields sets status-specific fields on the payment order.
func applyStatusFields(po *domain.PaymentOrder, status domain.PaymentOrderStatus, cfg *paymentOrderConfig, now time.Time) {
	switch status {
	case domain.PaymentOrderStatusInitiated:
		// No additional fields needed

	case domain.PaymentOrderStatusReserved:
		po.LienID = cfg.lienID
		po.ReservedAt = &now

	case domain.PaymentOrderStatusExecuting:
		po.LienID = cfg.lienID
		po.GatewayReferenceID = cfg.gatewayRefID
		po.ReservedAt = &now
		po.ExecutingAt = &now

	case domain.PaymentOrderStatusCompleted:
		po.LienID = cfg.lienID
		po.GatewayReferenceID = cfg.gatewayRefID
		po.LedgerBookingID = cfg.ledgerBookingID
		po.ReservedAt = &now
		po.ExecutingAt = &now
		po.CompletedAt = &now

	case domain.PaymentOrderStatusFailed:
		po.FailureReason = cfg.failureReason
		po.ErrorCode = cfg.errorCode
		po.FailedAt = &now
		if cfg.lienID != "" {
			po.LienID = cfg.lienID
			po.ReservedAt = &now
		}

	case domain.PaymentOrderStatusCancelled:
		po.FailureReason = cfg.failureReason
		po.CancelledAt = &now
		if cfg.lienID != "" {
			po.LienID = cfg.lienID
			po.ReservedAt = &now
		}

	case domain.PaymentOrderStatusReversed:
		po.LienID = cfg.lienID
		po.GatewayReferenceID = cfg.gatewayRefID
		po.LedgerBookingID = cfg.ledgerBookingID
		po.FailureReason = cfg.failureReason
		po.ReservedAt = &now
		po.ExecutingAt = &now
		po.CompletedAt = &now
		po.ReversedAt = &now
	}
}

// NewMoney creates a Money instance with the specified amount in cents and currency.
func NewMoney(t *testing.T, amountCents int64, currency string) domain.Money {
	t.Helper()
	m, err := domain.NewMoney(currency, amountCents)
	if err != nil {
		t.Fatalf("Failed to create money: %v", err)
	}
	return m
}

// DefaultGBPMoney returns 100 GBP (10000 cents) for testing.
func DefaultGBPMoney(t *testing.T) domain.Money {
	t.Helper()
	return NewMoney(t, 10000, "GBP")
}

// DefaultUSDMoney returns 100 USD (10000 cents) for testing.
func DefaultUSDMoney(t *testing.T) domain.Money {
	t.Helper()
	return NewMoney(t, 10000, "USD")
}

// LargeGBPMoney returns 100,000 GBP (10,000,000 cents) for testing high-value payments.
func LargeGBPMoney(t *testing.T) domain.Money {
	t.Helper()
	return NewMoney(t, 10000000, "GBP")
}

// SmallGBPMoney returns 1 GBP (100 cents) for testing small payments.
func SmallGBPMoney(t *testing.T) domain.Money {
	t.Helper()
	return NewMoney(t, 100, "GBP")
}

// TestAccountID returns a consistent test account ID.
func TestAccountID() string {
	return "TEST-DEBTOR-001"
}

// TestCreditorReference returns a valid IBAN-style creditor reference for testing.
func TestCreditorReference() string {
	return "GB82WEST12345698765432"
}

// TestLienID returns a consistent test lien ID.
func TestLienID() string {
	return "lien-test-001"
}

// TestGatewayReferenceID returns a consistent test gateway reference ID.
func TestGatewayReferenceID() string {
	return "gw-ref-test-001"
}

// RandomIdempotencyKey generates a unique idempotency key for testing.
func RandomIdempotencyKey() string {
	return "idem-" + uuid.New().String()
}

// RandomCorrelationID generates a unique correlation ID for testing.
func RandomCorrelationID() string {
	return "corr-" + uuid.New().String()
}

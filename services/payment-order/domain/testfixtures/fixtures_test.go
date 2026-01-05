package testfixtures_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPaymentOrder_Defaults(t *testing.T) {
	po := testfixtures.NewPaymentOrder(t)

	assert.NotEqual(t, uuid.Nil, po.ID)
	assert.Equal(t, "TEST-DEBTOR-001", po.DebtorAccountID)
	assert.Equal(t, "GB82WEST12345698765432", po.CreditorReference)
	assert.Equal(t, domain.PaymentOrderStatusInitiated, po.Status)
	assert.NotEmpty(t, po.IdempotencyKey)
	assert.NotEmpty(t, po.CorrelationID)
	assert.Equal(t, 1, po.Version)
}

func TestNewPaymentOrder_WithOptions(t *testing.T) {
	customID := uuid.New()
	po := testfixtures.NewPaymentOrder(t,
		testfixtures.WithID(customID),
		testfixtures.WithDebtorAccountID("CUSTOM-ACC-001"),
		testfixtures.WithCreditorReference("CUSTOM-CRED-REF"),
		testfixtures.WithAmountCents(50000),
		testfixtures.WithCurrency("USD"),
		testfixtures.WithIdempotencyKey("custom-idem-key"),
		testfixtures.WithCorrelationID("custom-corr-id"),
	)

	assert.Equal(t, customID, po.ID)
	assert.Equal(t, "CUSTOM-ACC-001", po.DebtorAccountID)
	assert.Equal(t, "CUSTOM-CRED-REF", po.CreditorReference)
	assert.Equal(t, "custom-idem-key", po.IdempotencyKey)
	assert.Equal(t, "custom-corr-id", po.CorrelationID)
	assert.True(t, po.Amount.IsPositive())
}

func TestNewPaymentOrderInStatus_Initiated(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusInitiated)

	assert.Equal(t, domain.PaymentOrderStatusInitiated, po.Status)
	assert.Empty(t, po.LienID)
	assert.Empty(t, po.GatewayReferenceID)
	assert.Nil(t, po.ReservedAt)
	assert.Nil(t, po.ExecutingAt)
	assert.Nil(t, po.CompletedAt)
}

func TestNewPaymentOrderInStatus_Reserved(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved)

	assert.Equal(t, domain.PaymentOrderStatusReserved, po.Status)
	assert.NotEmpty(t, po.LienID)
	assert.NotNil(t, po.ReservedAt)
	assert.Empty(t, po.GatewayReferenceID)
	assert.Nil(t, po.ExecutingAt)
}

func TestNewPaymentOrderInStatus_Executing(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusExecuting)

	assert.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)
	assert.NotEmpty(t, po.LienID)
	assert.NotEmpty(t, po.GatewayReferenceID)
	assert.NotNil(t, po.ReservedAt)
	assert.NotNil(t, po.ExecutingAt)
	assert.Nil(t, po.CompletedAt)
}

func TestNewPaymentOrderInStatus_Completed(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)

	assert.Equal(t, domain.PaymentOrderStatusCompleted, po.Status)
	assert.NotEmpty(t, po.LienID)
	assert.NotEmpty(t, po.GatewayReferenceID)
	assert.NotEmpty(t, po.LedgerBookingID)
	assert.NotNil(t, po.ReservedAt)
	assert.NotNil(t, po.ExecutingAt)
	assert.NotNil(t, po.CompletedAt)
}

func TestNewPaymentOrderInStatus_Failed(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusFailed)

	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status)
	assert.NotEmpty(t, po.FailureReason)
	assert.NotEmpty(t, po.ErrorCode)
	assert.NotNil(t, po.FailedAt)
}

func TestNewPaymentOrderInStatus_Cancelled(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCancelled)

	assert.Equal(t, domain.PaymentOrderStatusCancelled, po.Status)
	assert.NotEmpty(t, po.FailureReason)
	assert.NotNil(t, po.CancelledAt)
}

func TestNewPaymentOrderInStatus_Reversed(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReversed)

	assert.Equal(t, domain.PaymentOrderStatusReversed, po.Status)
	assert.NotEmpty(t, po.LienID)
	assert.NotEmpty(t, po.GatewayReferenceID)
	assert.NotEmpty(t, po.LedgerBookingID)
	assert.NotNil(t, po.CompletedAt)
	assert.NotNil(t, po.ReversedAt)
}

func TestNewPaymentOrderInStatus_WithCustomOptions(t *testing.T) {
	customLienID := "custom-lien-123"
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusReserved,
		testfixtures.WithLienID(customLienID),
		testfixtures.WithDebtorAccountID("CUSTOM-ACC"),
	)

	assert.Equal(t, domain.PaymentOrderStatusReserved, po.Status)
	assert.Equal(t, customLienID, po.LienID)
	assert.Equal(t, "CUSTOM-ACC", po.DebtorAccountID)
}

func TestNewMoney(t *testing.T) {
	m := testfixtures.NewMoney(t, 5000, "GBP")

	assert.True(t, m.IsPositive())
	assert.Equal(t, "GBP", domain.CurrencyCode(m))
}

func TestDefaultGBPMoney(t *testing.T) {
	m := testfixtures.DefaultGBPMoney(t)

	assert.True(t, m.IsPositive())
	assert.Equal(t, "GBP", domain.CurrencyCode(m))
}

func TestDefaultUSDMoney(t *testing.T) {
	m := testfixtures.DefaultUSDMoney(t)

	assert.True(t, m.IsPositive())
	assert.Equal(t, "USD", domain.CurrencyCode(m))
}

func TestLargeGBPMoney(t *testing.T) {
	m := testfixtures.LargeGBPMoney(t)

	assert.True(t, m.IsPositive())
	assert.Equal(t, "GBP", domain.CurrencyCode(m))
}

func TestSmallGBPMoney(t *testing.T) {
	m := testfixtures.SmallGBPMoney(t)

	assert.True(t, m.IsPositive())
	assert.Equal(t, "GBP", domain.CurrencyCode(m))
}

func TestHelperFunctions(t *testing.T) {
	assert.Equal(t, "TEST-DEBTOR-001", testfixtures.TestAccountID())
	assert.Equal(t, "GB82WEST12345698765432", testfixtures.TestCreditorReference())
	assert.Equal(t, "lien-test-001", testfixtures.TestLienID())
	assert.Equal(t, "gw-ref-test-001", testfixtures.TestGatewayReferenceID())
}

func TestRandomIdempotencyKey_Uniqueness(t *testing.T) {
	key1 := testfixtures.RandomIdempotencyKey()
	key2 := testfixtures.RandomIdempotencyKey()

	assert.NotEqual(t, key1, key2)
	assert.Contains(t, key1, "idem-")
	assert.Contains(t, key2, "idem-")
}

func TestRandomCorrelationID_Uniqueness(t *testing.T) {
	id1 := testfixtures.RandomCorrelationID()
	id2 := testfixtures.RandomCorrelationID()

	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "corr-")
	assert.Contains(t, id2, "corr-")
}

func TestNewPaymentOrder_UniqueIDs(t *testing.T) {
	po1 := testfixtures.NewPaymentOrder(t)
	po2 := testfixtures.NewPaymentOrder(t)

	require.NotEqual(t, po1.ID, po2.ID, "Each payment order should have a unique ID")
	require.NotEqual(t, po1.IdempotencyKey, po2.IdempotencyKey, "Each payment order should have a unique idempotency key")
	require.NotEqual(t, po1.CorrelationID, po2.CorrelationID, "Each payment order should have a unique correlation ID")
}

package testfixtures_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	"github.com/stretchr/testify/assert"
)

func TestWithGatewayReferenceID_InCompletedStatus(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted,
		testfixtures.WithGatewayReferenceID("gw-custom-ref"),
	)
	assert.Equal(t, "gw-custom-ref", po.GatewayReferenceID)
}

func TestWithLedgerBookingID_InCompletedStatus(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted,
		testfixtures.WithLedgerBookingID("ledger-custom-id"),
	)
	assert.Equal(t, "ledger-custom-id", po.LedgerBookingID)
}

func TestWithFailureReason_InFailedStatus(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusFailed,
		testfixtures.WithFailureReason("custom failure reason"),
	)
	assert.Equal(t, "custom failure reason", po.FailureReason)
}

func TestWithErrorCode_InFailedStatus(t *testing.T) {
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusFailed,
		testfixtures.WithErrorCode("CUSTOM_ERROR"),
	)
	assert.Equal(t, "CUSTOM_ERROR", po.ErrorCode)
}

func TestNewMoney_USD(t *testing.T) {
	m := testfixtures.NewMoney(t, 500, "USD")
	assert.True(t, m.IsPositive())
}

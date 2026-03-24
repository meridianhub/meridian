package gateway_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatus_Constants verifies all gateway status constants are distinct and non-empty.
func TestStatus_Constants(t *testing.T) {
	t.Parallel()

	statuses := []gateway.Status{
		gateway.StatusAccepted,
		gateway.StatusRejected,
		gateway.StatusPending,
	}

	seen := make(map[gateway.Status]bool)
	for _, s := range statuses {
		assert.False(t, seen[s], "duplicate status constant: %s", s)
		seen[s] = true
		assert.NotEmpty(t, string(s))
	}
}

// TestPaymentRequest_Construction verifies that a PaymentRequest can be fully populated.
func TestPaymentRequest_Construction(t *testing.T) {
	t.Parallel()

	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	req := gateway.PaymentRequest{
		PaymentOrderID:    uuid.New(),
		DebtorAccountID:   "acc-123",
		CreditorReference: "GB82WEST12345698765432",
		Amount:            amount,
		IdempotencyKey:    "idem-key-1",
	}

	assert.NotEqual(t, uuid.UUID{}, req.PaymentOrderID)
	assert.Equal(t, "acc-123", req.DebtorAccountID)
	assert.Equal(t, "GB82WEST12345698765432", req.CreditorReference)
	assert.Equal(t, "idem-key-1", req.IdempotencyKey)
}

// TestPaymentResponse_Construction verifies PaymentResponse fields.
func TestPaymentResponse_Construction(t *testing.T) {
	t.Parallel()

	resp := gateway.PaymentResponse{
		GatewayReferenceID: "GW-" + uuid.New().String(),
		Status:             gateway.StatusAccepted,
		Message:            "payment accepted",
		PlatformFeeAmount:  250,
	}

	assert.NotEmpty(t, resp.GatewayReferenceID)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.Equal(t, "payment accepted", resp.Message)
	assert.Equal(t, int64(250), resp.PlatformFeeAmount)
}

// TestPaymentResponse_ZeroFee verifies that a zero platform fee is valid.
func TestPaymentResponse_ZeroFee(t *testing.T) {
	t.Parallel()

	resp := gateway.PaymentResponse{
		GatewayReferenceID: "GW-ref",
		Status:             gateway.StatusAccepted,
		PlatformFeeAmount:  0,
	}

	assert.Equal(t, int64(0), resp.PlatformFeeAmount)
}

// TestPaymentGateway_Interface verifies that MockGateway satisfies the PaymentGateway interface.
// This is a compile-time check.
func TestPaymentGateway_Interface(_ *testing.T) {
	var _ gateway.PaymentGateway = (*gateway.MockGateway)(nil)
}

// TestStatus_Accepted_IsNot_Rejected verifies semantic distinctness.
func TestStatus_Accepted_IsNot_Rejected(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, gateway.StatusAccepted, gateway.StatusRejected)
	assert.NotEqual(t, gateway.StatusAccepted, gateway.StatusPending)
	assert.NotEqual(t, gateway.StatusRejected, gateway.StatusPending)
}

package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cadomain "github.com/meridianhub/meridian/internal/current-account/domain"
)

const testLienID = "lien-001"

func TestNewPaymentOrder_Success(t *testing.T) {
	amount, err := cadomain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po, err := NewPaymentOrder(
		"debtor-acc-123",
		"GB82WEST12345698765432",
		amount,
		"idem-key-001",
		"corr-id-001",
	)

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, po.ID)
	assert.Equal(t, "debtor-acc-123", po.DebtorAccountID)
	assert.Equal(t, "GB82WEST12345698765432", po.CreditorReference)
	assert.Equal(t, int64(10000), po.Amount.AmountCents())
	assert.Equal(t, "GBP", po.Amount.Currency())
	assert.Equal(t, PaymentOrderStatusInitiated, po.Status)
	assert.Equal(t, "idem-key-001", po.IdempotencyKey)
	assert.Equal(t, "corr-id-001", po.CorrelationID)
	assert.Equal(t, 1, po.Version)
	assert.Empty(t, po.LienID)
	assert.Empty(t, po.GatewayReferenceID)
}

func TestNewPaymentOrder_MissingDebtorAccountID_Fails(t *testing.T) {
	amount, err := cadomain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	_, err = NewPaymentOrder("", "GB82WEST12345698765432", amount, "idem-key-001", "corr-id-001")

	assert.ErrorIs(t, err, ErrMissingDebtorAccountID)
}

func TestNewPaymentOrder_MissingCreditorReference_Fails(t *testing.T) {
	amount, err := cadomain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	_, err = NewPaymentOrder("debtor-acc-123", "", amount, "idem-key-001", "corr-id-001")

	assert.ErrorIs(t, err, ErrMissingCreditorReference)
}

func TestNewPaymentOrder_ZeroAmount_Fails(t *testing.T) {
	amount, err := cadomain.NewMoney("GBP", 0)
	require.NoError(t, err)

	_, err = NewPaymentOrder("debtor-acc-123", "GB82WEST12345698765432", amount, "idem-key-001", "corr-id-001")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderAmount)
}

func TestNewPaymentOrder_NegativeAmount_Fails(t *testing.T) {
	amount, err := cadomain.NewMoney("GBP", -10000)
	require.NoError(t, err)

	_, err = NewPaymentOrder("debtor-acc-123", "GB82WEST12345698765432", amount, "idem-key-001", "corr-id-001")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderAmount)
}

func TestNewPaymentOrder_MissingIdempotencyKey_Fails(t *testing.T) {
	amount, err := cadomain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	_, err = NewPaymentOrder("debtor-acc-123", "GB82WEST12345698765432", amount, "", "corr-id-001")

	assert.ErrorIs(t, err, ErrMissingIdempotencyKey)
}

func TestNewPaymentOrder_MissingCorrelationID_Fails(t *testing.T) {
	amount, err := cadomain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	_, err = NewPaymentOrder("debtor-acc-123", "GB82WEST12345698765432", amount, "idem-key-001", "")

	assert.ErrorIs(t, err, ErrMissingCorrelationID)
}

func TestPaymentOrder_Reserve_FromInitiated(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Reserve("lien-001")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusReserved, po.Status)
	assert.Equal(t, "lien-001", po.LienID)
	assert.NotNil(t, po.ReservedAt)
}

func TestPaymentOrder_Reserve_MissingLienID_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Reserve("")

	assert.ErrorIs(t, err, ErrMissingLienID)
	assert.Equal(t, PaymentOrderStatusInitiated, po.Status)
}

func TestPaymentOrder_Reserve_FromReserved_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusReserved)

	err := po.Reserve("lien-002")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Reserve_FromExecuting_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusExecuting)

	err := po.Reserve("lien-002")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Execute_FromReserved(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusReserved)

	err := po.Execute("gw-ref-001")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusExecuting, po.Status)
	assert.Equal(t, "gw-ref-001", po.GatewayReferenceID)
	assert.NotNil(t, po.ExecutingAt)
}

func TestPaymentOrder_Execute_MissingGatewayReferenceID_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusReserved)

	err := po.Execute("")

	assert.ErrorIs(t, err, ErrMissingGatewayReferenceID)
	assert.Equal(t, PaymentOrderStatusReserved, po.Status)
}

func TestPaymentOrder_Execute_FromInitiated_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Execute("gw-ref-001")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Execute_FromExecuting_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusExecuting)

	err := po.Execute("gw-ref-002")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Complete_FromExecuting(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusExecuting)

	err := po.Complete("ledger-001")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusCompleted, po.Status)
	assert.Equal(t, "ledger-001", po.LedgerBookingID)
	assert.NotNil(t, po.CompletedAt)
}

func TestPaymentOrder_Complete_FromReserved_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusReserved)

	err := po.Complete("ledger-001")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Complete_FromInitiated_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Complete("ledger-001")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Fail_FromInitiated(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Fail("Validation failed", "VALIDATION_ERROR")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusFailed, po.Status)
	assert.Equal(t, "Validation failed", po.FailureReason)
	assert.Equal(t, "VALIDATION_ERROR", po.ErrorCode)
	assert.NotNil(t, po.FailedAt)
}

func TestPaymentOrder_Fail_FromReserved(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusReserved)

	err := po.Fail("Gateway timeout", "GATEWAY_TIMEOUT")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusFailed, po.Status)
}

func TestPaymentOrder_Fail_FromExecuting(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusExecuting)

	err := po.Fail("Gateway rejected", "GATEWAY_REJECTED")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusFailed, po.Status)
}

func TestPaymentOrder_Fail_MissingReason_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Fail("", "SOME_ERROR")

	assert.ErrorIs(t, err, ErrMissingFailureReason)
	assert.Equal(t, PaymentOrderStatusInitiated, po.Status)
}

func TestPaymentOrder_Fail_Idempotent(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Fail("First failure", "FIRST_ERROR")
	require.NoError(t, err)

	// Second call should be idempotent
	err = po.Fail("Second failure", "SECOND_ERROR")
	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusFailed, po.Status)
	// Original reason should be preserved
	assert.Equal(t, "First failure", po.FailureReason)
	assert.Equal(t, "FIRST_ERROR", po.ErrorCode)
}

func TestPaymentOrder_Fail_FromCompleted_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusCompleted)

	err := po.Fail("Cannot fail", "ERROR")

	assert.ErrorIs(t, err, ErrPaymentOrderTerminal)
	assert.Equal(t, PaymentOrderStatusCompleted, po.Status)
}

func TestPaymentOrder_Fail_FromCancelled_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusCancelled)

	err := po.Fail("Cannot fail", "ERROR")

	assert.ErrorIs(t, err, ErrPaymentOrderTerminal)
}

func TestPaymentOrder_Cancel_FromInitiated(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Cancel("User cancelled")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusCancelled, po.Status)
	assert.Equal(t, "User cancelled", po.FailureReason)
	assert.NotNil(t, po.CancelledAt)
}

func TestPaymentOrder_Cancel_FromReserved(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusReserved)

	err := po.Cancel("User cancelled")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusCancelled, po.Status)
}

func TestPaymentOrder_Cancel_Idempotent(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Cancel("First cancellation")
	require.NoError(t, err)

	// Second call should be idempotent
	err = po.Cancel("Second cancellation")
	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusCancelled, po.Status)
	// Original reason should be preserved
	assert.Equal(t, "First cancellation", po.FailureReason)
}

func TestPaymentOrder_Cancel_FromExecuting_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusExecuting)

	err := po.Cancel("Cannot cancel")

	assert.ErrorIs(t, err, ErrPaymentOrderNotCancellable)
	assert.Equal(t, PaymentOrderStatusExecuting, po.Status)
}

func TestPaymentOrder_Cancel_FromCompleted_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusCompleted)

	err := po.Cancel("Cannot cancel")

	assert.ErrorIs(t, err, ErrPaymentOrderNotCancellable)
}

func TestPaymentOrder_Cancel_FromFailed_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusFailed)

	err := po.Cancel("Cannot cancel")

	assert.ErrorIs(t, err, ErrPaymentOrderNotCancellable)
}

func TestPaymentOrder_Reverse_FromCompleted(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusCompleted)

	err := po.Reverse("Chargeback requested")

	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusReversed, po.Status)
	assert.Equal(t, "Chargeback requested", po.FailureReason)
	assert.NotNil(t, po.ReversedAt)
}

func TestPaymentOrder_Reverse_Idempotent(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusCompleted)

	err := po.Reverse("First reversal")
	require.NoError(t, err)

	// Second call should be idempotent
	err = po.Reverse("Second reversal")
	assert.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusReversed, po.Status)
	// Original reason should be preserved
	assert.Equal(t, "First reversal", po.FailureReason)
}

func TestPaymentOrder_Reverse_FromInitiated_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Reverse("Cannot reverse")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Reverse_FromReserved_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusReserved)

	err := po.Reverse("Cannot reverse")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Reverse_FromExecuting_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusExecuting)

	err := po.Reverse("Cannot reverse")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Reverse_FromFailed_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusFailed)

	err := po.Reverse("Cannot reverse")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Reverse_FromCancelled_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusCancelled)

	err := po.Reverse("Cannot reverse")

	assert.ErrorIs(t, err, ErrInvalidPaymentOrderTransition)
}

func TestPaymentOrder_Reverse_MissingReason_Fails(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusCompleted)

	err := po.Reverse("")

	assert.ErrorIs(t, err, ErrMissingReversalReason)
	assert.Equal(t, PaymentOrderStatusCompleted, po.Status)
}

func TestPaymentOrder_IsTerminal(t *testing.T) {
	tests := []struct {
		status   PaymentOrderStatus
		expected bool
	}{
		{PaymentOrderStatusInitiated, false},
		{PaymentOrderStatusReserved, false},
		{PaymentOrderStatusExecuting, false},
		{PaymentOrderStatusCompleted, true},
		{PaymentOrderStatusFailed, true},
		{PaymentOrderStatusCancelled, true},
		{PaymentOrderStatusReversed, true},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			po := createTestPaymentOrder(t, tc.status)
			assert.Equal(t, tc.expected, po.IsTerminal())
		})
	}
}

func TestPaymentOrder_CanCancel(t *testing.T) {
	tests := []struct {
		status   PaymentOrderStatus
		expected bool
	}{
		{PaymentOrderStatusInitiated, true},
		{PaymentOrderStatusReserved, true},
		{PaymentOrderStatusExecuting, false},
		{PaymentOrderStatusCompleted, false},
		{PaymentOrderStatusFailed, false},
		{PaymentOrderStatusCancelled, false},
		{PaymentOrderStatusReversed, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			po := createTestPaymentOrder(t, tc.status)
			assert.Equal(t, tc.expected, po.CanCancel())
		})
	}
}

func TestPaymentOrder_CanReverse(t *testing.T) {
	tests := []struct {
		status   PaymentOrderStatus
		expected bool
	}{
		{PaymentOrderStatusInitiated, false},
		{PaymentOrderStatusReserved, false},
		{PaymentOrderStatusExecuting, false},
		{PaymentOrderStatusCompleted, true},
		{PaymentOrderStatusFailed, false},
		{PaymentOrderStatusCancelled, false},
		{PaymentOrderStatusReversed, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			po := createTestPaymentOrder(t, tc.status)
			assert.Equal(t, tc.expected, po.CanReverse())
		})
	}
}

func TestPaymentOrder_RequiresLienRelease(t *testing.T) {
	t.Run("reserved with lien", func(t *testing.T) {
		po := createTestPaymentOrder(t, PaymentOrderStatusReserved)
		po.LienID = testLienID
		assert.True(t, po.RequiresLienRelease())
	})

	t.Run("executing with lien", func(t *testing.T) {
		po := createTestPaymentOrder(t, PaymentOrderStatusExecuting)
		po.LienID = testLienID
		assert.True(t, po.RequiresLienRelease())
	})

	t.Run("initiated with no lien", func(t *testing.T) {
		po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)
		assert.False(t, po.RequiresLienRelease())
	})

	t.Run("completed with lien", func(t *testing.T) {
		po := createTestPaymentOrder(t, PaymentOrderStatusCompleted)
		po.LienID = testLienID
		assert.False(t, po.RequiresLienRelease())
	})

	t.Run("failed with no lien", func(t *testing.T) {
		po := createTestPaymentOrder(t, PaymentOrderStatusFailed)
		assert.False(t, po.RequiresLienRelease())
	})
}

func TestPaymentOrder_SetCausationID(t *testing.T) {
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)
	originalUpdatedAt := po.UpdatedAt

	po.SetCausationID("event-001")

	assert.Equal(t, "event-001", po.CausationID)
	assert.True(t, po.UpdatedAt.After(originalUpdatedAt) || po.UpdatedAt.Equal(originalUpdatedAt))
}

func TestPaymentOrder_HappyPathTransitions(t *testing.T) {
	// Test the complete happy path: INITIATED -> RESERVED -> EXECUTING -> COMPLETED
	amount, err := cadomain.NewMoney("GBP", 50000)
	require.NoError(t, err)

	po, err := NewPaymentOrder(
		"debtor-123",
		"GB82WEST12345698765432",
		amount,
		"idem-happy-path",
		"corr-happy-path",
	)
	require.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusInitiated, po.Status)

	// Reserve funds
	err = po.Reserve("lien-happy-path")
	require.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusReserved, po.Status)
	assert.NotNil(t, po.ReservedAt)

	// Execute payment
	err = po.Execute("gw-ref-happy-path")
	require.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusExecuting, po.Status)
	assert.NotNil(t, po.ExecutingAt)

	// Complete payment
	err = po.Complete("ledger-happy-path")
	require.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusCompleted, po.Status)
	assert.NotNil(t, po.CompletedAt)
	assert.True(t, po.IsTerminal())
}

func TestPaymentOrder_FailurePathWithCompensation(t *testing.T) {
	// Test failure from RESERVED state which requires lien release
	po := createTestPaymentOrder(t, PaymentOrderStatusInitiated)

	err := po.Reserve("lien-to-release")
	require.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusReserved, po.Status)
	assert.True(t, po.RequiresLienRelease())

	// Fail the payment
	err = po.Fail("Gateway unavailable", "GATEWAY_UNAVAILABLE")
	require.NoError(t, err)
	assert.Equal(t, PaymentOrderStatusFailed, po.Status)
	// After failure, lien release is no longer required (status changed)
	assert.False(t, po.RequiresLienRelease())
}

// createTestPaymentOrder is a helper to create a payment order with a specific status for testing
func createTestPaymentOrder(t *testing.T, status PaymentOrderStatus) *PaymentOrder {
	t.Helper()
	amount, err := cadomain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	po := &PaymentOrder{
		ID:                uuid.New(),
		DebtorAccountID:   "debtor-test-123",
		CreditorReference: "GB82WEST12345698765432",
		Amount:            amount,
		Status:            status,
		IdempotencyKey:    "test-idem-key",
		CorrelationID:     "test-corr-id",
		Version:           1,
	}

	return po
}

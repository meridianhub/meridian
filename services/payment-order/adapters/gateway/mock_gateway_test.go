package gateway_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
)

func createTestPaymentRequest(t *testing.T, amountCents int64) gateway.PaymentRequest {
	t.Helper()
	amount, err := domain.NewMoney("GBP", amountCents)
	require.NoError(t, err)
	return gateway.PaymentRequest{
		PaymentOrderID:    uuid.New(),
		DebtorAccountID:   "debtor-123",
		CreditorReference: "GB82WEST12345698765432",
		Amount:            amount,
		IdempotencyKey:    "idem-key-" + uuid.New().String(),
	}
}

func TestMockGateway_SendPayment_Success(t *testing.T) {
	gw := gateway.NewMockGateway(gateway.DefaultMockGatewayConfig())
	req := createTestPaymentRequest(t, 10000)

	resp, err := gw.SendPayment(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.NotEmpty(t, resp.GatewayReferenceID)
	assert.True(t, strings.HasPrefix(resp.GatewayReferenceID, "GW-"))
	assert.Equal(t, "payment accepted", resp.Message)
}

func TestMockGateway_SendPayment_DeterministicFailure(t *testing.T) {
	config := gateway.MockGatewayConfig{
		DeterministicFailures: true,
	}
	gw := gateway.NewMockGateway(config)

	tests := []struct {
		name        string
		amountCents int64
		wantStatus  gateway.Status
	}{
		{"amount ending in 99 fails", 1099, gateway.StatusRejected},
		{"amount ending in 99 fails large", 99999, gateway.StatusRejected},
		{"amount not ending in 99 succeeds", 1000, gateway.StatusAccepted},
		{"amount ending in 00 succeeds", 1100, gateway.StatusAccepted},
		{"amount ending in 01 succeeds", 1001, gateway.StatusAccepted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := createTestPaymentRequest(t, tt.amountCents)
			resp, err := gw.SendPayment(context.Background(), req)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.Status)
			assert.NotEmpty(t, resp.GatewayReferenceID)
		})
	}
}

func TestMockGateway_SendPayment_RandomFailure(t *testing.T) {
	// Use seeded RNG for deterministic behavior
	config := gateway.MockGatewayConfig{
		FailureRate: 1.0, // 100% failure rate
	}
	gw := gateway.NewMockGatewayWithSeed(config, 12345)
	req := createTestPaymentRequest(t, 10000)

	resp, err := gw.SendPayment(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, gateway.StatusRejected, resp.Status)
	assert.Equal(t, "random rejection", resp.Message)
}

func TestMockGateway_SendPayment_ZeroFailureRate(t *testing.T) {
	config := gateway.MockGatewayConfig{
		FailureRate: 0.0,
	}
	gw := gateway.NewMockGateway(config)
	req := createTestPaymentRequest(t, 10000)

	// Run multiple times to ensure consistent success
	for i := 0; i < 10; i++ {
		resp, err := gw.SendPayment(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, gateway.StatusAccepted, resp.Status)
	}
}

func TestMockGateway_SendPayment_Timeout(t *testing.T) {
	config := gateway.MockGatewayConfig{
		TimeoutRate: 1.0, // 100% timeout rate
	}
	gw := gateway.NewMockGatewayWithSeed(config, 12345)
	req := createTestPaymentRequest(t, 10000)

	_, err := gw.SendPayment(context.Background(), req)

	assert.ErrorIs(t, err, gateway.ErrGatewayTimeout)
}

func TestMockGateway_SendPayment_ContextCancellation(t *testing.T) {
	config := gateway.MockGatewayConfig{
		Latency: 100 * time.Millisecond,
	}
	gw := gateway.NewMockGateway(config)
	req := createTestPaymentRequest(t, 10000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := gw.SendPayment(ctx, req)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestMockGateway_SendPayment_ContextDeadline(t *testing.T) {
	config := gateway.MockGatewayConfig{
		Latency: 100 * time.Millisecond,
	}
	gw := gateway.NewMockGateway(config)
	req := createTestPaymentRequest(t, 10000)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := gw.SendPayment(ctx, req)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestMockGateway_SendPayment_Latency(t *testing.T) {
	latency := 50 * time.Millisecond
	config := gateway.MockGatewayConfig{
		Latency: latency,
	}
	gw := gateway.NewMockGateway(config)
	req := createTestPaymentRequest(t, 10000)

	start := time.Now()
	resp, err := gw.SendPayment(context.Background(), req)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.GreaterOrEqual(t, elapsed, latency)
}

func TestMockGateway_GatewayReferenceID_Format(t *testing.T) {
	gw := gateway.NewMockGateway(gateway.DefaultMockGatewayConfig())
	req := createTestPaymentRequest(t, 10000)

	resp, err := gw.SendPayment(context.Background(), req)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(resp.GatewayReferenceID, "GW-"))
	// Verify it's a valid UUID after the prefix
	uuidPart := strings.TrimPrefix(resp.GatewayReferenceID, "GW-")
	_, err = uuid.Parse(uuidPart)
	assert.NoError(t, err, "GatewayReferenceID should contain a valid UUID")
}

func TestMockGateway_GatewayReferenceID_Unique(t *testing.T) {
	gw := gateway.NewMockGateway(gateway.DefaultMockGatewayConfig())
	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		req := createTestPaymentRequest(t, 10000)
		resp, err := gw.SendPayment(context.Background(), req)
		require.NoError(t, err)

		assert.False(t, seen[resp.GatewayReferenceID], "GatewayReferenceID should be unique")
		seen[resp.GatewayReferenceID] = true
	}
}

func TestMockGateway_ImplementsInterface(_ *testing.T) {
	var _ gateway.PaymentGateway = (*gateway.MockGateway)(nil)
}

func TestMockGateway_ConcurrentSendPayment(t *testing.T) {
	config := gateway.MockGatewayConfig{
		FailureRate: 0.5,
	}
	gw := gateway.NewMockGatewayWithSeed(config, 12345)

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			req := createTestPaymentRequest(t, 10000)
			resp, err := gw.SendPayment(context.Background(), req)
			// Should not panic or return unexpected errors
			require.NoError(t, err)
			assert.NotEmpty(t, resp.GatewayReferenceID)
			assert.Contains(t, []gateway.Status{gateway.StatusAccepted, gateway.StatusRejected}, resp.Status)
		}()
	}

	wg.Wait()
}

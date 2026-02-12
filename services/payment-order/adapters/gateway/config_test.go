package gateway_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
)

func TestDefaultConfig(t *testing.T) {
	config := gateway.DefaultConfig()

	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, 3, config.MaxRetries)
	assert.Equal(t, 1*time.Second, config.RetryBackoff)
	assert.False(t, config.UseMock)
}

func TestNew_WithMock(t *testing.T) {
	config := gateway.Config{
		UseMock: true,
		MockConfig: gateway.MockGatewayConfig{
			FailureRate: 0.5,
		},
	}

	gw := gateway.New(config)

	assert.NotNil(t, gw)
	assert.IsType(t, &gateway.MockGateway{}, gw)
}

func TestNew_WithoutMock(t *testing.T) {
	config := gateway.Config{
		UseMock: false,
	}

	gw := gateway.New(config)

	// Should return a fallback gateway (mock for now until real implementation)
	assert.NotNil(t, gw)
}

func TestProviderConstants(t *testing.T) {
	assert.Equal(t, "stripe", gateway.ProviderStripe)
	assert.Equal(t, "mock", gateway.ProviderMock)
}

func TestDefaultMockGatewayConfig(t *testing.T) {
	config := gateway.DefaultMockGatewayConfig()

	assert.Zero(t, config.Latency)
	assert.Zero(t, config.FailureRate)
	assert.False(t, config.DeterministicFailures)
	assert.Zero(t, config.TimeoutRate)
}

package ports_test

import (
	"testing"

	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/stretchr/testify/assert"
)

func TestPortConstantsAreAccessible(t *testing.T) {
	// gRPC service ports
	assert.Equal(t, 50051, ports.CurrentAccount)
	assert.Equal(t, 50052, ports.FinancialAccounting)
	assert.Equal(t, 50053, ports.PositionKeeping)
	assert.Equal(t, 50054, ports.PaymentOrder)
	assert.Equal(t, 50055, ports.Party)
	assert.Equal(t, 50056, ports.Tenant)

	// HTTP service ports
	assert.Equal(t, 8080, ports.Gateway)
	assert.Equal(t, 8081, ports.HTTPHealth)
	assert.Equal(t, 9090, ports.HTTPMetrics)
}

func TestGrpcPortsAreInExpectedRange(t *testing.T) {
	grpcPorts := []int{
		ports.CurrentAccount,
		ports.FinancialAccounting,
		ports.PositionKeeping,
		ports.PaymentOrder,
		ports.Party,
		ports.Tenant,
	}

	for _, port := range grpcPorts {
		assert.GreaterOrEqual(t, port, 50051, "gRPC port should be >= 50051")
		assert.LessOrEqual(t, port, 50099, "gRPC port should be <= 50099")
	}
}

func TestPortsAreUnique(t *testing.T) {
	allPorts := []int{
		ports.CurrentAccount,
		ports.FinancialAccounting,
		ports.PositionKeeping,
		ports.PaymentOrder,
		ports.Party,
		ports.Tenant,
		ports.Gateway,
		ports.HTTPHealth,
		ports.HTTPMetrics,
	}

	seen := make(map[int]bool)
	for _, port := range allPorts {
		assert.False(t, seen[port], "port %d is duplicated", port)
		seen[port] = true
	}
}

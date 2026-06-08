package ports_test

import (
	"testing"

	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	internalaccountclient "github.com/meridianhub/meridian/services/internal-account/client"
	marketinformationclient "github.com/meridianhub/meridian/services/market-information/client"
	operationalgatewayclient "github.com/meridianhub/meridian/services/operational-gateway/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	positionkeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"
	reconciliationclient "github.com/meridianhub/meridian/services/reconciliation/client"
	referencedataclient "github.com/meridianhub/meridian/services/reference-data/client"
	tenantclient "github.com/meridianhub/meridian/services/tenant/client"
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

// TestClientDefaultPortsMatchRegistry is a drift-prevention guard: every service
// client's DefaultPort must equal its canonical constant in this registry. If anyone
// re-hardcodes a port that diverges from the registry, this test fails loudly.
//
// To add a new service client, append one row to the table below.
func TestClientDefaultPortsMatchRegistry(t *testing.T) {
	cases := []struct {
		service      string
		clientPort   int
		registryPort int
	}{
		{"current-account", currentaccountclient.DefaultPort, ports.CurrentAccount},
		{"financial-accounting", financialaccountingclient.DefaultPort, ports.FinancialAccounting},
		{"position-keeping", positionkeepingclient.DefaultPort, ports.PositionKeeping},
		{"party", partyclient.DefaultPort, ports.Party},
		{"tenant", tenantclient.DefaultPort, ports.Tenant},
		{"internal-account", internalaccountclient.DefaultPort, ports.InternalAccount},
		{"market-information", marketinformationclient.DefaultPort, ports.MarketInformation},
		{"reference-data", referencedataclient.DefaultPort, ports.ReferenceData},
		{"reconciliation", reconciliationclient.DefaultPort, ports.Reconciliation},
		{"operational-gateway", operationalgatewayclient.DefaultPort, ports.OperationalGateway},
		{"financial-gateway", financialgatewayclient.DefaultPort, ports.FinancialGateway},
	}

	for _, tc := range cases {
		t.Run(tc.service, func(t *testing.T) {
			assert.Equal(t, tc.registryPort, tc.clientPort,
				"%s client DefaultPort must match ports registry constant", tc.service)
		})
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

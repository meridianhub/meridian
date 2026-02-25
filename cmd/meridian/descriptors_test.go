package main

import (
	"testing"

	"github.com/meridianhub/meridian/services/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"
)

// TestGetProtoDescriptors verifies that the embedded proto descriptor set is
// non-empty and parseable as a valid FileDescriptorSet.
func TestGetProtoDescriptors(t *testing.T) {
	data := GetProtoDescriptors()
	require.NotEmpty(t, data, "GetProtoDescriptors must return non-empty bytes")

	var fds descriptorpb.FileDescriptorSet
	err := proto.Unmarshal(data, &fds)
	require.NoError(t, err, "descriptor bytes must parse as a valid FileDescriptorSet")
	require.NotEmpty(t, fds.File, "FileDescriptorSet must contain at least one file descriptor")
}

// TestProtoDescriptors_ContainsAllServices verifies the descriptor set contains all
// Meridian gRPC services needed by the Vanguard transcoder.
func TestProtoDescriptors_ContainsAllServices(t *testing.T) {
	data := GetProtoDescriptors()
	require.NotEmpty(t, data)

	var fds descriptorpb.FileDescriptorSet
	require.NoError(t, proto.Unmarshal(data, &fds))

	// Build a set of all service names found across all file descriptors.
	found := make(map[string]bool)
	for _, file := range fds.File {
		for _, svc := range file.Service {
			found[svc.GetName()] = true
		}
	}

	// These are the 11 services wired into the unified binary (cmd/meridian/main.go).
	required := []string{
		"PartyService",
		"CurrentAccountService",
		"PositionKeepingService",
		"FinancialAccountingService",
		"PaymentOrderService",
		"MarketInformationService",
		"AccountReconciliationService",
		"InternalAccountService",
		"SagaRegistryService",
		"TenantService",
		"ForecastingService",
	}

	for _, svc := range required {
		assert.True(t, found[svc], "descriptor set must contain service %q", svc)
	}
}

// TestWireGateway_TranscoderBuildsCleanly verifies that wireGateway successfully
// constructs the Vanguard transcoder from the embedded descriptor set and the
// registered serviceNames list. A build failure here indicates a mismatch between
// serviceNames in main.go and the services present in the descriptor.
func TestWireGateway_TranscoderBuildsCleanly(t *testing.T) {
	// Construct per-service backends as wireGateway does, using a dummy address.
	backends := make([]gateway.ServiceBackend, 0, len(serviceNames))
	for _, name := range serviceNames {
		backends = append(backends, gateway.ServiceBackend{
			ServiceName: name,
			BackendAddr: "localhost:50051",
		})
	}

	handler, err := gateway.NewTranscoder(GetProtoDescriptors(), backends)
	require.NoError(t, err, "transcoder must build cleanly from embedded descriptor and serviceNames")
	assert.NotNil(t, handler)
}

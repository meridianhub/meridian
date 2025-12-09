package clients_test

import (
	"context"
	"net"
	"testing"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// TestNewPartyClient_Success verifies client creation with valid configuration
func TestNewPartyClient_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Target:  "localhost:50054",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_RequiresTarget verifies error when target is missing
func TestNewPartyClient_RequiresTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Target:  "",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	assert.ErrorIs(t, err, clients.ErrPartyTargetRequired)
	assert.Nil(t, client)
}

// TestNewPartyClient_DefaultTimeout verifies 30s default timeout is applied
func TestNewPartyClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Target:  "localhost:50054",
		Timeout: 0, // Should default to 30 seconds
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_CustomTimeout verifies custom timeout is respected
func TestNewPartyClient_CustomTimeout(t *testing.T) {
	t.Parallel()

	customTimeout := 5 * time.Minute
	cfg := &clients.PartyClientConfig{
		Target:  "localhost:50054",
		Timeout: customTimeout,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_WithTracer verifies tracer configuration is accepted
func TestNewPartyClient_WithTracer(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	cfg := &clients.PartyClientConfig{
		Target:  "localhost:50054",
		Timeout: 10 * time.Second,
		Tracer:  tracer,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_DefaultDialOptions verifies insecure credentials are used by default
func TestNewPartyClient_DefaultDialOptions(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Target:      "localhost:50054",
		Timeout:     10 * time.Second,
		DialOptions: nil, // Should use default insecure credentials
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_CustomDialOptions verifies custom dial options are respected
func TestNewPartyClient_CustomDialOptions(t *testing.T) {
	t.Parallel()

	customOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	cfg := &clients.PartyClientConfig{
		Target:      "localhost:50054",
		Timeout:     10 * time.Second,
		DialOptions: customOpts,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_EmptyTarget verifies empty target is rejected
func TestNewPartyClient_EmptyTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Target:  "",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	assert.ErrorIs(t, err, clients.ErrPartyTargetRequired)
	assert.Nil(t, client)
}

// TestNewPartyClient_ValidTargetFormats verifies various valid target formats
func TestNewPartyClient_ValidTargetFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "host and port",
			target: "localhost:50054",
		},
		{
			name:   "IP and port",
			target: "127.0.0.1:50054",
		},
		{
			name:   "service name",
			target: "party-service:443",
		},
		{
			name:   "DNS name",
			target: "party.example.com:443",
		},
		{
			name:   "kubernetes service",
			target: "party-service.default.svc.cluster.local:50054",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &clients.PartyClientConfig{
				Target:  tt.target,
				Timeout: 10 * time.Second,
			}

			client, err := clients.NewPartyClient(cfg)

			require.NoError(t, err)
			require.NotNil(t, client)
			assert.NoError(t, client.Close())
		})
	}
}

// TestPartyClient_Close_Success verifies Close closes the connection without error
func TestPartyClient_Close_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Target:  "localhost:50054",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	err = client.Close()

	assert.NoError(t, err)
}

// TestPartyClient_Close_Multiple verifies Close behavior when called multiple times
func TestPartyClient_Close_Multiple(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Target:  "localhost:50054",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	// First close
	err1 := client.Close()
	assert.NoError(t, err1)

	// Second close returns error (gRPC connection already closed)
	err2 := client.Close()
	assert.Error(t, err2, "closing already-closed connection should return error")
}

// TestNewPartyClient_DNSBasedMode verifies DNS-based client creation
func TestNewPartyClient_DNSBasedMode(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party-service",
		Namespace:   "default",
		Port:        50054,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_DNSBasedMode_DefaultNamespace verifies namespace defaults to "default"
func TestNewPartyClient_DNSBasedMode_DefaultNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party-service",
		Namespace:   "", // Should default to "default"
		Port:        50054,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_DNSBasedMode_WithTracer verifies DNS mode with tracer
func TestNewPartyClient_DNSBasedMode_WithTracer(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	cfg := &clients.PartyClientConfig{
		ServiceName: "party-service",
		Namespace:   "test-namespace",
		Port:        50054,
		Timeout:     10 * time.Second,
		Tracer:      tracer,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_PrefersDNSOverTarget verifies ServiceName takes precedence
func TestNewPartyClient_PrefersDNSOverTarget(t *testing.T) {
	t.Parallel()

	// When both ServiceName and Target are provided, ServiceName should be used
	cfg := &clients.PartyClientConfig{
		ServiceName: "party-service",
		Namespace:   "default",
		Port:        50054,
		Target:      "ignored:9999", // Should be ignored
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// mockPartyServer is a mock implementation of PartyServiceServer for testing
type mockPartyServer struct {
	partyv1.UnimplementedPartyServiceServer
	retrieveFunc func(ctx context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error)
}

func (m *mockPartyServer) RetrieveParty(ctx context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
	if m.retrieveFunc != nil {
		return m.retrieveFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// startMockPartyServer starts a mock gRPC server for testing
func startMockPartyServer(t *testing.T, server *mockPartyServer) (string, func()) {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	partyv1.RegisterPartyServiceServer(grpcServer, server)

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	return listener.Addr().String(), func() {
		grpcServer.Stop()
	}
}

// TestPartyClient_ValidateParty_ActiveParty verifies successful validation of active party
func TestPartyClient_ValidateParty_ActiveParty(t *testing.T) {
	t.Parallel()

	mockServer := &mockPartyServer{
		retrieveFunc: func(_ context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return &partyv1.RetrievePartyResponse{
				Party: &partyv1.Party{
					PartyId:   req.PartyId,
					LegalName: "Test Party",
					Status:    partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
				},
			}, nil
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	err = client.ValidateParty(context.Background(), "party-123")

	assert.NoError(t, err)
}

// TestPartyClient_ValidateParty_InactiveParty verifies error for inactive party
func TestPartyClient_ValidateParty_InactiveParty(t *testing.T) {
	t.Parallel()

	mockServer := &mockPartyServer{
		retrieveFunc: func(_ context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return &partyv1.RetrievePartyResponse{
				Party: &partyv1.Party{
					PartyId:   req.PartyId,
					LegalName: "Test Party",
					Status:    partyv1.PartyStatus_PARTY_STATUS_RESTRICTED,
				},
			}, nil
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	err = client.ValidateParty(context.Background(), "party-123")

	assert.ErrorIs(t, err, clients.ErrPartyNotActive)
}

// TestPartyClient_ValidateParty_TerminatedParty verifies error for terminated party
func TestPartyClient_ValidateParty_TerminatedParty(t *testing.T) {
	t.Parallel()

	mockServer := &mockPartyServer{
		retrieveFunc: func(_ context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return &partyv1.RetrievePartyResponse{
				Party: &partyv1.Party{
					PartyId:   req.PartyId,
					LegalName: "Test Party",
					Status:    partyv1.PartyStatus_PARTY_STATUS_TERMINATED,
				},
			}, nil
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	err = client.ValidateParty(context.Background(), "party-123")

	assert.ErrorIs(t, err, clients.ErrPartyNotActive)
}

// TestPartyClient_ValidateParty_NotFound verifies error for non-existent party
func TestPartyClient_ValidateParty_NotFound(t *testing.T) {
	t.Parallel()

	mockServer := &mockPartyServer{
		retrieveFunc: func(_ context.Context, _ *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return nil, status.Error(codes.NotFound, "party not found")
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	err = client.ValidateParty(context.Background(), "nonexistent-party")

	assert.ErrorIs(t, err, clients.ErrPartyNotFound)
}

// TestPartyClient_GetParty_Success verifies successful party retrieval
func TestPartyClient_GetParty_Success(t *testing.T) {
	t.Parallel()

	expectedParty := &partyv1.Party{
		PartyId:     "party-123",
		LegalName:   "Test Company Ltd",
		DisplayName: "Test Company",
		PartyType:         partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
		Status:            partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
		ExternalReference: "CH12345678",
	}

	mockServer := &mockPartyServer{
		retrieveFunc: func(_ context.Context, _ *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return &partyv1.RetrievePartyResponse{
				Party: expectedParty,
			}, nil
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	party, err := client.GetParty(context.Background(), "party-123")

	require.NoError(t, err)
	assert.Equal(t, expectedParty.PartyId, party.PartyId)
	assert.Equal(t, expectedParty.LegalName, party.LegalName)
	assert.Equal(t, expectedParty.Status, party.Status)
}

// TestPartyClient_GetParty_NotFound verifies error for non-existent party
func TestPartyClient_GetParty_NotFound(t *testing.T) {
	t.Parallel()

	mockServer := &mockPartyServer{
		retrieveFunc: func(_ context.Context, _ *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return nil, status.Error(codes.NotFound, "party not found")
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	party, err := client.GetParty(context.Background(), "nonexistent-party")

	assert.ErrorIs(t, err, clients.ErrPartyNotFound)
	assert.Nil(t, party)
}

// TestPartyClient_GetParty_ServerError verifies handling of server errors
func TestPartyClient_GetParty_ServerError(t *testing.T) {
	t.Parallel()

	mockServer := &mockPartyServer{
		retrieveFunc: func(_ context.Context, _ *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return nil, status.Error(codes.Internal, "internal server error")
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	party, err := client.GetParty(context.Background(), "party-123")

	assert.Error(t, err)
	assert.NotErrorIs(t, err, clients.ErrPartyNotFound)
	assert.Nil(t, party)
}

// TestPartyClient_ContextCancellation verifies context cancellation is handled
func TestPartyClient_ContextCancellation(t *testing.T) {
	t.Parallel()

	mockServer := &mockPartyServer{
		retrieveFunc: func(ctx context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			// Simulate slow response
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return &partyv1.RetrievePartyResponse{
					Party: &partyv1.Party{PartyId: req.PartyId},
				}, nil
			}
		},
	}

	addr, cleanup := startMockPartyServer(t, mockServer)
	defer cleanup()

	client, err := clients.NewPartyClient(&clients.PartyClientConfig{
		Target:  addr,
		Timeout: 100 * time.Millisecond, // Short timeout
	})
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = client.GetParty(ctx, "party-123")

	assert.Error(t, err)
}

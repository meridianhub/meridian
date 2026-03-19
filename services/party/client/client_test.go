package client

import (
	"context"
	"net"
	"testing"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestNew_WithTarget(t *testing.T) {
	client, cleanup, err := New(Config{
		Target:  "localhost:50055",
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.party)
	assert.Equal(t, 10*time.Second, client.timeout)

	// Test cleanup doesn't panic
	cleanup()
}

func TestNew_WithServiceName(t *testing.T) {
	client, cleanup, err := New(Config{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.party)
	assert.Equal(t, DefaultTimeout, client.timeout)

	cleanup()
}

func TestNew_Defaults(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50055",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Check defaults are applied
	assert.Equal(t, DefaultTimeout, client.timeout)
}

func TestNew_RequiresTargetOrServiceName(t *testing.T) {
	_, _, err := New(Config{})
	assert.ErrorIs(t, err, ErrTargetRequired)
}

func TestNew_DefaultsApplied(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantPort int
	}{
		{
			name:     "empty port defaults to 50055",
			cfg:      Config{ServiceName: "party"},
			wantPort: DefaultPort,
		},
		{
			name:     "custom port preserved",
			cfg:      Config{ServiceName: "party", Port: 9999},
			wantPort: 9999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup, err := New(tt.cfg)
			require.NoError(t, err)
			defer cleanup()
			require.NotNil(t, client)
		})
	}
}

func TestClose(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50055",
	})
	require.NoError(t, err)
	defer cleanup()

	// Close should not error on a valid connection
	err = client.Close()
	assert.NoError(t, err)
}

func TestClose_NilConn(t *testing.T) {
	client := &Client{}
	err := client.Close()
	assert.NoError(t, err)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, 50055, DefaultPort)
	assert.Equal(t, 30*time.Second, DefaultTimeout)
	assert.Equal(t, "default", DefaultNamespace)
	assert.Equal(t, "party", ServiceName)
}

func TestNew_WithResilience(t *testing.T) {
	resilienceConfig := clients.DefaultResilientClientConfig("party-client")
	client, cleanup, err := New(Config{
		Target:     "localhost:50055",
		Resilience: &resilienceConfig,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was created
	assert.NotNil(t, client.resilient)
}

func TestNew_WithoutResilience(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50055",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was not created
	assert.Nil(t, client.resilient)
}

// --- bufconn test infrastructure ---

const bufSize = 1024 * 1024

// fakePartyServer implements PartyServiceServer for testing.
type fakePartyServer struct {
	partyv1.UnimplementedPartyServiceServer

	registerFn       func(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error)
	retrieveFn       func(ctx context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error)
	getPaymentFn     func(ctx context.Context, req *partyv1.GetDefaultPaymentMethodRequest) (*partyv1.GetDefaultPaymentMethodResponse, error)
	listPartFn       func(ctx context.Context, req *partyv1.ListParticipantsRequest) (*partyv1.ListParticipantsResponse, error)
	getStructuringFn func(ctx context.Context, req *partyv1.GetStructuringDataRequest) (*partyv1.GetStructuringDataResponse, error)
}

func (s *fakePartyServer) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
	if s.registerFn != nil {
		return s.registerFn(ctx, req)
	}
	return &partyv1.RegisterPartyResponse{}, nil
}

func (s *fakePartyServer) RetrieveParty(ctx context.Context, req *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
	if s.retrieveFn != nil {
		return s.retrieveFn(ctx, req)
	}
	return &partyv1.RetrievePartyResponse{}, nil
}

func (s *fakePartyServer) GetDefaultPaymentMethod(ctx context.Context, req *partyv1.GetDefaultPaymentMethodRequest) (*partyv1.GetDefaultPaymentMethodResponse, error) {
	if s.getPaymentFn != nil {
		return s.getPaymentFn(ctx, req)
	}
	return &partyv1.GetDefaultPaymentMethodResponse{}, nil
}

func (s *fakePartyServer) ListParticipants(ctx context.Context, req *partyv1.ListParticipantsRequest) (*partyv1.ListParticipantsResponse, error) {
	if s.listPartFn != nil {
		return s.listPartFn(ctx, req)
	}
	return &partyv1.ListParticipantsResponse{}, nil
}

func (s *fakePartyServer) GetStructuringData(ctx context.Context, req *partyv1.GetStructuringDataRequest) (*partyv1.GetStructuringDataResponse, error) {
	if s.getStructuringFn != nil {
		return s.getStructuringFn(ctx, req)
	}
	return &partyv1.GetStructuringDataResponse{}, nil
}

func setupTestServer(t *testing.T, server *fakePartyServer) (*Client, func()) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	partyv1.RegisterPartyServiceServer(srv, server)

	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	c := &Client{
		conn:    conn,
		party:   partyv1.NewPartyServiceClient(conn),
		timeout: DefaultTimeout,
	}

	cleanup := func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	}

	return c, cleanup
}

func setupTestServerWithResilience(t *testing.T, server *fakePartyServer) (*Client, func()) {
	t.Helper()

	c, cleanup := setupTestServer(t, server)
	resilienceConfig := clients.DefaultResilientClientConfig("party-test")
	c.resilient = clients.NewResilientClient(resilienceConfig)

	return c, cleanup
}

// --- RegisterParty ---

func TestClient_RegisterParty_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	resp, err := c.RegisterParty(context.Background(), &partyv1.RegisterPartyRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestClient_RegisterParty_Error(t *testing.T) {
	server := &fakePartyServer{
		registerFn: func(_ context.Context, _ *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "missing required fields")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.RegisterParty(context.Background(), &partyv1.RegisterPartyRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to register party")
}

func TestClient_RegisterParty_WithResilience(t *testing.T) {
	server := &fakePartyServer{
		registerFn: func(_ context.Context, _ *partyv1.RegisterPartyRequest) (*partyv1.RegisterPartyResponse, error) {
			return nil, status.Error(codes.Unavailable, "service down")
		},
	}
	c, cleanup := setupTestServerWithResilience(t, server)
	defer cleanup()

	// With resilience configured, delegates to ExecuteWithResilienceNoRetry (non-idempotent)
	_, err := c.RegisterParty(context.Background(), &partyv1.RegisterPartyRequest{})
	require.Error(t, err)
	// Error comes from resilience wrapper, not the "failed to register party" wrapper
	assert.NotContains(t, err.Error(), "failed to register party")
}

// --- RetrieveParty ---

func TestClient_RetrieveParty_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	resp, err := c.RetrieveParty(context.Background(), &partyv1.RetrievePartyRequest{
		PartyId: "party-1",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestClient_RetrieveParty_Error(t *testing.T) {
	server := &fakePartyServer{
		retrieveFn: func(_ context.Context, _ *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return nil, status.Error(codes.NotFound, "party not found")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.RetrieveParty(context.Background(), &partyv1.RetrievePartyRequest{
		PartyId: "nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to retrieve party")
}

func TestClient_RetrieveParty_WithResilience(t *testing.T) {
	server := &fakePartyServer{
		retrieveFn: func(_ context.Context, _ *partyv1.RetrievePartyRequest) (*partyv1.RetrievePartyResponse, error) {
			return nil, status.Error(codes.Unavailable, "service down")
		},
	}
	c, cleanup := setupTestServerWithResilience(t, server)
	defer cleanup()

	// With resilience configured, delegates to ExecuteWithResilience (idempotent read)
	_, err := c.RetrieveParty(context.Background(), &partyv1.RetrievePartyRequest{
		PartyId: "party-1",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "failed to retrieve party")
}

// --- GetDefaultPaymentMethod ---

func TestClient_GetDefaultPaymentMethod_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	resp, err := c.GetDefaultPaymentMethod(context.Background(), &partyv1.GetDefaultPaymentMethodRequest{
		PartyId: "party-1",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestClient_GetDefaultPaymentMethod_Error(t *testing.T) {
	server := &fakePartyServer{
		getPaymentFn: func(_ context.Context, _ *partyv1.GetDefaultPaymentMethodRequest) (*partyv1.GetDefaultPaymentMethodResponse, error) {
			return nil, status.Error(codes.NotFound, "no payment method")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.GetDefaultPaymentMethod(context.Background(), &partyv1.GetDefaultPaymentMethodRequest{
		PartyId: "party-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get default payment method")
}

func TestClient_GetDefaultPaymentMethod_WithResilience(t *testing.T) {
	server := &fakePartyServer{
		getPaymentFn: func(_ context.Context, _ *partyv1.GetDefaultPaymentMethodRequest) (*partyv1.GetDefaultPaymentMethodResponse, error) {
			return nil, status.Error(codes.Unavailable, "service down")
		},
	}
	c, cleanup := setupTestServerWithResilience(t, server)
	defer cleanup()

	_, err := c.GetDefaultPaymentMethod(context.Background(), &partyv1.GetDefaultPaymentMethodRequest{
		PartyId: "party-1",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "failed to get default payment method")
}

// --- ListParticipants ---

func TestClient_ListParticipants_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	resp, err := c.ListParticipants(context.Background(), &partyv1.ListParticipantsRequest{
		OrgPartyId: "org-1",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestClient_ListParticipants_Error(t *testing.T) {
	server := &fakePartyServer{
		listPartFn: func(_ context.Context, _ *partyv1.ListParticipantsRequest) (*partyv1.ListParticipantsResponse, error) {
			return nil, status.Error(codes.Internal, "database error")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.ListParticipants(context.Background(), &partyv1.ListParticipantsRequest{
		OrgPartyId: "org-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list participants")
}

func TestClient_ListParticipants_WithResilience(t *testing.T) {
	server := &fakePartyServer{
		listPartFn: func(_ context.Context, _ *partyv1.ListParticipantsRequest) (*partyv1.ListParticipantsResponse, error) {
			return nil, status.Error(codes.Unavailable, "service down")
		},
	}
	c, cleanup := setupTestServerWithResilience(t, server)
	defer cleanup()

	_, err := c.ListParticipants(context.Background(), &partyv1.ListParticipantsRequest{
		OrgPartyId: "org-1",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "failed to list participants")
}

// --- GetStructuringData ---

func TestClient_GetStructuringData_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	resp, err := c.GetStructuringData(context.Background(), &partyv1.GetStructuringDataRequest{
		OrgPartyId: "org-1",
		PartyId:    "part-1",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestClient_GetStructuringData_Error(t *testing.T) {
	server := &fakePartyServer{
		getStructuringFn: func(_ context.Context, _ *partyv1.GetStructuringDataRequest) (*partyv1.GetStructuringDataResponse, error) {
			return nil, status.Error(codes.NotFound, "structuring data not found")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.GetStructuringData(context.Background(), &partyv1.GetStructuringDataRequest{
		OrgPartyId: "org-1",
		PartyId:    "part-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get structuring data")
}

func TestClient_GetStructuringData_WithResilience(t *testing.T) {
	server := &fakePartyServer{
		getStructuringFn: func(_ context.Context, _ *partyv1.GetStructuringDataRequest) (*partyv1.GetStructuringDataResponse, error) {
			return nil, status.Error(codes.Unavailable, "service down")
		},
	}
	c, cleanup := setupTestServerWithResilience(t, server)
	defer cleanup()

	_, err := c.GetStructuringData(context.Background(), &partyv1.GetStructuringDataRequest{
		OrgPartyId: "org-1",
		PartyId:    "part-1",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "failed to get structuring data")
}

// --- Conn ---

func TestClient_Conn_ReturnsConnection(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	conn := c.Conn()
	assert.NotNil(t, conn)
	assert.Equal(t, c.conn, conn)
}

func TestClient_Conn_NilConnection(t *testing.T) {
	c := &Client{}
	conn := c.Conn()
	assert.Nil(t, conn)
}

// --- Close (via bufconn) ---

func TestClient_Close_ViaServer(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	err := c.Close()
	require.NoError(t, err)
}

func TestClient_Close_DoubleClose(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakePartyServer{})
	defer cleanup()

	err := c.Close()
	require.NoError(t, err)

	// Second close on an already-closed connection returns an error wrapped with context
	err = c.Close()
	if err != nil {
		assert.Contains(t, err.Error(), "failed to close party client connection")
	}
}

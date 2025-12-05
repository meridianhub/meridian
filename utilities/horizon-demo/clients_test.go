package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"testing"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
)

// mockCurrentAccountServer implements the CurrentAccountService for testing.
type mockCurrentAccountServer struct {
	currentaccountv1.UnimplementedCurrentAccountServiceServer
}

func (m *mockCurrentAccountServer) InitiateCurrentAccount(
	_ context.Context,
	_ *currentaccountv1.InitiateCurrentAccountRequest,
) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
	return &currentaccountv1.InitiateCurrentAccountResponse{
		AccountId: "test-account-id",
	}, nil
}

// mockPaymentOrderServer implements the PaymentOrderService for testing.
type mockPaymentOrderServer struct {
	paymentorderv1.UnimplementedPaymentOrderServiceServer
}

func (m *mockPaymentOrderServer) RetrievePaymentOrder(
	_ context.Context,
	req *paymentorderv1.RetrievePaymentOrderRequest,
) (*paymentorderv1.RetrievePaymentOrderResponse, error) {
	return &paymentorderv1.RetrievePaymentOrderResponse{
		PaymentOrder: &paymentorderv1.PaymentOrder{
			PaymentOrderId: req.PaymentOrderId,
		},
	}, nil
}

// testListener creates a listener for testing, using context-aware net.ListenConfig.
func testListener(ctx context.Context, t *testing.T) net.Listener {
	t.Helper()
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return listener
}

func TestNewClients_ConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *ClientsConfig
		wantError error
	}{
		{
			name:      "nil config",
			cfg:       nil,
			wantError: ErrTargetRequired,
		},
		{
			name: "missing CurrentAccountTarget",
			cfg: &ClientsConfig{
				CurrentAccountTarget: "",
				PaymentOrderTarget:   "localhost:50054",
			},
			wantError: ErrTargetRequired,
		},
		{
			name: "missing PaymentOrderTarget",
			cfg: &ClientsConfig{
				CurrentAccountTarget: "localhost:50051",
				PaymentOrderTarget:   "",
			},
			wantError: ErrTargetRequired,
		},
		{
			name: "both targets missing",
			cfg: &ClientsConfig{
				CurrentAccountTarget: "",
				PaymentOrderTarget:   "",
			},
			wantError: ErrTargetRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clients, err := NewClients(tt.cfg)
			assert.Nil(t, clients)
			require.Error(t, err)
			assert.True(t, errors.Is(err, tt.wantError), "expected error %v, got %v", tt.wantError, err)
		})
	}
}

func TestNewClients_ValidConfig(t *testing.T) {
	ctx := context.Background()

	// Start mock servers
	caListener := testListener(ctx, t)
	t.Cleanup(func() { _ = caListener.Close() })

	poListener := testListener(ctx, t)
	t.Cleanup(func() { _ = poListener.Close() })

	// Start CurrentAccountService mock
	caServer := grpc.NewServer()
	currentaccountv1.RegisterCurrentAccountServiceServer(caServer, &mockCurrentAccountServer{})
	go func() {
		_ = caServer.Serve(caListener)
	}()
	defer caServer.Stop()

	// Start PaymentOrderService mock
	poServer := grpc.NewServer()
	paymentorderv1.RegisterPaymentOrderServiceServer(poServer, &mockPaymentOrderServer{})
	go func() {
		_ = poServer.Serve(poListener)
	}()
	defer poServer.Stop()

	// Create clients
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &ClientsConfig{
		CurrentAccountTarget: caListener.Addr().String(),
		PaymentOrderTarget:   poListener.Addr().String(),
		Logger:               logger,
	}

	clients, err := NewClients(cfg)
	require.NoError(t, err)
	require.NotNil(t, clients)
	defer func() {
		_ = clients.Close()
	}()

	// Verify clients are created
	assert.NotNil(t, clients.CurrentAccount)
	assert.NotNil(t, clients.PaymentOrder)
}

func TestClients_Close(t *testing.T) {
	ctx := context.Background()

	// Start mock servers
	caListener := testListener(ctx, t)
	t.Cleanup(func() { _ = caListener.Close() })

	poListener := testListener(ctx, t)
	t.Cleanup(func() { _ = poListener.Close() })

	caServer := grpc.NewServer()
	currentaccountv1.RegisterCurrentAccountServiceServer(caServer, &mockCurrentAccountServer{})
	go func() {
		_ = caServer.Serve(caListener)
	}()
	defer caServer.Stop()

	poServer := grpc.NewServer()
	paymentorderv1.RegisterPaymentOrderServiceServer(poServer, &mockPaymentOrderServer{})
	go func() {
		_ = poServer.Serve(poListener)
	}()
	defer poServer.Stop()

	cfg := &ClientsConfig{
		CurrentAccountTarget: caListener.Addr().String(),
		PaymentOrderTarget:   poListener.Addr().String(),
	}

	clients, err := NewClients(cfg)
	require.NoError(t, err)
	require.NotNil(t, clients)

	// Close should succeed
	err = clients.Close()
	assert.NoError(t, err)

	// Connection state should be shutdown after close
	assert.Equal(t, connectivity.Shutdown, clients.CurrentAccountState())
	assert.Equal(t, connectivity.Shutdown, clients.PaymentOrderState())
}

func TestClients_CheckHealth(t *testing.T) {
	ctx := context.Background()

	// Start mock servers
	caListener := testListener(ctx, t)
	t.Cleanup(func() { _ = caListener.Close() })

	poListener := testListener(ctx, t)
	t.Cleanup(func() { _ = poListener.Close() })

	caServer := grpc.NewServer()
	currentaccountv1.RegisterCurrentAccountServiceServer(caServer, &mockCurrentAccountServer{})
	go func() {
		_ = caServer.Serve(caListener)
	}()
	defer caServer.Stop()

	poServer := grpc.NewServer()
	paymentorderv1.RegisterPaymentOrderServiceServer(poServer, &mockPaymentOrderServer{})
	go func() {
		_ = poServer.Serve(poListener)
	}()
	defer poServer.Stop()

	cfg := &ClientsConfig{
		CurrentAccountTarget: caListener.Addr().String(),
		PaymentOrderTarget:   poListener.Addr().String(),
	}

	clients, err := NewClients(cfg)
	require.NoError(t, err)
	defer func() {
		_ = clients.Close()
	}()

	// Health check should pass initially (connections are in IDLE state)
	err = clients.CheckHealth(ctx)
	assert.NoError(t, err)
}

func TestClients_CheckHealth_AfterClose(t *testing.T) {
	ctx := context.Background()

	// Start mock servers
	caListener := testListener(ctx, t)
	t.Cleanup(func() { _ = caListener.Close() })

	poListener := testListener(ctx, t)
	t.Cleanup(func() { _ = poListener.Close() })

	caServer := grpc.NewServer()
	currentaccountv1.RegisterCurrentAccountServiceServer(caServer, &mockCurrentAccountServer{})
	go func() {
		_ = caServer.Serve(caListener)
	}()
	defer caServer.Stop()

	poServer := grpc.NewServer()
	paymentorderv1.RegisterPaymentOrderServiceServer(poServer, &mockPaymentOrderServer{})
	go func() {
		_ = poServer.Serve(poListener)
	}()
	defer poServer.Stop()

	cfg := &ClientsConfig{
		CurrentAccountTarget: caListener.Addr().String(),
		PaymentOrderTarget:   poListener.Addr().String(),
	}

	clients, err := NewClients(cfg)
	require.NoError(t, err)

	// Close connections
	err = clients.Close()
	require.NoError(t, err)

	// Health check should fail after close
	err = clients.CheckHealth(ctx)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrServiceUnreachable))
}

func TestContextWithCorrelationID(t *testing.T) {
	tests := []struct {
		name          string
		correlationID string
		wantInMeta    bool
	}{
		{
			name:          "with correlation ID",
			correlationID: "test-correlation-123",
			wantInMeta:    true,
		},
		{
			name:          "empty correlation ID",
			correlationID: "",
			wantInMeta:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			newCtx := ContextWithCorrelationID(ctx, tt.correlationID)

			md, ok := metadata.FromOutgoingContext(newCtx)
			if tt.wantInMeta {
				require.True(t, ok, "expected metadata in context")
				vals := md.Get("x-correlation-id")
				require.Len(t, vals, 1)
				assert.Equal(t, tt.correlationID, vals[0])
			} else {
				// Either no metadata or no correlation ID header
				if ok {
					vals := md.Get("x-correlation-id")
					assert.Empty(t, vals)
				}
			}
		})
	}
}

func TestContextWithCorrelationID_PreservesExistingMetadata(t *testing.T) {
	ctx := context.Background()

	// Add existing metadata
	existingMD := metadata.Pairs("x-request-id", "existing-request")
	ctx = metadata.NewOutgoingContext(ctx, existingMD)

	// Add correlation ID
	newCtx := ContextWithCorrelationID(ctx, "new-correlation-id")

	// Verify both headers are present
	md, ok := metadata.FromOutgoingContext(newCtx)
	require.True(t, ok)

	requestIDs := md.Get("x-request-id")
	require.Len(t, requestIDs, 1)
	assert.Equal(t, "existing-request", requestIDs[0])

	correlationIDs := md.Get("x-correlation-id")
	require.Len(t, correlationIDs, 1)
	assert.Equal(t, "new-correlation-id", correlationIDs[0])
}

func TestExtractCorrelationID(t *testing.T) {
	tests := []struct {
		name     string
		setupCtx func() context.Context
		wantID   string
	}{
		{
			name: "from incoming metadata",
			setupCtx: func() context.Context {
				md := metadata.Pairs("x-correlation-id", "meta-correlation-789")
				return metadata.NewIncomingContext(context.Background(), md)
			},
			wantID: "meta-correlation-789",
		},
		{
			name: "from x-request-id in metadata",
			setupCtx: func() context.Context {
				md := metadata.Pairs("x-request-id", "meta-request-abc")
				return metadata.NewIncomingContext(context.Background(), md)
			},
			wantID: "meta-request-abc",
		},
		{
			name: "empty when no correlation ID",
			setupCtx: func() context.Context {
				return context.Background()
			},
			wantID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupCtx()
			got := ExtractCorrelationID(ctx)
			assert.Equal(t, tt.wantID, got)
		})
	}
}

func TestClients_ConnectionStates(t *testing.T) {
	ctx := context.Background()

	// Start mock servers
	caListener := testListener(ctx, t)
	t.Cleanup(func() { _ = caListener.Close() })

	poListener := testListener(ctx, t)
	t.Cleanup(func() { _ = poListener.Close() })

	caServer := grpc.NewServer()
	currentaccountv1.RegisterCurrentAccountServiceServer(caServer, &mockCurrentAccountServer{})
	go func() {
		_ = caServer.Serve(caListener)
	}()
	defer caServer.Stop()

	poServer := grpc.NewServer()
	paymentorderv1.RegisterPaymentOrderServiceServer(poServer, &mockPaymentOrderServer{})
	go func() {
		_ = poServer.Serve(poListener)
	}()
	defer poServer.Stop()

	cfg := &ClientsConfig{
		CurrentAccountTarget: caListener.Addr().String(),
		PaymentOrderTarget:   poListener.Addr().String(),
	}

	clients, err := NewClients(cfg)
	require.NoError(t, err)
	defer func() {
		_ = clients.Close()
	}()

	// Initially connections are IDLE (grpc.NewClient doesn't connect immediately)
	caState := clients.CurrentAccountState()
	poState := clients.PaymentOrderState()

	// States should be valid (not shutdown)
	assert.NotEqual(t, connectivity.Shutdown, caState)
	assert.NotEqual(t, connectivity.Shutdown, poState)
}

func TestDefaultPorts(t *testing.T) {
	assert.Equal(t, 50051, CurrentAccountPort)
	assert.Equal(t, 50054, PaymentOrderPort)
}

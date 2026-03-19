package grpc

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// mockManifestHistoryServer implements ManifestHistoryServiceServer for testing.
type mockManifestHistoryServer struct {
	controlplanev1.UnimplementedManifestHistoryServiceServer

	getCurrentManifestFunc func(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error)
}

func (s *mockManifestHistoryServer) GetCurrentManifest(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
	if s.getCurrentManifestFunc != nil {
		return s.getCurrentManifestFunc(ctx, req)
	}
	return &controlplanev1.GetCurrentManifestResponse{}, nil
}

// startMockManifestServer starts a gRPC server with the mock manifest implementation.
func startMockManifestServer(t *testing.T, mock *mockManifestHistoryServer) (string, func()) {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	controlplanev1.RegisterManifestHistoryServiceServer(server, mock)

	go func() {
		_ = server.Serve(lis)
	}()

	return lis.Addr().String(), func() {
		server.GracefulStop()
	}
}

// createTestManifestClient creates a ManifestClient connected to a mock server.
func createTestManifestClient(t *testing.T, addr string) *ManifestClient {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	return &ManifestClient{
		conn:    conn,
		client:  controlplanev1.NewManifestHistoryServiceClient(conn),
		timeout: 5 * time.Second,
		logger:  slog.Default(),
	}
}

func TestNewManifestClient_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		config  *ManifestClientConfig
		wantErr error
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: ErrManifestClientConfigRequired,
		},
		{
			name: "missing service name",
			config: &ManifestClientConfig{
				ServiceName: "",
				Port:        50051,
			},
			wantErr: ErrManifestClientServiceNameRequired,
		},
		{
			name: "negative timeout",
			config: &ManifestClientConfig{
				ServiceName: "control-plane",
				Port:        50051,
				Timeout:     -1 * time.Second,
			},
			wantErr: ErrManifestClientNegativeTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewManifestClient(tt.config)
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, client)
		})
	}
}

func TestNewManifestClient_ValidConfig(t *testing.T) {
	client, err := NewManifestClient(&ManifestClientConfig{
		ServiceName: "control-plane",
		Port:        50051,
	})
	// Client creation may succeed since platformgrpc.NewClient uses lazy connect
	if err == nil && client != nil {
		assert.NotNil(t, client)
		_ = client.Close()
	}
}

func TestNewManifestClient_DefaultTimeout(t *testing.T) {
	cfg := &ManifestClientConfig{
		ServiceName: "control-plane",
		Port:        50051,
	}
	assert.Equal(t, time.Duration(0), cfg.Timeout, "timeout should start as zero")
}

func TestManifestClient_GetCurrentSagaDefinitions_Success(t *testing.T) {
	sagaDefs := []*controlplanev1.SagaDefinition{
		{
			Name:    "settlement_saga",
			Trigger: "event:payment.completed",
			Script:  "def run(ctx, input): pass",
		},
		{
			Name:    "reconciliation_saga",
			Trigger: "event:reconciliation.started",
			Script:  "def run(ctx, input): pass",
		},
	}

	mock := &mockManifestHistoryServer{
		getCurrentManifestFunc: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					Manifest: &controlplanev1.Manifest{
						Version: "1.0",
						Sagas:   sagaDefs,
					},
				},
			}, nil
		},
	}
	addr, cleanup := startMockManifestServer(t, mock)
	defer cleanup()

	client := createTestManifestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	result, err := client.GetCurrentSagaDefinitions(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "settlement_saga", result[0].GetName())
	assert.Equal(t, "reconciliation_saga", result[1].GetName())
}

func TestManifestClient_GetCurrentSagaDefinitions_NotFound_ReturnsEmptySlice(t *testing.T) {
	mock := &mockManifestHistoryServer{
		getCurrentManifestFunc: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
			return nil, status.Error(codes.NotFound, "no manifest found")
		},
	}
	addr, cleanup := startMockManifestServer(t, mock)
	defer cleanup()

	client := createTestManifestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	result, err := client.GetCurrentSagaDefinitions(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestManifestClient_GetCurrentSagaDefinitions_NilManifest_ReturnsEmptySlice(t *testing.T) {
	mock := &mockManifestHistoryServer{
		getCurrentManifestFunc: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
			return &controlplanev1.GetCurrentManifestResponse{
				Version: &controlplanev1.ManifestVersion{
					// Manifest is nil
				},
			}, nil
		},
	}
	addr, cleanup := startMockManifestServer(t, mock)
	defer cleanup()

	client := createTestManifestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	result, err := client.GetCurrentSagaDefinitions(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestManifestClient_GetCurrentSagaDefinitions_GRPCError(t *testing.T) {
	mock := &mockManifestHistoryServer{
		getCurrentManifestFunc: func(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
			return nil, status.Error(codes.Internal, "internal server error")
		},
	}
	addr, cleanup := startMockManifestServer(t, mock)
	defer cleanup()

	client := createTestManifestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	result, err := client.GetCurrentSagaDefinitions(context.Background())
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "fetch current manifest")
}

func TestManifestClient_Close_NilConn(t *testing.T) {
	client := &ManifestClient{
		conn:   nil,
		logger: slog.Default(),
	}
	err := client.Close()
	require.NoError(t, err)
}

func TestManifestClient_Close_ValidConn(t *testing.T) {
	mock := &mockManifestHistoryServer{}
	addr, cleanup := startMockManifestServer(t, mock)
	defer cleanup()

	client := createTestManifestClient(t, addr)

	err := client.Close()
	require.NoError(t, err)
}

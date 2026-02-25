package auth_test

import (
	"context"
	"net"
	"testing"

	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
)

// TestLoadFromEnv_MissingKey verifies ErrMissingAPIKey is returned when the
// env var is absent.
func TestLoadFromEnv_MissingKey(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "")

	_, err := auth.LoadFromEnv()
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrMissingAPIKey)
}

// TestLoadFromEnv_WithKey verifies successful loading when the env var is set.
func TestLoadFromEnv_WithKey(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "test-key-123")
	t.Setenv(auth.EnvAPIURL, "gateway:443")

	cfg, err := auth.LoadFromEnv()
	require.NoError(t, err)
	assert.Equal(t, "test-key-123", cfg.APIKey)
	assert.Equal(t, "gateway:443", cfg.APIUrl)
}

// TestLoadFromEnv_WhitespaceKey verifies that a whitespace-only key is treated as missing.
func TestLoadFromEnv_WhitespaceKey(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "   ")

	_, err := auth.LoadFromEnv()
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrMissingAPIKey)
}

// TestLoadFromEnv_NoURL verifies that the URL field is optional.
func TestLoadFromEnv_NoURL(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "my-key")
	t.Setenv(auth.EnvAPIURL, "")

	cfg, err := auth.LoadFromEnv()
	require.NoError(t, err)
	assert.Equal(t, "my-key", cfg.APIKey)
	assert.Equal(t, "", cfg.APIUrl)
}

// capturedMetadataServer captures the incoming metadata from gRPC calls for
// inspection in tests.
type capturedMetadataServer struct {
	grpc_testing.UnimplementedTestServiceServer
	captured metadata.MD
}

// EmptyCall captures metadata on an empty call.
func (s *capturedMetadataServer) EmptyCall(ctx context.Context, _ *grpc_testing.Empty) (*grpc_testing.Empty, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.captured = md.Copy()
	return &grpc_testing.Empty{}, nil
}

// listenTCP creates a TCP listener on a random local port using ListenConfig
// to satisfy the noctx linter requirement.
func listenTCP(t *testing.T) net.Listener {
	t.Helper()
	lc := net.ListenConfig{}
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return lis
}

// TestUnaryInterceptor_InjectsAuthorizationHeader verifies that the interceptor
// adds the Bearer token to outgoing gRPC metadata.
func TestUnaryInterceptor_InjectsAuthorizationHeader(t *testing.T) {
	apiKey := "super-secret-key"

	lis := listenTCP(t)

	captured := &capturedMetadataServer{}
	srv := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(srv, captured)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	cfg := &auth.Config{APIKey: apiKey}
	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(cfg.UnaryInterceptor()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := grpc_testing.NewTestServiceClient(conn)
	_, err = client.EmptyCall(context.Background(), &grpc_testing.Empty{})
	require.NoError(t, err)

	authVals := captured.captured.Get("authorization")
	require.Len(t, authVals, 1, "expected exactly one authorization header")
	assert.Equal(t, "Bearer "+apiKey, authVals[0])
}

// TestUnaryInterceptor_ExistingMetadataPreserved verifies that pre-existing
// outgoing metadata is preserved alongside the injected Authorization header.
func TestUnaryInterceptor_ExistingMetadataPreserved(t *testing.T) {
	apiKey := "key-abc"

	lis := listenTCP(t)

	captured := &capturedMetadataServer{}
	srv := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(srv, captured)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	cfg := &auth.Config{APIKey: apiKey}
	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(cfg.UnaryInterceptor()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-request-id", "req-42")

	client := grpc_testing.NewTestServiceClient(conn)
	_, err = client.EmptyCall(ctx, &grpc_testing.Empty{})
	require.NoError(t, err)

	authVals := captured.captured.Get("authorization")
	require.Len(t, authVals, 1)
	assert.Equal(t, "Bearer "+apiKey, authVals[0])

	reqIDVals := captured.captured.Get("x-request-id")
	require.Len(t, reqIDVals, 1)
	assert.Equal(t, "req-42", reqIDVals[0])
}

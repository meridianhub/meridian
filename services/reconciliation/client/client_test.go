package client_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/client"
)

// mockReconciliationServer implements the AccountReconciliationServiceServer for testing.
type mockReconciliationServer struct {
	reconciliationv1.UnimplementedAccountReconciliationServiceServer

	initiateResp *reconciliationv1.InitiateAccountReconciliationResponse
	initiateErr  error

	retrieveResp *reconciliationv1.RetrieveAccountReconciliationResponse
	retrieveErr  error
}

func (m *mockReconciliationServer) InitiateAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.InitiateAccountReconciliationRequest,
) (*reconciliationv1.InitiateAccountReconciliationResponse, error) {
	if m.initiateErr != nil {
		return nil, m.initiateErr
	}
	return m.initiateResp, nil
}

func (m *mockReconciliationServer) RetrieveAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.RetrieveAccountReconciliationRequest,
) (*reconciliationv1.RetrieveAccountReconciliationResponse, error) {
	if m.retrieveErr != nil {
		return nil, m.retrieveErr
	}
	return m.retrieveResp, nil
}

func setupTestServer(t *testing.T, mock *mockReconciliationServer) (client.Config, func()) {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	reconciliationv1.RegisterAccountReconciliationServiceServer(grpcServer, mock)

	go func() {
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("gRPC server error: %v", err)
		}
	}()

	cleanup := func() {
		grpcServer.GracefulStop()
	}

	return client.Config{
		Target:  lis.Addr().String(),
		Timeout: 5 * time.Second,
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	}, cleanup
}

func TestNew_RequiresTarget(t *testing.T) {
	_, _, err := client.New(client.Config{})
	require.Error(t, err)
	assert.ErrorIs(t, err, client.ErrTargetRequired)
}

func TestNew_DirectConnection(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()
	require.NotNil(t, c)
}

func TestInitiateAccountReconciliation_Unimplemented(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateErr: status.Error(codes.Unimplemented, "not implemented"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.InitiateAccountReconciliation(context.Background(), &reconciliationv1.InitiateAccountReconciliationRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to initiate account reconciliation")
}

func TestRetrieveAccountReconciliation_NotFound(t *testing.T) {
	mock := &mockReconciliationServer{
		retrieveErr: status.Error(codes.NotFound, "run not found"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: "00000000-0000-0000-0000-000000000001",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to retrieve account reconciliation")
}

func TestClose(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, _, err := client.New(cfg)
	require.NoError(t, err)

	err = c.Close()
	assert.NoError(t, err)
}

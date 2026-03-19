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

	executeResp *reconciliationv1.ExecuteAccountReconciliationResponse
	executeErr  error

	retrieveResp *reconciliationv1.RetrieveAccountReconciliationResponse
	retrieveErr  error

	controlResp *reconciliationv1.ControlAccountReconciliationResponse
	controlErr  error

	listResultsResp *reconciliationv1.ListReconciliationResultsResponse
	listResultsErr  error

	assertBalanceResp *reconciliationv1.AssertBalanceResponse
	assertBalanceErr  error

	initiateDisputeResp *reconciliationv1.InitiateDisputeResponse
	initiateDisputeErr  error

	controlDisputeResp *reconciliationv1.ControlDisputeResponse
	controlDisputeErr  error

	retrieveDisputeResp *reconciliationv1.RetrieveDisputeResponse
	retrieveDisputeErr  error
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

func (m *mockReconciliationServer) ExecuteAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.ExecuteAccountReconciliationRequest,
) (*reconciliationv1.ExecuteAccountReconciliationResponse, error) {
	if m.executeErr != nil {
		return nil, m.executeErr
	}
	return m.executeResp, nil
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

func (m *mockReconciliationServer) ControlAccountReconciliation(
	_ context.Context,
	_ *reconciliationv1.ControlAccountReconciliationRequest,
) (*reconciliationv1.ControlAccountReconciliationResponse, error) {
	if m.controlErr != nil {
		return nil, m.controlErr
	}
	return m.controlResp, nil
}

func (m *mockReconciliationServer) ListReconciliationResults(
	_ context.Context,
	_ *reconciliationv1.ListReconciliationResultsRequest,
) (*reconciliationv1.ListReconciliationResultsResponse, error) {
	if m.listResultsErr != nil {
		return nil, m.listResultsErr
	}
	return m.listResultsResp, nil
}

func (m *mockReconciliationServer) AssertBalance(
	_ context.Context,
	_ *reconciliationv1.AssertBalanceRequest,
) (*reconciliationv1.AssertBalanceResponse, error) {
	if m.assertBalanceErr != nil {
		return nil, m.assertBalanceErr
	}
	return m.assertBalanceResp, nil
}

func (m *mockReconciliationServer) InitiateDispute(
	_ context.Context,
	_ *reconciliationv1.InitiateDisputeRequest,
) (*reconciliationv1.InitiateDisputeResponse, error) {
	if m.initiateDisputeErr != nil {
		return nil, m.initiateDisputeErr
	}
	return m.initiateDisputeResp, nil
}

func (m *mockReconciliationServer) ControlDispute(
	_ context.Context,
	_ *reconciliationv1.ControlDisputeRequest,
) (*reconciliationv1.ControlDisputeResponse, error) {
	if m.controlDisputeErr != nil {
		return nil, m.controlDisputeErr
	}
	return m.controlDisputeResp, nil
}

func (m *mockReconciliationServer) RetrieveDispute(
	_ context.Context,
	_ *reconciliationv1.RetrieveDisputeRequest,
) (*reconciliationv1.RetrieveDisputeResponse, error) {
	if m.retrieveDisputeErr != nil {
		return nil, m.retrieveDisputeErr
	}
	return m.retrieveDisputeResp, nil
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

func TestNew_DefaultsApplied(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	// Clear timeout to test default
	cfg.Timeout = 0
	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()
	require.NotNil(t, c)
}

func TestNew_ServiceNameConnection(t *testing.T) {
	// ServiceName path uses platformgrpc.NewClient which may fail without real DNS,
	// but we can test that it doesn't panic and returns an error or success.
	_, _, err := client.New(client.Config{
		ServiceName: "reconciliation",
		Namespace:   "test-ns",
		Port:        50058,
	})
	// This may succeed (creating a lazy connection) or fail depending on resolver
	// The key is it doesn't panic and handles the path
	if err != nil {
		assert.Contains(t, err.Error(), "reconciliation")
	}
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

func TestInitiateAccountReconciliation_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateResp: &reconciliationv1.InitiateAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{
				RunId: "run-001",
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.InitiateAccountReconciliation(context.Background(), &reconciliationv1.InitiateAccountReconciliationRequest{
		AccountId: "ACC-001",
	})
	require.NoError(t, err)
	assert.Equal(t, "run-001", resp.GetRun().GetRunId())
}

func TestExecuteAccountReconciliation_Error(t *testing.T) {
	mock := &mockReconciliationServer{
		executeErr: status.Error(codes.NotFound, "run not found"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ExecuteAccountReconciliation(context.Background(), &reconciliationv1.ExecuteAccountReconciliationRequest{
		RunId: "run-001",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to execute account reconciliation")
}

func TestExecuteAccountReconciliation_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		executeResp: &reconciliationv1.ExecuteAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{
				RunId: "run-001",
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ExecuteAccountReconciliation(context.Background(), &reconciliationv1.ExecuteAccountReconciliationRequest{
		RunId: "run-001",
	})
	require.NoError(t, err)
	assert.Equal(t, "run-001", resp.GetRun().GetRunId())
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

func TestRetrieveAccountReconciliation_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		retrieveResp: &reconciliationv1.RetrieveAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{
				RunId:  "run-001",
				Status: reconciliationv1.RunStatus_RUN_STATUS_COMPLETED,
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.RetrieveAccountReconciliation(context.Background(), &reconciliationv1.RetrieveAccountReconciliationRequest{
		RunId: "run-001",
	})
	require.NoError(t, err)
	assert.Equal(t, "run-001", resp.GetRun().GetRunId())
}

func TestControlAccountReconciliation_Error(t *testing.T) {
	mock := &mockReconciliationServer{
		controlErr: status.Error(codes.FailedPrecondition, "cannot cancel completed run"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  "run-001",
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to control account reconciliation")
}

func TestControlAccountReconciliation_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		controlResp: &reconciliationv1.ControlAccountReconciliationResponse{
			Run: &reconciliationv1.SettlementRunSummary{
				RunId: "run-001",
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ControlAccountReconciliation(context.Background(), &reconciliationv1.ControlAccountReconciliationRequest{
		RunId:  "run-001",
		Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
	})
	require.NoError(t, err)
	assert.Equal(t, "run-001", resp.GetRun().GetRunId())
}

func TestListReconciliationResults_Error(t *testing.T) {
	mock := &mockReconciliationServer{
		listResultsErr: status.Error(codes.NotFound, "run not found"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId: "run-001",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to list reconciliation results")
}

func TestListReconciliationResults_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		listResultsResp: &reconciliationv1.ListReconciliationResultsResponse{
			Variances: []*reconciliationv1.VarianceDetail{
				{VarianceId: "var-001"},
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ListReconciliationResults(context.Background(), &reconciliationv1.ListReconciliationResultsRequest{
		RunId: "run-001",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetVariances(), 1)
	assert.Equal(t, "var-001", resp.GetVariances()[0].GetVarianceId())
}

func TestAssertBalance_Error(t *testing.T) {
	mock := &mockReconciliationServer{
		assertBalanceErr: status.Error(codes.InvalidArgument, "invalid expression"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.AssertBalance(context.Background(), &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "INVALID",
		ExpectedBalance: "100.00",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to assert balance")
}

func TestAssertBalance_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		assertBalanceResp: &reconciliationv1.AssertBalanceResponse{
			Assertion: &reconciliationv1.BalanceAssertionDetail{
				AssertionId: "assert-001",
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.AssertBalance(context.Background(), &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "DEBIT == CREDIT",
		ExpectedBalance: "100.00",
	})
	require.NoError(t, err)
	assert.Equal(t, "assert-001", resp.GetAssertion().GetAssertionId())
}

func TestInitiateDispute_Error(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateDisputeErr: status.Error(codes.NotFound, "variance not found"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.InitiateDispute(context.Background(), &reconciliationv1.InitiateDisputeRequest{
		VarianceId: "var-001",
		RunId:      "run-001",
		AccountId:  "ACC-001",
		Reason:     "incorrect amount",
		RaisedBy:   "user-001",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to initiate dispute")
}

func TestInitiateDispute_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		initiateDisputeResp: &reconciliationv1.InitiateDisputeResponse{
			Dispute: &reconciliationv1.DisputeDetail{
				DisputeId: "disp-001",
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.InitiateDispute(context.Background(), &reconciliationv1.InitiateDisputeRequest{
		VarianceId: "var-001",
		RunId:      "run-001",
		AccountId:  "ACC-001",
		Reason:     "incorrect amount",
		RaisedBy:   "user-001",
	})
	require.NoError(t, err)
	assert.Equal(t, "disp-001", resp.GetDispute().GetDisputeId())
}

func TestControlDispute_Error(t *testing.T) {
	mock := &mockReconciliationServer{
		controlDisputeErr: status.Error(codes.FailedPrecondition, "dispute already resolved"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ControlDispute(context.Background(), &reconciliationv1.ControlDisputeRequest{
		DisputeId: "disp-001",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to control dispute")
}

func TestControlDispute_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		controlDisputeResp: &reconciliationv1.ControlDisputeResponse{
			Dispute: &reconciliationv1.DisputeDetail{
				DisputeId: "disp-001",
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.ControlDispute(context.Background(), &reconciliationv1.ControlDisputeRequest{
		DisputeId: "disp-001",
	})
	require.NoError(t, err)
	assert.Equal(t, "disp-001", resp.GetDispute().GetDisputeId())
}

func TestRetrieveDispute_Error(t *testing.T) {
	mock := &mockReconciliationServer{
		retrieveDisputeErr: status.Error(codes.NotFound, "dispute not found"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.RetrieveDispute(context.Background(), &reconciliationv1.RetrieveDisputeRequest{
		DisputeId: "disp-001",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to retrieve dispute")
}

func TestRetrieveDispute_Success(t *testing.T) {
	mock := &mockReconciliationServer{
		retrieveDisputeResp: &reconciliationv1.RetrieveDisputeResponse{
			Dispute: &reconciliationv1.DisputeDetail{
				DisputeId: "disp-001",
			},
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	resp, err := c.RetrieveDispute(context.Background(), &reconciliationv1.RetrieveDisputeRequest{
		DisputeId: "disp-001",
	})
	require.NoError(t, err)
	assert.Equal(t, "disp-001", resp.GetDispute().GetDisputeId())
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

func TestConn(t *testing.T) {
	mock := &mockReconciliationServer{}
	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer clientCleanup()

	conn := c.Conn()
	assert.NotNil(t, conn)
}

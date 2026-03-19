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

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/client"
)

// fullMockServer extends mockPositionKeepingServer with all RPC methods
type fullMockServer struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer

	initiateResp      *positionkeepingv1.InitiateFinancialPositionLogResponse
	initiateErr       error
	initiateBatchResp *positionkeepingv1.InitiateFinancialPositionLogBatchResponse
	initiateBatchErr  error
	updateResp        *positionkeepingv1.UpdateFinancialPositionLogResponse
	updateErr         error
	retrieveResp      *positionkeepingv1.RetrieveFinancialPositionLogResponse
	retrieveErr       error
	bulkImportResp    *positionkeepingv1.BulkImportTransactionsResponse
	bulkImportErr     error
	listResp          *positionkeepingv1.ListFinancialPositionLogsResponse
	listErr           error
	balanceResp       *positionkeepingv1.GetAccountBalanceResponse
	balanceErr        error
	balancesResp      *positionkeepingv1.GetAccountBalancesResponse
	balancesErr       error
	releaseResp       *positionkeepingv1.ReleaseReservationResponse
	releaseErr        error
}

func (m *fullMockServer) InitiateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return m.initiateResp, m.initiateErr
}

func (m *fullMockServer) InitiateFinancialPositionLogBatch(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogBatchRequest) (*positionkeepingv1.InitiateFinancialPositionLogBatchResponse, error) {
	return m.initiateBatchResp, m.initiateBatchErr
}

func (m *fullMockServer) UpdateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	return m.updateResp, m.updateErr
}

func (m *fullMockServer) RetrieveFinancialPositionLog(_ context.Context, _ *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	return m.retrieveResp, m.retrieveErr
}

func (m *fullMockServer) BulkImportTransactions(_ context.Context, _ *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	return m.bulkImportResp, m.bulkImportErr
}

func (m *fullMockServer) ListFinancialPositionLogs(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	return m.listResp, m.listErr
}

func (m *fullMockServer) GetAccountBalance(_ context.Context, _ *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	return m.balanceResp, m.balanceErr
}

func (m *fullMockServer) GetAccountBalances(_ context.Context, _ *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	return m.balancesResp, m.balancesErr
}

func (m *fullMockServer) ReleaseReservation(_ context.Context, _ *positionkeepingv1.ReleaseReservationRequest) (*positionkeepingv1.ReleaseReservationResponse, error) {
	return m.releaseResp, m.releaseErr
}

func setupFullTestServer(t *testing.T, mock *fullMockServer) (client.Config, func()) {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	positionkeepingv1.RegisterPositionKeepingServiceServer(grpcServer, mock)

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

func TestNew_ErrTargetRequired(t *testing.T) {
	c, cleanup, err := client.New(client.Config{})
	assert.ErrorIs(t, err, client.ErrTargetRequired)
	assert.Nil(t, c)
	assert.Nil(t, cleanup)
}

func TestNew_Defaults(t *testing.T) {
	mock := &fullMockServer{}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	// Verify defaults are applied
	cfg.Timeout = 0    // Should default to 30s
	cfg.Port = 0       // Should default to 50053
	cfg.Namespace = "" // Should default to "default"

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	require.NotNil(t, c)
	defer cleanup()
}

func TestInitiateFinancialPositionLog_Error(t *testing.T) {
	mock := &fullMockServer{
		initiateErr: status.Error(codes.InvalidArgument, "invalid account"),
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.InitiateFinancialPositionLog(context.Background(), &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to initiate financial position log")
}

func TestInitiateFinancialPositionLogBatch_Success(t *testing.T) {
	mock := &fullMockServer{
		initiateBatchResp: &positionkeepingv1.InitiateFinancialPositionLogBatchResponse{
			Results: []*positionkeepingv1.BatchInitiateResult{
				{Log: &positionkeepingv1.FinancialPositionLog{LogId: "log-1", AccountId: "acc-1"}},
			},
			TotalCount:   1,
			SuccessCount: 1,
		},
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.InitiateFinancialPositionLogBatch(context.Background(), &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Results, 1)
}

func TestInitiateFinancialPositionLogBatch_Error(t *testing.T) {
	mock := &fullMockServer{
		initiateBatchErr: status.Error(codes.Internal, "batch failed"),
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.InitiateFinancialPositionLogBatch(context.Background(), &positionkeepingv1.InitiateFinancialPositionLogBatchRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to initiate financial position log batch")
}

func TestUpdateFinancialPositionLog_Error(t *testing.T) {
	mock := &fullMockServer{
		updateErr: status.Error(codes.NotFound, "log not found"),
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.UpdateFinancialPositionLog(context.Background(), &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: "nonexistent",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to update financial position log")
}

func TestRetrieveFinancialPositionLog_Success(t *testing.T) {
	mock := &fullMockServer{
		retrieveResp: &positionkeepingv1.RetrieveFinancialPositionLogResponse{
			Log: &positionkeepingv1.FinancialPositionLog{
				LogId:     "log-1",
				AccountId: "acc-1",
			},
		},
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.RetrieveFinancialPositionLog(context.Background(), &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: "log-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "log-1", resp.Log.LogId)
}

func TestRetrieveFinancialPositionLog_Error(t *testing.T) {
	mock := &fullMockServer{
		retrieveErr: status.Error(codes.NotFound, "not found"),
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.RetrieveFinancialPositionLog(context.Background(), &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: "nonexistent",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to retrieve financial position log")
}

func TestBulkImportTransactions_Success(t *testing.T) {
	mock := &fullMockServer{
		bulkImportResp: &positionkeepingv1.BulkImportTransactionsResponse{},
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.BulkImportTransactions(context.Background(), &positionkeepingv1.BulkImportTransactionsRequest{
		LogId: "log-1",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestBulkImportTransactions_Error(t *testing.T) {
	mock := &fullMockServer{
		bulkImportErr: status.Error(codes.Internal, "import failed"),
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.BulkImportTransactions(context.Background(), &positionkeepingv1.BulkImportTransactionsRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to bulk import transactions")
}

func TestListFinancialPositionLogs_Success(t *testing.T) {
	mock := &fullMockServer{
		listResp: &positionkeepingv1.ListFinancialPositionLogsResponse{
			Logs: []*positionkeepingv1.FinancialPositionLog{
				{LogId: "log-1"},
				{LogId: "log-2"},
			},
		},
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.ListFinancialPositionLogs(context.Background(), &positionkeepingv1.ListFinancialPositionLogsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Logs, 2)
}

func TestListFinancialPositionLogs_Error(t *testing.T) {
	mock := &fullMockServer{
		listErr: status.Error(codes.Internal, "list failed"),
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.ListFinancialPositionLogs(context.Background(), &positionkeepingv1.ListFinancialPositionLogsRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to list financial position logs")
}

func TestReleaseReservation_Success(t *testing.T) {
	mock := &fullMockServer{
		releaseResp: &positionkeepingv1.ReleaseReservationResponse{},
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.ReleaseReservation(context.Background(), &positionkeepingv1.ReleaseReservationRequest{
		LienId: "lien-1",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestReleaseReservation_Error(t *testing.T) {
	mock := &fullMockServer{
		releaseErr: status.Error(codes.NotFound, "reservation not found"),
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.ReleaseReservation(context.Background(), &positionkeepingv1.ReleaseReservationRequest{
		LienId: "nonexistent",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to release reservation")
}

func TestClient_Close(t *testing.T) {
	mock := &fullMockServer{}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, _, err := client.New(cfg)
	require.NoError(t, err)

	err = c.Close()
	assert.NoError(t, err)
}

func TestClient_Conn(t *testing.T) {
	mock := &fullMockServer{}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	conn := c.Conn()
	assert.NotNil(t, conn)
}

func TestInitiateFinancialPositionLog_Success(t *testing.T) {
	mock := &fullMockServer{
		initiateResp: &positionkeepingv1.InitiateFinancialPositionLogResponse{
			Log: &positionkeepingv1.FinancialPositionLog{
				LogId:     "log-1",
				AccountId: "acc-1",
			},
		},
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.InitiateFinancialPositionLog(context.Background(), &positionkeepingv1.InitiateFinancialPositionLogRequest{
		AccountId: "acc-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "log-1", resp.Log.LogId)
}

func TestUpdateFinancialPositionLog_Success(t *testing.T) {
	mock := &fullMockServer{
		updateResp: &positionkeepingv1.UpdateFinancialPositionLogResponse{
			Log: &positionkeepingv1.FinancialPositionLog{
				LogId:     "log-1",
				AccountId: "acc-1",
			},
		},
	}
	cfg, serverCleanup := setupFullTestServer(t, mock)
	defer serverCleanup()

	c, cleanup, err := client.New(cfg)
	require.NoError(t, err)
	defer cleanup()

	resp, err := c.UpdateFinancialPositionLog(context.Background(), &positionkeepingv1.UpdateFinancialPositionLogRequest{
		LogId: "log-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "log-1", resp.Log.LogId)
}

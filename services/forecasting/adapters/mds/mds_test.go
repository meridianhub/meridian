package mds

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	misclient "github.com/meridianhub/meridian/services/market-information/client"
)

const bufSize = 1024 * 1024

// --- Mock gRPC Server ---

// fakeMISServer implements the MarketInformationService for testing the MDS adapter.
type fakeMISServer struct {
	marketinformationv1.UnimplementedMarketInformationServiceServer

	listObservationsFn     func(ctx context.Context, req *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error)
	recordObservationBatchFn func(ctx context.Context, req *marketinformationv1.RecordObservationBatchRequest) (*marketinformationv1.RecordObservationBatchResponse, error)
}

func (f *fakeMISServer) ListObservations(ctx context.Context, req *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
	if f.listObservationsFn != nil {
		return f.listObservationsFn(ctx, req)
	}
	return &marketinformationv1.ListObservationsResponse{}, nil
}

func (f *fakeMISServer) RecordObservationBatch(ctx context.Context, req *marketinformationv1.RecordObservationBatchRequest) (*marketinformationv1.RecordObservationBatchResponse, error) {
	if f.recordObservationBatchFn != nil {
		return f.recordObservationBatchFn(ctx, req)
	}
	return &marketinformationv1.RecordObservationBatchResponse{
		TotalCount:   int32(len(req.GetObservations())),
		SuccessCount: int32(len(req.GetObservations())),
	}, nil
}

// setupFakeServer creates a bufconn-based fake gRPC server and returns a misclient.Client connected to it.
func setupFakeServer(t *testing.T, srv *fakeMISServer) (*misclient.Client, func()) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	marketinformationv1.RegisterMarketInformationServiceServer(server, srv)

	go func() {
		_ = server.Serve(lis)
	}()

	bufDialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	client, cleanup, err := misclient.New(context.Background(), misclient.Config{
		Target:  "passthrough:///bufnet",
		Timeout: 5 * time.Second,
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(bufDialer),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)

	return client, func() {
		_ = cleanup()
		server.Stop()
	}
}

// --- qualityToString tests ---

func TestQualityToString_Estimate(t *testing.T) {
	assert.Equal(t, "ESTIMATE", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE))
}

func TestQualityToString_Provisional(t *testing.T) {
	assert.Equal(t, "PROVISIONAL", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL))
}

func TestQualityToString_Actual(t *testing.T) {
	assert.Equal(t, "ACTUAL", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL))
}

func TestQualityToString_Revised(t *testing.T) {
	assert.Equal(t, "REVISED", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED))
}

func TestQualityToString_Unspecified(t *testing.T) {
	assert.Equal(t, "UNSPECIFIED", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED))
}

func TestQualityToString_Unknown(t *testing.T) {
	// Default case: any value not in the switch
	assert.Equal(t, "UNSPECIFIED", qualityToString(marketinformationv1.QualityLevel(999)))
}

// --- Constructor tests ---

func TestNewMISAdapter(t *testing.T) {
	adapter := NewMISAdapter(nil)
	require.NotNil(t, adapter)
}

func TestNewPublisherAdapter(t *testing.T) {
	adapter := NewPublisherAdapter(nil)
	require.NotNil(t, adapter)
}

// --- FetchObservations tests ---

func TestFetchObservations_SinglePage(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	srv := &fakeMISServer{
		listObservationsFn: func(_ context.Context, req *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
			assert.Equal(t, "ENERGY_TARIFF", req.GetDatasetCode())
			assert.Equal(t, int32(1000), req.GetPageSize())
			return &marketinformationv1.ListObservationsResponse{
				Observations: []*marketinformationv1.MarketPriceObservation{
					{
						ValidFrom: timestamppb.New(now.Add(-1 * time.Hour)),
						Value:     "42.50",
						Quality:   marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					},
					{
						ValidFrom: timestamppb.New(now.Add(-2 * time.Hour)),
						Value:     "41.00",
						Quality:   marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
					},
				},
				NextPageToken: "", // no more pages
			}, nil
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewMISAdapter(client)
	obs, err := adapter.FetchObservations(context.Background(), "ENERGY_TARIFF", now)

	require.NoError(t, err)
	require.Len(t, obs, 2)
	assert.Equal(t, "42.5", obs[0].Value.String())
	assert.Equal(t, "ACTUAL", obs[0].Quality)
	assert.Equal(t, now.Add(-1*time.Hour), obs[0].Timestamp)
	assert.Equal(t, "41", obs[1].Value.String())
	assert.Equal(t, "ESTIMATE", obs[1].Quality)
}

func TestFetchObservations_MultiplePagesWithPagination(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	callCount := 0

	srv := &fakeMISServer{
		listObservationsFn: func(_ context.Context, req *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
			callCount++
			if callCount == 1 {
				assert.Equal(t, "", req.GetPageToken())
				return &marketinformationv1.ListObservationsResponse{
					Observations: []*marketinformationv1.MarketPriceObservation{
						{ValidFrom: timestamppb.New(now.Add(-1 * time.Hour)), Value: "10.0", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL},
					},
					NextPageToken: "page2",
				}, nil
			}
			// Second page
			assert.Equal(t, "page2", req.GetPageToken())
			return &marketinformationv1.ListObservationsResponse{
				Observations: []*marketinformationv1.MarketPriceObservation{
					{ValidFrom: timestamppb.New(now.Add(-2 * time.Hour)), Value: "20.0", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL},
				},
				NextPageToken: "", // done
			}, nil
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewMISAdapter(client)
	obs, err := adapter.FetchObservations(context.Background(), "ENERGY_TARIFF", now)

	require.NoError(t, err)
	require.Len(t, obs, 2)
	assert.Equal(t, "10", obs[0].Value.String())
	assert.Equal(t, "20", obs[1].Value.String())
	assert.Equal(t, 2, callCount)
}

func TestFetchObservations_EmptyResponse(t *testing.T) {
	srv := &fakeMISServer{
		listObservationsFn: func(_ context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
			return &marketinformationv1.ListObservationsResponse{}, nil
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewMISAdapter(client)
	obs, err := adapter.FetchObservations(context.Background(), "EMPTY_DATASET", time.Now())

	require.NoError(t, err)
	assert.Empty(t, obs)
}

func TestFetchObservations_GRPCError(t *testing.T) {
	srv := &fakeMISServer{
		listObservationsFn: func(_ context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
			return nil, errors.New("connection refused")
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewMISAdapter(client)
	obs, err := adapter.FetchObservations(context.Background(), "FAILING_DATASET", time.Now())

	require.Error(t, err)
	assert.Nil(t, obs)
}

func TestFetchObservations_InvalidDecimalValue(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	srv := &fakeMISServer{
		listObservationsFn: func(_ context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
			return &marketinformationv1.ListObservationsResponse{
				Observations: []*marketinformationv1.MarketPriceObservation{
					{
						ValidFrom: timestamppb.New(now),
						Value:     "not-a-number",
						Quality:   marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					},
				},
			}, nil
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewMISAdapter(client)
	obs, err := adapter.FetchObservations(context.Background(), "BAD_DATA", now)

	require.Error(t, err)
	assert.Nil(t, obs)
	assert.Contains(t, err.Error(), "invalid decimal value")
	assert.Contains(t, err.Error(), "BAD_DATA")
}

func TestFetchObservations_GRPCErrorOnSecondPage(t *testing.T) {
	callCount := 0

	srv := &fakeMISServer{
		listObservationsFn: func(_ context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
			callCount++
			if callCount == 1 {
				return &marketinformationv1.ListObservationsResponse{
					Observations: []*marketinformationv1.MarketPriceObservation{
						{ValidFrom: timestamppb.New(time.Now()), Value: "10.0", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL},
					},
					NextPageToken: "page2",
				}, nil
			}
			return nil, errors.New("network timeout")
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewMISAdapter(client)
	obs, err := adapter.FetchObservations(context.Background(), "FAILING_PAGE2", time.Now())

	require.Error(t, err)
	assert.Nil(t, obs)
}

func TestFetchObservations_PageLimitExceeded(t *testing.T) {
	// Every page returns a NextPageToken, so after maxObservationPages (100) we hit the limit.
	srv := &fakeMISServer{
		listObservationsFn: func(_ context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
			return &marketinformationv1.ListObservationsResponse{
				Observations: []*marketinformationv1.MarketPriceObservation{
					{ValidFrom: timestamppb.New(time.Now()), Value: "1.0", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL},
				},
				NextPageToken: "more", // always more pages
			}, nil
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewMISAdapter(client)
	obs, err := adapter.FetchObservations(context.Background(), "INFINITE_DATASET", time.Now())

	require.Error(t, err)
	assert.Nil(t, obs)
	assert.ErrorIs(t, err, ErrObservationPageLimitExceeded)
	assert.Contains(t, err.Error(), "INFINITE_DATASET")
	assert.Contains(t, err.Error(), "100 pages")
}

// --- RecordObservationBatch tests ---

func TestRecordObservationBatch_Success(t *testing.T) {
	entries := []*marketinformationv1.BatchObservationEntry{
		{DatasetCode: "FORECAST_ENERGY", Value: "100.0"},
		{DatasetCode: "FORECAST_ENERGY", Value: "200.0"},
	}

	var receivedCount int
	srv := &fakeMISServer{
		recordObservationBatchFn: func(_ context.Context, req *marketinformationv1.RecordObservationBatchRequest) (*marketinformationv1.RecordObservationBatchResponse, error) {
			receivedCount = len(req.GetObservations())
			return &marketinformationv1.RecordObservationBatchResponse{
				TotalCount:   int32(len(req.GetObservations())),
				SuccessCount: int32(len(req.GetObservations())),
			}, nil
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewPublisherAdapter(client)
	resp, err := adapter.RecordObservationBatch(context.Background(), entries)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(2), resp.GetTotalCount())
	assert.Equal(t, int32(2), resp.GetSuccessCount())
	assert.Equal(t, 2, receivedCount)
}

func TestRecordObservationBatch_GRPCError(t *testing.T) {
	srv := &fakeMISServer{
		recordObservationBatchFn: func(_ context.Context, _ *marketinformationv1.RecordObservationBatchRequest) (*marketinformationv1.RecordObservationBatchResponse, error) {
			return nil, errors.New("service unavailable")
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewPublisherAdapter(client)
	resp, err := adapter.RecordObservationBatch(context.Background(), []*marketinformationv1.BatchObservationEntry{
		{DatasetCode: "FORECAST", Value: "1.0"},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestRecordObservationBatch_PartialFailure(t *testing.T) {
	srv := &fakeMISServer{
		recordObservationBatchFn: func(_ context.Context, _ *marketinformationv1.RecordObservationBatchRequest) (*marketinformationv1.RecordObservationBatchResponse, error) {
			return &marketinformationv1.RecordObservationBatchResponse{
				TotalCount:   3,
				SuccessCount: 2,
				FailureCount: 1,
			}, nil
		},
	}

	client, cleanup := setupFakeServer(t, srv)
	defer cleanup()

	adapter := NewPublisherAdapter(client)
	resp, err := adapter.RecordObservationBatch(context.Background(), []*marketinformationv1.BatchObservationEntry{
		{DatasetCode: "FORECAST", Value: "1.0"},
		{DatasetCode: "FORECAST", Value: "2.0"},
		{DatasetCode: "FORECAST", Value: "3.0"},
	})

	require.NoError(t, err) // The adapter itself doesn't error on partial failure, it returns the response
	require.NotNil(t, resp)
	assert.Equal(t, int32(1), resp.GetFailureCount())
}

// --- NoOpRefDataClient tests ---

func TestNoOpRefDataClient_GetNodeByResolutionKey_ReturnsError(t *testing.T) {
	client := &NoOpRefDataClient{}
	result, err := client.GetNodeByResolutionKey(context.Background(), "tenant-1", "region:us-east-1")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrRefDataNotConfigured)
	assert.Contains(t, err.Error(), "region:us-east-1")
}

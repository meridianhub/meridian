package client

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

const bufSize = 1024 * 1024

// mockServer implements the MarketInformationService for testing.
type mockServer struct {
	marketinformationv1.UnimplementedMarketInformationServiceServer

	// Configurable responses
	listObservationsResp  *marketinformationv1.ListObservationsResponse
	listObservationsErr   error
	recordObservationResp *marketinformationv1.RecordObservationResponse
	recordObservationErr  error
	batchResp             *marketinformationv1.RecordObservationBatchResponse
	batchErr              error
	retrieveDataSetResp   *marketinformationv1.RetrieveDataSetResponse
	retrieveDataSetErr    error

	// Call tracking
	listObservationsCalls atomic.Int32
	recordCalls           atomic.Int32
	batchCalls            atomic.Int32
	datasetCalls          atomic.Int32

	// For verifying context propagation
	lastMetadata metadata.MD
	metadataMu   sync.Mutex

	// For simulating transient failures
	failsRemain  atomic.Int32
	failureError error
}

func (m *mockServer) ListObservations(ctx context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
	m.listObservationsCalls.Add(1)

	// Capture metadata for verification
	m.metadataMu.Lock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		m.lastMetadata = md
	}
	m.metadataMu.Unlock()

	// Simulate transient failures for retry testing using compare-and-swap
	// to avoid going negative
	for {
		current := m.failsRemain.Load()
		if current <= 0 {
			break
		}
		if m.failsRemain.CompareAndSwap(current, current-1) {
			return nil, m.failureError
		}
	}

	if m.listObservationsErr != nil {
		return nil, m.listObservationsErr
	}
	return m.listObservationsResp, nil
}

func (m *mockServer) RecordObservation(ctx context.Context, _ *marketinformationv1.RecordObservationRequest) (*marketinformationv1.RecordObservationResponse, error) {
	m.recordCalls.Add(1)

	m.metadataMu.Lock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		m.lastMetadata = md
	}
	m.metadataMu.Unlock()

	if m.recordObservationErr != nil {
		return nil, m.recordObservationErr
	}
	return m.recordObservationResp, nil
}

func (m *mockServer) RecordObservationBatch(ctx context.Context, _ *marketinformationv1.RecordObservationBatchRequest) (*marketinformationv1.RecordObservationBatchResponse, error) {
	m.batchCalls.Add(1)

	m.metadataMu.Lock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		m.lastMetadata = md
	}
	m.metadataMu.Unlock()

	if m.batchErr != nil {
		return nil, m.batchErr
	}
	return m.batchResp, nil
}

func (m *mockServer) RetrieveDataSet(ctx context.Context, _ *marketinformationv1.RetrieveDataSetRequest) (*marketinformationv1.RetrieveDataSetResponse, error) {
	m.datasetCalls.Add(1)

	m.metadataMu.Lock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		m.lastMetadata = md
	}
	m.metadataMu.Unlock()

	if m.retrieveDataSetErr != nil {
		return nil, m.retrieveDataSetErr
	}
	return m.retrieveDataSetResp, nil
}

// setupMockServer creates a bufconn-based mock server and returns a client connected to it.
func setupMockServer(t *testing.T, mock *mockServer) (*Client, func()) {
	t.Helper()

	lis := bufconn.Listen(bufSize)

	server := grpc.NewServer()
	marketinformationv1.RegisterMarketInformationServiceServer(server, mock)

	go func() {
		_ = server.Serve(lis)
	}()

	bufDialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(bufDialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := &Client{
		conn:       conn,
		grpcClient: marketinformationv1.NewMarketInformationServiceClient(conn),
		timeout:    DefaultTimeout,
	}

	cleanup := func() {
		conn.Close()
		server.Stop()
		lis.Close()
	}

	return client, cleanup
}

func TestNew_WithTarget(t *testing.T) {
	// Just verify config parsing, not actual connection
	cfg := Config{
		Target:  "localhost:50051",
		Timeout: 5 * time.Second,
	}
	cfg.applyDefaults()

	assert.Equal(t, 5*time.Second, cfg.Timeout)
	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, DefaultNamespace, cfg.Namespace)
}

func TestNew_WithServiceName(t *testing.T) {
	cfg := Config{
		ServiceName: "market-information",
		Namespace:   "production",
		Port:        9090,
	}
	cfg.applyDefaults()

	assert.Equal(t, "market-information", cfg.ServiceName)
	assert.Equal(t, "production", cfg.Namespace)
	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, DefaultTimeout, cfg.Timeout)
}

func TestNew_ErrTargetRequired(t *testing.T) {
	_, _, err := New(context.Background(), Config{})
	assert.ErrorIs(t, err, ErrTargetRequired)
}

func TestGetRate_Success(t *testing.T) {
	now := time.Now()
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{
					Id:                 "obs-1",
					DatasetCode:        "USD_EUR_FX",
					ResolutionKeyValue: "spot",
					Value:              "1.0856",
					Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					ObservedAt:         timestamppb.New(now),
					ValidFrom:          timestamppb.New(now),
				},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	obs, err := client.GetRate(context.Background(), "USD_EUR_FX", "spot", now,
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.NoError(t, err)
	assert.Equal(t, "1.0856", obs.Value)
	assert.Equal(t, "USD_EUR_FX", obs.DatasetCode)
	assert.Equal(t, int32(1), mock.listObservationsCalls.Load())
}

func TestGetRate_NotFound(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	_, err := client.GetRate(context.Background(), "USD_EUR_FX", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrObservationNotFound)
}

func TestGetRate_GRPCError(t *testing.T) {
	mock := &mockServer{
		listObservationsErr: status.Error(codes.Unavailable, "service unavailable"),
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	_, err := client.GetRate(context.Background(), "USD_EUR_FX", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get rate")
}

func TestGetRateWithKnowledgeTime_Success(t *testing.T) {
	asOf := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	knowledgeTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{
					Id:                 "obs-1",
					DatasetCode:        "USD_EUR_FX",
					ResolutionKeyValue: "spot",
					Value:              "1.0800",
					Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					ObservedAt:         timestamppb.New(asOf),
					ValidFrom:          timestamppb.New(asOf),
				},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	obs, err := client.GetRateWithKnowledgeTime(context.Background(), "USD_EUR_FX", "spot",
		asOf, knowledgeTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.NoError(t, err)
	assert.Equal(t, "1.0800", obs.Value)
}

func TestRecordObservation_Success(t *testing.T) {
	mock := &mockServer{
		recordObservationResp: &marketinformationv1.RecordObservationResponse{
			Observation: &marketinformationv1.MarketPriceObservation{
				Id:          "obs-new",
				DatasetCode: "USD_EUR_FX",
				Value:       "1.0856",
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	resp, err := client.RecordObservation(context.Background(), &marketinformationv1.RecordObservationRequest{
		DatasetCode: "USD_EUR_FX",
		ObservedAt:  timestamppb.Now(),
		ValidFrom:   timestamppb.Now(),
		Value:       "1.0856",
		Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		SourceCode:  "BLOOMBERG",
	})

	require.NoError(t, err)
	assert.Equal(t, "obs-new", resp.Observation.Id)
	assert.Equal(t, int32(1), mock.recordCalls.Load())
}

func TestRecordObservation_ValidationError(t *testing.T) {
	mock := &mockServer{
		recordObservationErr: status.Error(codes.InvalidArgument, "value must be positive"),
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	_, err := client.RecordObservation(context.Background(), &marketinformationv1.RecordObservationRequest{
		DatasetCode: "USD_EUR_FX",
		ObservedAt:  timestamppb.Now(),
		ValidFrom:   timestamppb.Now(),
		Value:       "-1.0",
		Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		SourceCode:  "BLOOMBERG",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRecordObservationBatch_Success(t *testing.T) {
	mock := &mockServer{
		batchResp: &marketinformationv1.RecordObservationBatchResponse{
			BatchId:      "batch-1",
			TotalCount:   3,
			SuccessCount: 3,
			FailureCount: 0,
			Results: []*marketinformationv1.BatchObservationResult{
				{Success: true, Index: 0, Observation: &marketinformationv1.MarketPriceObservation{Id: "obs-1"}},
				{Success: true, Index: 1, Observation: &marketinformationv1.MarketPriceObservation{Id: "obs-2"}},
				{Success: true, Index: 2, Observation: &marketinformationv1.MarketPriceObservation{Id: "obs-3"}},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	entries := []*marketinformationv1.BatchObservationEntry{
		{DatasetCode: "ELEC_TARIFF", ObservedAt: timestamppb.Now(), ValidFrom: timestamppb.Now(), Value: "0.15", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, SourceCode: "GRID"},
		{DatasetCode: "ELEC_TARIFF", ObservedAt: timestamppb.Now(), ValidFrom: timestamppb.Now(), Value: "0.12", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, SourceCode: "GRID"},
		{DatasetCode: "ELEC_TARIFF", ObservedAt: timestamppb.Now(), ValidFrom: timestamppb.Now(), Value: "0.18", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, SourceCode: "GRID"},
	}

	resp, err := client.RecordObservationBatch(context.Background(), entries)

	require.NoError(t, err)
	assert.Equal(t, int32(3), resp.TotalCount)
	assert.Equal(t, int32(3), resp.SuccessCount)
	assert.Equal(t, int32(0), resp.FailureCount)
}

func TestRecordObservationBatch_PartialFailure(t *testing.T) {
	mock := &mockServer{
		batchResp: &marketinformationv1.RecordObservationBatchResponse{
			BatchId:      "batch-1",
			TotalCount:   3,
			SuccessCount: 2,
			FailureCount: 1,
			Results: []*marketinformationv1.BatchObservationResult{
				{Success: true, Index: 0, Observation: &marketinformationv1.MarketPriceObservation{Id: "obs-1"}},
				{Success: false, Index: 1, ErrorMessage: "validation failed: value out of range"},
				{Success: true, Index: 2, Observation: &marketinformationv1.MarketPriceObservation{Id: "obs-3"}},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	entries := []*marketinformationv1.BatchObservationEntry{
		{DatasetCode: "ELEC_TARIFF", ObservedAt: timestamppb.Now(), ValidFrom: timestamppb.Now(), Value: "0.15", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, SourceCode: "GRID"},
		{DatasetCode: "ELEC_TARIFF", ObservedAt: timestamppb.Now(), ValidFrom: timestamppb.Now(), Value: "99999", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, SourceCode: "GRID"}, // Invalid
		{DatasetCode: "ELEC_TARIFF", ObservedAt: timestamppb.Now(), ValidFrom: timestamppb.Now(), Value: "0.18", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, SourceCode: "GRID"},
	}

	resp, err := client.RecordObservationBatch(context.Background(), entries)

	require.NoError(t, err)
	assert.Equal(t, int32(3), resp.TotalCount)
	assert.Equal(t, int32(2), resp.SuccessCount)
	assert.Equal(t, int32(1), resp.FailureCount)
	assert.False(t, resp.Results[1].Success)
	assert.Contains(t, resp.Results[1].ErrorMessage, "validation failed")
}

func TestGetDataSet_Success(t *testing.T) {
	mock := &mockServer{
		retrieveDataSetResp: &marketinformationv1.RetrieveDataSetResponse{
			Dataset: &marketinformationv1.DataSetDefinition{
				Id:      "ds-1",
				Code:    "USD_EUR_FX",
				Version: 1,
				Status:  marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	dataset, err := client.GetDataSet(context.Background(), "USD_EUR_FX", nil)

	require.NoError(t, err)
	assert.Equal(t, "USD_EUR_FX", dataset.Code)
	assert.Equal(t, int32(1), dataset.Version)
}

func TestGetDataSet_WithVersion(t *testing.T) {
	mock := &mockServer{
		retrieveDataSetResp: &marketinformationv1.RetrieveDataSetResponse{
			Dataset: &marketinformationv1.DataSetDefinition{
				Id:      "ds-1",
				Code:    "USD_EUR_FX",
				Version: 2,
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	version := int32(2)
	dataset, err := client.GetDataSet(context.Background(), "USD_EUR_FX", &version)

	require.NoError(t, err)
	assert.Equal(t, int32(2), dataset.Version)
}

func TestGetDataSet_NotFound(t *testing.T) {
	mock := &mockServer{
		retrieveDataSetErr: status.Error(codes.NotFound, "dataset not found"),
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	_, err := client.GetDataSet(context.Background(), "NONEXISTENT", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get dataset")
}

func TestContextPropagation_TenantID(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "TEST", Value: "1.0"},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	// Create context with tenant ID
	tenantID := tenant.MustNewTenantID("test_tenant_123")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	_, err := client.GetRate(ctx, "TEST", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.NoError(t, err)

	// Verify tenant ID was propagated
	mock.metadataMu.Lock()
	md := mock.lastMetadata
	mock.metadataMu.Unlock()

	tenantVals := md.Get(tenant.TenantIDKey)
	require.Len(t, tenantVals, 1)
	assert.Equal(t, tenantID.String(), tenantVals[0])
}

func TestContextPropagation_CorrelationID(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "TEST", Value: "1.0"},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	// Create context with correlation ID using string key to match production behavior
	// in shared/pkg/clients/common.go which uses ctx.Value("x-correlation-id")
	//nolint:staticcheck,revive // production code uses string keys
	ctx := context.WithValue(context.Background(), "x-correlation-id", "test-correlation-123")

	_, err := client.GetRate(ctx, "TEST", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.NoError(t, err)

	// Verify correlation ID was propagated
	mock.metadataMu.Lock()
	md := mock.lastMetadata
	mock.metadataMu.Unlock()

	corrVals := md.Get("x-correlation-id")
	require.Len(t, corrVals, 1)
	assert.Equal(t, "test-correlation-123", corrVals[0])
}

func TestContextCancellation(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "TEST", Value: "1.0"},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	// Create already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetRate(ctx, "TEST", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestConcurrentCalls(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "TEST", Value: "1.0"},
			},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := client.GetRate(context.Background(), "TEST", "spot", time.Now(),
				marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent call failed: %v", err)
	}

	assert.Equal(t, int32(numGoroutines), mock.listObservationsCalls.Load())
}

func TestClose(t *testing.T) {
	mock := &mockServer{}
	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	// Close should succeed
	err := client.Close()
	require.NoError(t, err)
}

func TestConn(t *testing.T) {
	mock := &mockServer{}
	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	conn := client.Conn()
	assert.NotNil(t, conn)
}

func TestRecordObservation_NilRequest(t *testing.T) {
	mock := &mockServer{}
	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	_, err := client.RecordObservation(context.Background(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilRequest)
}

func TestRecordObservationBatch_EmptyObservations(t *testing.T) {
	mock := &mockServer{}
	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	_, err := client.RecordObservationBatch(context.Background(), []*marketinformationv1.BatchObservationEntry{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyObservations)
}

func TestListObservations_NilRequest(t *testing.T) {
	mock := &mockServer{}
	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	_, err := client.ListObservations(context.Background(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilRequest)
}

func TestListObservations_Success(t *testing.T) {
	now := time.Now()
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "USD_EUR_FX", Value: "1.0850", ObservedAt: timestamppb.New(now)},
				{Id: "obs-2", DatasetCode: "USD_EUR_FX", Value: "1.0855", ObservedAt: timestamppb.New(now.Add(-time.Hour))},
			},
			NextPageToken: "token-123",
			TotalCount:    10,
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	resp, err := client.ListObservations(context.Background(), &marketinformationv1.ListObservationsRequest{
		DatasetCode: "USD_EUR_FX",
		PageSize:    2,
	})

	require.NoError(t, err)
	assert.Len(t, resp.Observations, 2)
	assert.Equal(t, "token-123", resp.NextPageToken)
	assert.Equal(t, int32(10), resp.TotalCount)
}

// setupMockServerWithResilience creates a bufconn-based mock server and returns a client
// with resilience configured.
func setupMockServerWithResilience(t *testing.T, mock *mockServer) (*Client, func()) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	marketinformationv1.RegisterMarketInformationServiceServer(server, mock)

	go func() {
		_ = server.Serve(lis)
	}()

	bufDialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(bufDialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	resilientCfg := clients.DefaultResilientClientConfig("market-information-test")
	resilientCfg.MaxRetries = 3
	resilientCfg.InitialInterval = 10 * time.Millisecond

	client := &Client{
		conn:       conn,
		grpcClient: marketinformationv1.NewMarketInformationServiceClient(conn),
		resilient:  clients.NewResilientClient(resilientCfg),
		timeout:    DefaultTimeout,
	}

	cleanup := func() {
		conn.Close()
		server.Stop()
		lis.Close()
	}

	return client, cleanup
}

func TestGetRateWithKnowledgeTime_NotFound(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{},
		},
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	asOf := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	knowledgeTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	_, err := client.GetRateWithKnowledgeTime(context.Background(), "USD_EUR_FX", "spot",
		asOf, knowledgeTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrObservationNotFound)
}

func TestGetRateWithKnowledgeTime_GRPCError(t *testing.T) {
	mock := &mockServer{
		listObservationsErr: status.Error(codes.Unavailable, "service unavailable"),
	}

	client, cleanup := setupMockServer(t, mock)
	defer cleanup()

	asOf := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	knowledgeTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	_, err := client.GetRateWithKnowledgeTime(context.Background(), "USD_EUR_FX", "spot",
		asOf, knowledgeTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get rate with knowledge time")
}

func TestGetRate_WithResilience_Success(t *testing.T) {
	now := time.Now()
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "USD_EUR_FX", Value: "1.0856", ObservedAt: timestamppb.New(now)},
			},
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	obs, err := client.GetRate(context.Background(), "USD_EUR_FX", "spot", now,
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.NoError(t, err)
	assert.Equal(t, "1.0856", obs.Value)
}

func TestGetRate_WithResilience_NotFound(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{},
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	_, err := client.GetRate(context.Background(), "USD_EUR_FX", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrObservationNotFound)
}

func TestGetRate_WithResilience_GRPCError(t *testing.T) {
	mock := &mockServer{
		listObservationsErr: status.Error(codes.Internal, "internal error"),
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	_, err := client.GetRate(context.Background(), "USD_EUR_FX", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get rate")
}

func TestGetRateWithKnowledgeTime_WithResilience_Success(t *testing.T) {
	asOf := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	knowledgeTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "USD_EUR_FX", Value: "1.0800", ObservedAt: timestamppb.New(asOf)},
			},
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	obs, err := client.GetRateWithKnowledgeTime(context.Background(), "USD_EUR_FX", "spot",
		asOf, knowledgeTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.NoError(t, err)
	assert.Equal(t, "1.0800", obs.Value)
}

func TestGetRateWithKnowledgeTime_WithResilience_NotFound(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{},
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	asOf := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	knowledgeTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	_, err := client.GetRateWithKnowledgeTime(context.Background(), "USD_EUR_FX", "spot",
		asOf, knowledgeTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrObservationNotFound)
}

func TestGetRateWithKnowledgeTime_WithResilience_GRPCError(t *testing.T) {
	mock := &mockServer{
		listObservationsErr: status.Error(codes.Internal, "internal error"),
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	asOf := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	knowledgeTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	_, err := client.GetRateWithKnowledgeTime(context.Background(), "USD_EUR_FX", "spot",
		asOf, knowledgeTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get rate with knowledge time")
}

func TestRecordObservation_WithResilience_Success(t *testing.T) {
	mock := &mockServer{
		recordObservationResp: &marketinformationv1.RecordObservationResponse{
			Observation: &marketinformationv1.MarketPriceObservation{
				Id:          "obs-new",
				DatasetCode: "USD_EUR_FX",
				Value:       "1.0856",
			},
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	resp, err := client.RecordObservation(context.Background(), &marketinformationv1.RecordObservationRequest{
		DatasetCode: "USD_EUR_FX",
		ObservedAt:  timestamppb.Now(),
		ValidFrom:   timestamppb.Now(),
		Value:       "1.0856",
		Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		SourceCode:  "BLOOMBERG",
	})

	require.NoError(t, err)
	assert.Equal(t, "obs-new", resp.Observation.Id)
}

func TestRecordObservationBatch_WithResilience_Success(t *testing.T) {
	mock := &mockServer{
		batchResp: &marketinformationv1.RecordObservationBatchResponse{
			BatchId:      "batch-1",
			TotalCount:   1,
			SuccessCount: 1,
			FailureCount: 0,
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	entries := []*marketinformationv1.BatchObservationEntry{
		{DatasetCode: "ELEC_TARIFF", ObservedAt: timestamppb.Now(), ValidFrom: timestamppb.Now(), Value: "0.15", Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, SourceCode: "GRID"},
	}

	resp, err := client.RecordObservationBatch(context.Background(), entries)

	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.TotalCount)
}

func TestGetDataSet_WithResilience_Success(t *testing.T) {
	mock := &mockServer{
		retrieveDataSetResp: &marketinformationv1.RetrieveDataSetResponse{
			Dataset: &marketinformationv1.DataSetDefinition{
				Id:      "ds-1",
				Code:    "USD_EUR_FX",
				Version: 1,
			},
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	dataset, err := client.GetDataSet(context.Background(), "USD_EUR_FX", nil)

	require.NoError(t, err)
	assert.Equal(t, "USD_EUR_FX", dataset.Code)
}

func TestGetDataSet_WithResilience_WithVersion(t *testing.T) {
	mock := &mockServer{
		retrieveDataSetResp: &marketinformationv1.RetrieveDataSetResponse{
			Dataset: &marketinformationv1.DataSetDefinition{
				Id:      "ds-1",
				Code:    "USD_EUR_FX",
				Version: 3,
			},
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	version := int32(3)
	dataset, err := client.GetDataSet(context.Background(), "USD_EUR_FX", &version)

	require.NoError(t, err)
	assert.Equal(t, int32(3), dataset.Version)
}

func TestGetDataSet_WithResilience_Error(t *testing.T) {
	mock := &mockServer{
		retrieveDataSetErr: status.Error(codes.NotFound, "not found"),
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	_, err := client.GetDataSet(context.Background(), "NONEXISTENT", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get dataset")
}

func TestListObservations_WithResilience_Success(t *testing.T) {
	mock := &mockServer{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "USD_EUR_FX", Value: "1.0850"},
			},
			TotalCount: 1,
		},
	}

	client, cleanup := setupMockServerWithResilience(t, mock)
	defer cleanup()

	resp, err := client.ListObservations(context.Background(), &marketinformationv1.ListObservationsRequest{
		DatasetCode: "USD_EUR_FX",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Observations, 1)
}

func TestClose_NilConn(t *testing.T) {
	client := &Client{
		conn:    nil,
		timeout: DefaultTimeout,
	}

	err := client.Close()
	require.NoError(t, err)
}

func TestConfig_ApplyDefaults(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()

	assert.Equal(t, DefaultTimeout, cfg.Timeout)
	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, DefaultNamespace, cfg.Namespace)
}

func TestConfig_ApplyDefaults_NoOverwrite(t *testing.T) {
	cfg := Config{
		Timeout:   5 * time.Second,
		Port:      9090,
		Namespace: "production",
	}
	cfg.applyDefaults()

	assert.Equal(t, 5*time.Second, cfg.Timeout)
	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, "production", cfg.Namespace)
}

func TestNew_WithTarget_CreatesClient(t *testing.T) {
	client, cleanup, err := New(context.Background(), Config{
		Target: "localhost:50051",
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.grpcClient)
	assert.Equal(t, DefaultTimeout, client.timeout)
	assert.Nil(t, client.resilient)

	err = cleanup()
	require.NoError(t, err)
}

func TestNew_WithTarget_AndResilience(t *testing.T) {
	resilientCfg := clients.DefaultResilientClientConfig("test")
	client, cleanup, err := New(context.Background(), Config{
		Target:     "localhost:50051",
		Resilience: &resilientCfg,
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NotNil(t, client.resilient)

	err = cleanup()
	require.NoError(t, err)
}

func TestNew_WithTarget_CustomDialOptions(t *testing.T) {
	client, cleanup, err := New(context.Background(), Config{
		Target: "localhost:50051",
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, client)

	err = cleanup()
	require.NoError(t, err)
}

func TestWithResilience_RetryOnTransientError(t *testing.T) {
	mock := &mockServer{
		failsRemain:  atomic.Int32{},
		failureError: status.Error(codes.Unavailable, "transient failure"),
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{Id: "obs-1", DatasetCode: "TEST", Value: "1.0"},
			},
		},
	}
	mock.failsRemain.Store(2) // Fail twice, succeed on third

	// Create client with resilience
	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	marketinformationv1.RegisterMarketInformationServiceServer(server, mock)

	go func() {
		_ = server.Serve(lis)
	}()

	bufDialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(bufDialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	resilientCfg := clients.DefaultResilientClientConfig("market-information-test")
	resilientCfg.MaxRetries = 5
	resilientCfg.InitialInterval = 10 * time.Millisecond

	client := &Client{
		conn:       conn,
		grpcClient: marketinformationv1.NewMarketInformationServiceClient(conn),
		resilient:  clients.NewResilientClient(resilientCfg),
		timeout:    DefaultTimeout,
	}

	defer func() {
		conn.Close()
		server.Stop()
		lis.Close()
	}()

	obs, err := client.GetRate(context.Background(), "TEST", "spot", time.Now(),
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)

	require.NoError(t, err)
	assert.Equal(t, "1.0", obs.Value)
	assert.GreaterOrEqual(t, mock.listObservationsCalls.Load(), int32(3))
}

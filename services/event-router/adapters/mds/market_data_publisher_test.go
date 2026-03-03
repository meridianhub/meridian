package mds

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/utilization-metering-consumer/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockMDSClient implements MarketInformationServiceClient for testing.
type mockMDSClient struct {
	mu             sync.Mutex
	batchCalls     []*marketinformationv1.RecordObservationBatchRequest
	batchResponses []*marketinformationv1.RecordObservationBatchResponse
	batchErr       error
	callCount      atomic.Int64
}

func newMockMDSClient() *mockMDSClient {
	return &mockMDSClient{}
}

func (m *mockMDSClient) RecordObservationBatch(_ context.Context, req *marketinformationv1.RecordObservationBatchRequest, _ ...grpc.CallOption) (*marketinformationv1.RecordObservationBatchResponse, error) {
	m.callCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	m.batchCalls = append(m.batchCalls, req)

	if m.batchErr != nil {
		return nil, m.batchErr
	}

	if len(m.batchResponses) > 0 {
		resp := m.batchResponses[0]
		m.batchResponses = m.batchResponses[1:]
		return resp, nil
	}

	// Default success response
	results := make([]*marketinformationv1.BatchObservationResult, len(req.Observations))
	for i := range req.Observations {
		results[i] = &marketinformationv1.BatchObservationResult{
			Success: true,
			Observation: &marketinformationv1.MarketPriceObservation{
				Id:          fmt.Sprintf("obs-%d", i),
				DatasetCode: req.Observations[i].DatasetCode,
			},
			Index: int32(i),
		}
	}

	return &marketinformationv1.RecordObservationBatchResponse{
		Results:      results,
		TotalCount:   int32(len(req.Observations)),
		SuccessCount: int32(len(req.Observations)),
	}, nil
}

// Stub methods for the full interface (unused in tests)
func (m *mockMDSClient) RegisterDataSet(context.Context, *marketinformationv1.RegisterDataSetRequest, ...grpc.CallOption) (*marketinformationv1.RegisterDataSetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) UpdateDataSet(context.Context, *marketinformationv1.UpdateDataSetRequest, ...grpc.CallOption) (*marketinformationv1.UpdateDataSetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) ActivateDataSet(context.Context, *marketinformationv1.ActivateDataSetRequest, ...grpc.CallOption) (*marketinformationv1.ActivateDataSetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) DeprecateDataSet(context.Context, *marketinformationv1.DeprecateDataSetRequest, ...grpc.CallOption) (*marketinformationv1.DeprecateDataSetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) RetrieveDataSet(context.Context, *marketinformationv1.RetrieveDataSetRequest, ...grpc.CallOption) (*marketinformationv1.RetrieveDataSetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) ListDataSets(context.Context, *marketinformationv1.ListDataSetsRequest, ...grpc.CallOption) (*marketinformationv1.ListDataSetsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) RegisterDataSource(context.Context, *marketinformationv1.RegisterDataSourceRequest, ...grpc.CallOption) (*marketinformationv1.RegisterDataSourceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) UpdateDataSource(context.Context, *marketinformationv1.UpdateDataSourceRequest, ...grpc.CallOption) (*marketinformationv1.UpdateDataSourceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) DeactivateDataSource(context.Context, *marketinformationv1.DeactivateDataSourceRequest, ...grpc.CallOption) (*marketinformationv1.DeactivateDataSourceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) ListDataSources(context.Context, *marketinformationv1.ListDataSourcesRequest, ...grpc.CallOption) (*marketinformationv1.ListDataSourcesResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) RecordObservation(context.Context, *marketinformationv1.RecordObservationRequest, ...grpc.CallOption) (*marketinformationv1.RecordObservationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) RetrieveObservation(context.Context, *marketinformationv1.RetrieveObservationRequest, ...grpc.CallOption) (*marketinformationv1.RetrieveObservationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) ListObservations(context.Context, *marketinformationv1.ListObservationsRequest, ...grpc.CallOption) (*marketinformationv1.ListObservationsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockMDSClient) getBatchCalls() []*marketinformationv1.RecordObservationBatchRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*marketinformationv1.RecordObservationBatchRequest, len(m.batchCalls))
	copy(result, m.batchCalls)
	return result
}

func testInstrument() quantity.Instrument {
	inst, _ := quantity.NewInstrument("TRANSACTION", 1, "COUNT", 0)
	return inst
}

func testMeasurement(tenantID, service, operation string, amount int64, ts time.Time) *domain.UtilizationMeasurement {
	return &domain.UtilizationMeasurement{
		TenantID:      tenantID,
		ServiceName:   service,
		OperationType: operation,
		Amount:        quantity.NewAssetFromInt(amount, testInstrument()),
		Timestamp:     ts,
		CorrelationID: "corr-" + tenantID,
	}
}

// --- AggregationBuffer Tests ---

func TestAggregationBuffer_AddMeasurement(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	m := testMeasurement("tenant-1", "current-account", "CreateAccount", 1, ts)

	buf.Add(m)

	windows := buf.Snapshot()
	require.Len(t, windows, 1)

	w := windows[0]
	assert.Equal(t, int64(1), w.ObservationCount)
	assert.True(t, w.TotalUnits.Equal(decimal.NewFromInt(1)))
	assert.True(t, w.PeakUnits.Equal(decimal.NewFromInt(1)))
}

func TestAggregationBuffer_AggregatesWithinWindow(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	// Add multiple measurements within the same hour window
	buf.Add(testMeasurement("tenant-1", "current-account", "CreateAccount", 3, base.Add(5*time.Minute)))
	buf.Add(testMeasurement("tenant-1", "current-account", "CreateAccount", 7, base.Add(15*time.Minute)))
	buf.Add(testMeasurement("tenant-1", "current-account", "CreateAccount", 2, base.Add(30*time.Minute)))

	windows := buf.Snapshot()
	require.Len(t, windows, 1)

	w := windows[0]
	assert.Equal(t, int64(3), w.ObservationCount)
	assert.True(t, w.TotalUnits.Equal(decimal.NewFromInt(12)))
	assert.True(t, w.PeakUnits.Equal(decimal.NewFromInt(7)))
	assert.True(t, w.AvgUnits.Equal(decimal.NewFromInt(4)))
}

func TestAggregationBuffer_SeparatesWindowsByTime(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	hour1 := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	hour2 := time.Date(2025, 1, 1, 11, 30, 0, 0, time.UTC)

	buf.Add(testMeasurement("tenant-1", "current-account", "Op", 5, hour1))
	buf.Add(testMeasurement("tenant-1", "current-account", "Op", 3, hour2))

	windows := buf.Snapshot()
	assert.Len(t, windows, 2)
}

func TestAggregationBuffer_SeparatesWindowsByResolutionKey(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)

	buf.Add(testMeasurement("tenant-1", "current-account", "Op", 5, ts))
	buf.Add(testMeasurement("tenant-2", "current-account", "Op", 3, ts))

	windows := buf.Snapshot()
	assert.Len(t, windows, 2)
}

func TestAggregationBuffer_DrainReturnsAndClears(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	buf.Add(testMeasurement("tenant-1", "current-account", "Op", 5, ts))

	drained := buf.Drain()
	require.Len(t, drained, 1)

	// Buffer should be empty after drain
	assert.Empty(t, buf.Snapshot())
}

func TestAggregationBuffer_ConcurrentAccess(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)
	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	const goroutines = 50
	const perGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			for j := range perGoroutine {
				ts := base.Add(time.Duration(j) * time.Minute)
				m := testMeasurement(
					fmt.Sprintf("tenant-%d", idx),
					"current-account",
					"Op",
					1,
					ts,
				)
				buf.Add(m)
			}
		}(i)
	}

	wg.Wait()

	windows := buf.Snapshot()
	totalObs := int64(0)
	for _, w := range windows {
		totalObs += w.ObservationCount
	}
	assert.Equal(t, int64(goroutines*perGoroutine), totalObs)
}

func TestAggregationBuffer_WindowKey(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	// Two measurements from same tenant/service in same hour -> same window
	ts1 := time.Date(2025, 1, 1, 10, 15, 0, 0, time.UTC)
	ts2 := time.Date(2025, 1, 1, 10, 45, 0, 0, time.UTC)

	buf.Add(testMeasurement("tenant-1", "current-account", "Op", 1, ts1))
	buf.Add(testMeasurement("tenant-1", "current-account", "Op", 1, ts2))

	windows := buf.Snapshot()
	require.Len(t, windows, 1)
	assert.Equal(t, int64(2), windows[0].ObservationCount)
}

// --- MarketDataPublisher Tests ---

func TestNewMarketDataPublisher_Validation(t *testing.T) {
	mock := newMockMDSClient()

	t.Run("nil MDS client", func(t *testing.T) {
		_, err := NewMarketDataPublisher(nil, DefaultConfig())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilMDSClient)
	})

	t.Run("valid config", func(t *testing.T) {
		pub, err := NewMarketDataPublisher(mock, DefaultConfig())
		require.NoError(t, err)
		require.NotNil(t, pub)
		pub.Stop()
	})
}

func TestMarketDataPublisher_PublishFlushesBuffer(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 100 * time.Millisecond
	cfg.WindowSize = 1 * time.Hour

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	pub.Publish(testMeasurement("tenant-1", "current-account", "CreateAccount", 5, ts))

	// Wait for flush to happen
	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() > 0
		})
	require.NoError(t, err, "expected at least one batch call after flush interval")

	calls := mock.getBatchCalls()
	require.NotEmpty(t, calls)

	// Verify observation mapping
	req := calls[0]
	require.NotEmpty(t, req.Observations)
	obs := req.Observations[0]

	assert.Equal(t, "UTILIZATION_TRANSACTION", obs.DatasetCode)
	assert.Equal(t, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, obs.Quality)
	assert.Contains(t, obs.ClientReference, "tenant-1")
	assert.Contains(t, obs.ClientReference, "current-account")
}

func TestMarketDataPublisher_ObservationMapping(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 50 * time.Millisecond

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	windowStart := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	ts := windowStart.Add(30 * time.Minute)
	pub.Publish(testMeasurement("tenant-1", "current-account", "CreateAccount", 10, ts))

	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() > 0
		})
	require.NoError(t, err)

	calls := mock.getBatchCalls()
	require.NotEmpty(t, calls)

	obs := calls[0].Observations[0]

	// dataset_code: UTILIZATION_{INSTRUMENT_TYPE}
	assert.Equal(t, "UTILIZATION_TRANSACTION", obs.DatasetCode)

	// quality: QUALITY_LEVEL_ACTUAL
	assert.Equal(t, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, obs.Quality)

	// resolution_key: tenant/{resource_type}/{resource_id}
	assert.Equal(t, "tenant-1/current-account/CreateAccount", obs.ClientReference)

	// observed_at: window midpoint
	expectedMidpoint := windowStart.Add(30 * time.Minute)
	assert.Equal(t, expectedMidpoint.Unix(), obs.ObservedAt.AsTime().Unix())

	// valid_from/valid_to: window boundaries
	assert.Equal(t, windowStart.Unix(), obs.ValidFrom.AsTime().Unix())
	expectedEnd := windowStart.Add(1 * time.Hour)
	assert.Equal(t, expectedEnd.Unix(), obs.ValidTo.AsTime().Unix())

	// value: total_units as string
	assert.Equal(t, "10", obs.Value)
}

func TestMarketDataPublisher_ResolutionKeyFormat(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 50 * time.Millisecond

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	pub.Publish(testMeasurement("my-tenant", "payment-order", "ProcessPayment", 1, ts))

	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() > 0
		})
	require.NoError(t, err)

	calls := mock.getBatchCalls()
	require.NotEmpty(t, calls)
	obs := calls[0].Observations[0]
	assert.Equal(t, "my-tenant/payment-order/ProcessPayment", obs.ClientReference)
}

func TestMarketDataPublisher_MultipleWindowsFlushed(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 100 * time.Millisecond
	cfg.WindowSize = 1 * time.Hour

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	// Add measurements in two different hour windows
	hour1 := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	hour2 := time.Date(2025, 1, 1, 11, 30, 0, 0, time.UTC)

	pub.Publish(testMeasurement("tenant-1", "current-account", "Op", 5, hour1))
	pub.Publish(testMeasurement("tenant-1", "current-account", "Op", 3, hour2))

	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() > 0
		})
	require.NoError(t, err)

	calls := mock.getBatchCalls()
	require.NotEmpty(t, calls)

	// Should have 2 observations in the batch (one per window)
	totalObs := 0
	for _, call := range calls {
		totalObs += len(call.Observations)
	}
	assert.Equal(t, 2, totalObs)
}

func TestMarketDataPublisher_CircuitBreaker(t *testing.T) {
	mock := newMockMDSClient()
	mock.batchErr = status.Errorf(codes.Unavailable, "service unavailable")

	cfg := DefaultConfig()
	cfg.FlushInterval = 50 * time.Millisecond
	cfg.CircuitBreakerConsecutiveFailures = 3

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	// Continuously publish measurements so each flush attempt has data
	stopPublish := make(chan struct{})
	go func() {
		ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
		i := 0
		for {
			select {
			case <-stopPublish:
				return
			default:
				pub.Publish(testMeasurement("tenant-1", "current-account", "Op", 1, ts.Add(time.Duration(i)*time.Millisecond)))
				i++
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	defer close(stopPublish)

	// Wait for enough flush attempts to trigger circuit breaker
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return pub.IsCircuitBreakerOpen()
		})
	require.NoError(t, err, "circuit breaker should open after consecutive failures")

	assert.True(t, pub.IsCircuitBreakerOpen())
}

func TestMarketDataPublisher_StopFlushesRemaining(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 10 * time.Minute // Long interval so manual flush on stop

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	pub.Publish(testMeasurement("tenant-1", "current-account", "Op", 5, ts))

	// Stop should flush remaining
	pub.Stop()

	calls := mock.getBatchCalls()
	require.NotEmpty(t, calls, "Stop should flush remaining buffer contents")
}

func TestMarketDataPublisher_ConcurrentPublish(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 200 * time.Millisecond

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	const goroutines = 20
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			for j := range perGoroutine {
				ts := base.Add(time.Duration(j) * time.Minute)
				pub.Publish(testMeasurement(
					fmt.Sprintf("tenant-%d", idx),
					"current-account",
					"Op",
					1,
					ts,
				))
			}
		}(i)
	}

	wg.Wait()

	// Wait for flush
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() > 0
		})
	require.NoError(t, err)
}

func TestMarketDataPublisher_DatasetCodeMapping(t *testing.T) {
	tests := []struct {
		instrumentCode  string
		expectedDataset string
	}{
		{"TRANSACTION", "UTILIZATION_TRANSACTION"},
		{"API_CALL", "UTILIZATION_API_CALL"},
		{"STORAGE_GB_HOUR", "UTILIZATION_STORAGE_GB_HOUR"},
		{"COMPUTE_HOUR", "UTILIZATION_COMPUTE_HOUR"},
		{"OPERATION", "UTILIZATION_OPERATION"},
	}

	for _, tt := range tests {
		t.Run(tt.instrumentCode, func(t *testing.T) {
			mock := newMockMDSClient()
			cfg := DefaultConfig()
			cfg.FlushInterval = 50 * time.Millisecond

			pub, err := NewMarketDataPublisher(mock, cfg)
			require.NoError(t, err)
			defer pub.Stop()

			inst, err := quantity.NewInstrument(tt.instrumentCode, 1, "COUNT", 0)
			require.NoError(t, err)

			m := &domain.UtilizationMeasurement{
				TenantID:      "tenant-1",
				ServiceName:   "test-service",
				OperationType: "TestOp",
				Amount:        quantity.NewAssetFromInt(1, inst),
				Timestamp:     time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC),
				CorrelationID: "corr-1",
			}
			pub.Publish(m)

			err = await.New().
				AtMost(3 * time.Second).
				PollInterval(50 * time.Millisecond).
				Until(func() bool {
					return mock.callCount.Load() > 0
				})
			require.NoError(t, err)

			calls := mock.getBatchCalls()
			require.NotEmpty(t, calls)
			assert.Equal(t, tt.expectedDataset, calls[0].Observations[0].DatasetCode)
		})
	}
}

func TestMarketDataPublisher_WindowBoundaryTimestamps(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 50 * time.Millisecond
	cfg.WindowSize = 1 * time.Hour

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	// Measurement exactly at window boundary
	ts := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	pub.Publish(testMeasurement("tenant-1", "svc", "Op", 1, ts))

	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() > 0
		})
	require.NoError(t, err)

	calls := mock.getBatchCalls()
	require.NotEmpty(t, calls)
	obs := calls[0].Observations[0]

	// valid_from should be the window start (10:00)
	assert.Equal(t, ts.Unix(), obs.ValidFrom.AsTime().Unix())
	// valid_to should be window end (11:00)
	assert.Equal(t, ts.Add(1*time.Hour).Unix(), obs.ValidTo.AsTime().Unix())
	// observed_at should be midpoint (10:30)
	assert.Equal(t, ts.Add(30*time.Minute).Unix(), obs.ObservedAt.AsTime().Unix())
}

func TestMarketDataPublisher_SourceCode(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 50 * time.Millisecond
	cfg.SourceCode = "MERIDIAN_UTILIZATION"

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	pub.Publish(testMeasurement("tenant-1", "svc", "Op", 1, ts))

	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() > 0
		})
	require.NoError(t, err)

	calls := mock.getBatchCalls()
	require.NotEmpty(t, calls)
	obs := calls[0].Observations[0]
	assert.Equal(t, "MERIDIAN_UTILIZATION", obs.SourceCode)
}

func TestUtilizationWindow_AverageCalculation(t *testing.T) {
	w := &UtilizationWindow{
		TotalUnits:       decimal.NewFromInt(15),
		PeakUnits:        decimal.NewFromInt(7),
		ObservationCount: 3,
	}
	w.recalculateAvg()
	assert.True(t, w.AvgUnits.Equal(decimal.NewFromInt(5)))
}

func TestUtilizationWindow_AverageWithZeroCount(t *testing.T) {
	w := &UtilizationWindow{
		TotalUnits:       decimal.Zero,
		PeakUnits:        decimal.Zero,
		ObservationCount: 0,
	}
	w.recalculateAvg()
	assert.True(t, w.AvgUnits.Equal(decimal.Zero))
}

func TestMarketDataPublisher_BufferSizeMetric(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 10 * time.Minute // No auto-flush

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	pub.Publish(testMeasurement("tenant-1", "svc", "Op", 1, ts))
	pub.Publish(testMeasurement("tenant-2", "svc", "Op", 1, ts))

	assert.Equal(t, 2, pub.BufferSize())
}

// --- Observation timestamp validation ---

func TestObservationTimestamps(t *testing.T) {
	windowSize := 1 * time.Hour
	windowStart := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(windowSize)
	midpoint := windowStart.Add(windowSize / 2)

	obs := buildObservation(
		&UtilizationWindow{
			ResolutionKey:    "tenant-1/svc/Op",
			InstrumentCode:   "TRANSACTION",
			WindowStart:      windowStart,
			WindowEnd:        windowEnd,
			TotalUnits:       decimal.NewFromInt(10),
			PeakUnits:        decimal.NewFromInt(5),
			AvgUnits:         decimal.NewFromInt(3),
			ObservationCount: 3,
		},
		"MERIDIAN_UTILIZATION",
	)

	assert.Equal(t, timestamppb.New(midpoint), obs.ObservedAt)
	assert.Equal(t, timestamppb.New(windowStart), obs.ValidFrom)
	assert.Equal(t, timestamppb.New(windowEnd), obs.ValidTo)
}

// --- Restore tests ---

func TestAggregationBuffer_RestoreOnEmpty(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	windowStart := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	windows := []*UtilizationWindow{
		{
			ResolutionKey:    "tenant-1/svc/Op",
			InstrumentCode:   "TRANSACTION",
			WindowStart:      windowStart,
			WindowEnd:        windowStart.Add(1 * time.Hour),
			TotalUnits:       decimal.NewFromInt(10),
			PeakUnits:        decimal.NewFromInt(5),
			AvgUnits:         decimal.NewFromInt(5),
			ObservationCount: 2,
		},
	}

	buf.Restore(windows)

	snap := buf.Snapshot()
	require.Len(t, snap, 1)
	assert.True(t, snap[0].TotalUnits.Equal(decimal.NewFromInt(10)))
	assert.Equal(t, int64(2), snap[0].ObservationCount)
}

func TestAggregationBuffer_RestoreMergesWithExisting(t *testing.T) {
	buf := NewAggregationBuffer(1 * time.Hour)

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	buf.Add(testMeasurement("tenant-1", "svc", "Op", 3, ts))

	// Simulate failed flush: restore windows with same key
	windowStart := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	buf.Restore([]*UtilizationWindow{
		{
			ResolutionKey:    "tenant-1/svc/Op",
			InstrumentCode:   "TRANSACTION",
			WindowStart:      windowStart,
			WindowEnd:        windowStart.Add(1 * time.Hour),
			TotalUnits:       decimal.NewFromInt(7),
			PeakUnits:        decimal.NewFromInt(7),
			AvgUnits:         decimal.NewFromInt(7),
			ObservationCount: 1,
		},
	})

	snap := buf.Snapshot()
	require.Len(t, snap, 1)
	// 3 (existing) + 7 (restored) = 10
	assert.True(t, snap[0].TotalUnits.Equal(decimal.NewFromInt(10)))
	// peak: max(3, 7) = 7
	assert.True(t, snap[0].PeakUnits.Equal(decimal.NewFromInt(7)))
	// count: 1 + 1 = 2
	assert.Equal(t, int64(2), snap[0].ObservationCount)
}

func TestMarketDataPublisher_FlushFailureRequeuesData(t *testing.T) {
	mock := newMockMDSClient()
	mock.batchErr = status.Errorf(codes.Internal, "internal error")

	cfg := DefaultConfig()
	cfg.FlushInterval = 50 * time.Millisecond

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	defer pub.Stop()

	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	pub.Publish(testMeasurement("tenant-1", "svc", "Op", 5, ts))

	// Data should be re-queued on failure, so multiple flush attempts should occur
	// for the same single measurement (proving it was restored after each failure)
	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mock.callCount.Load() >= 3
		})
	require.NoError(t, err, "expected multiple flush attempts due to restore-on-failure")
}

// --- Idempotent Stop ---

func TestMarketDataPublisher_StopIsIdempotent(t *testing.T) {
	mock := newMockMDSClient()
	cfg := DefaultConfig()
	cfg.FlushInterval = 10 * time.Minute

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)

	// Multiple Stop calls should not panic
	pub.Stop()
	pub.Stop()
	pub.Stop()
}

// --- Partial failure handling ---

func TestMarketDataPublisher_PartialFailureRequeuesFailedOnly(t *testing.T) {
	mock := newMockMDSClient()

	// Configure mock to return partial failure: first observation succeeds, second fails
	mock.batchResponses = []*marketinformationv1.RecordObservationBatchResponse{
		{
			Results: []*marketinformationv1.BatchObservationResult{
				{Success: true, Index: 0},
				{Success: false, Index: 1, ErrorMessage: "dataset not found"},
			},
			TotalCount:   2,
			SuccessCount: 1,
			FailureCount: 1,
		},
	}

	cfg := DefaultConfig()
	cfg.FlushInterval = 10 * time.Minute // Manual flush via Stop
	cfg.WindowSize = 1 * time.Hour

	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)

	// Publish two measurements with different resolution keys to create two windows
	ts := time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC)
	pub.Publish(testMeasurement("tenant-1", "svc-a", "Op", 5, ts))
	pub.Publish(testMeasurement("tenant-2", "svc-b", "Op", 3, ts))

	assert.Equal(t, 2, pub.BufferSize())

	// Stop triggers flush
	pub.Stop()

	// Verify the batch was sent
	calls := mock.getBatchCalls()
	require.Len(t, calls, 1)
	require.Len(t, calls[0].Observations, 2)

	// The failed window (index 1) should be restored to the buffer
	assert.Equal(t, 1, pub.BufferSize(), "failed observation should be re-queued")
}

// --- Config normalization ---

func TestNewMarketDataPublisher_ConfigNormalization(t *testing.T) {
	mock := newMockMDSClient()

	// Zero config should use defaults, not panic
	cfg := Config{}
	pub, err := NewMarketDataPublisher(mock, cfg)
	require.NoError(t, err)
	require.NotNil(t, pub)
	pub.Stop()
}

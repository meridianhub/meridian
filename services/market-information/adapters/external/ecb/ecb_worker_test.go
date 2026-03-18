package ecb_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/external/ecb"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// Test sentinel errors for use in test cases.
var (
	errGRPC    = errors.New("gRPC error")
	errUnknown = errors.New("some unknown error")
)

// mockMarketInfoClient implements the MarketInformationClient interface for testing.
type mockMarketInfoClient struct {
	mu              sync.Mutex
	recordedObs     []*marketinformationv1.RecordObservationRequest
	recordErr       error
	recordCallCount atomic.Int32
}

func newMockMarketInfoClient() *mockMarketInfoClient {
	return &mockMarketInfoClient{
		recordedObs: make([]*marketinformationv1.RecordObservationRequest, 0),
	}
}

func (m *mockMarketInfoClient) RecordObservation(
	_ context.Context,
	req *marketinformationv1.RecordObservationRequest,
) (*marketinformationv1.RecordObservationResponse, error) {
	m.recordCallCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.recordErr != nil {
		return nil, m.recordErr
	}

	m.recordedObs = append(m.recordedObs, req)
	return &marketinformationv1.RecordObservationResponse{
		Observation: &marketinformationv1.MarketPriceObservation{
			Id: "test-obs-id",
		},
	}, nil
}

func (m *mockMarketInfoClient) getRecordedObservations() []*marketinformationv1.RecordObservationRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*marketinformationv1.RecordObservationRequest{}, m.recordedObs...)
}

func (m *mockMarketInfoClient) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordErr = err
}

// sampleECBCSV provides valid ECB CSV data for testing.
const sampleECBCSV = `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.0876
EXR.D.GBP.EUR.SP00.A,D,GBP,EUR,SP00,A,2024-01-15,0.8612
EXR.D.JPY.EUR.SP00.A,D,JPY,EUR,SP00,A,2024-01-15,160.52`

func TestNewWorker_Defaults(t *testing.T) {
	client := ecb.NewClient(ecb.Config{})
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{}, nil)
	require.NotNil(t, worker)
}

func TestNewWorker_CustomConfig(t *testing.T) {
	client := ecb.NewClient(ecb.Config{})
	mockClient := newMockMarketInfoClient()

	config := ecb.WorkerConfig{
		DatasetCode:   "CUSTOM_FX",
		SourceCode:    "CUSTOM_ECB",
		FetchInterval: 12 * time.Hour,
		MaxRetries:    5,
	}

	worker := ecb.NewWorker(client, mockClient, config, nil)
	require.NotNil(t, worker)
}

func TestDefaultWorkerConfig(t *testing.T) {
	config := ecb.DefaultWorkerConfig()

	assert.Equal(t, "ECB_FX", config.DatasetCode)
	assert.Equal(t, "ECB", config.SourceCode)
	assert.Equal(t, 24*time.Hour, config.FetchInterval)
	assert.Equal(t, 3, config.MaxRetries)
}

func TestWorker_StartStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 100 * time.Millisecond,
		SourceCode:    "ECB",
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for initial ingestion to complete using await
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockClient.recordCallCount.Load() >= 3
		})
	require.NoError(t, err, "expected at least 3 observations to be recorded")

	worker.Stop()

	// Verify observations were recorded
	obs := mockClient.getRecordedObservations()
	require.GreaterOrEqual(t, len(obs), 3)

	// Verify observation content
	foundUSD := false
	foundGBP := false
	for _, o := range obs {
		if o.DatasetCode == "USD_EUR_FX" {
			foundUSD = true
			assert.Equal(t, "ECB", o.SourceCode)
			assert.Equal(t, "1.0876", o.Value)
		}
		if o.DatasetCode == "GBP_EUR_FX" {
			foundGBP = true
			assert.Equal(t, "0.8612", o.Value)
		}
	}
	assert.True(t, foundUSD, "expected USD_EUR_FX observation")
	assert.True(t, foundGBP, "expected GBP_EUR_FX observation")
}

func TestWorker_StopIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour, // Long interval so we don't tick during test
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for initial ingestion
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockClient.recordCallCount.Load() >= 1
		})
	require.NoError(t, err)

	// Stop multiple times - should not panic
	worker.Stop()
	worker.Stop()
	worker.Stop()
}

func TestWorker_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 100 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for initial ingestion
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockClient.recordCallCount.Load() >= 1
		})
	require.NoError(t, err)

	// Cancel context
	cancel()

	// Worker should stop gracefully - use Stop() to wait for completion
	worker.Stop()
}

func TestWorker_FetchError(t *testing.T) {
	// Server returns error
	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 100 * time.Millisecond,
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait until the worker has made at least one fetch attempt before asserting
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return fetchCount.Load() >= 1
	})
	require.NoError(t, err, "worker should make at least one fetch attempt")

	worker.Stop()

	// Verify no observations were recorded due to fetch error
	obs := mockClient.getRecordedObservations()
	assert.Empty(t, obs)
}

func TestWorker_ParseError(t *testing.T) {
	// Server returns invalid CSV
	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid,csv,format"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 100 * time.Millisecond,
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait until the worker has made at least one fetch attempt before asserting
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return fetchCount.Load() >= 1
	})
	require.NoError(t, err, "worker should make at least one fetch attempt")

	worker.Stop()

	// Verify no observations were recorded due to parse error
	obs := mockClient.getRecordedObservations()
	assert.Empty(t, obs)
}

func TestWorker_RecordObservationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()
	mockClient.setError(errGRPC)

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour, // Long interval
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for attempt to record
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockClient.recordCallCount.Load() >= 3
		})
	require.NoError(t, err)

	worker.Stop()

	// Verify no observations were successfully recorded (all failed)
	obs := mockClient.getRecordedObservations()
	assert.Empty(t, obs)
}

func TestWorker_PartialRecordError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := &partialErrorMockClient{
		failOnCall: 2, // Fail on second call
	}

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour, // Long interval
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for all calls to complete
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockClient.callCount.Load() >= 3
		})
	require.NoError(t, err)

	worker.Stop()

	// Verify 2 observations were recorded (1st and 3rd succeeded, 2nd failed)
	obs := mockClient.getRecordedObservations()
	assert.Len(t, obs, 2)
}

// partialErrorMockClient fails on specific call numbers for testing partial failures.
type partialErrorMockClient struct {
	mu          sync.Mutex
	recordedObs []*marketinformationv1.RecordObservationRequest
	failOnCall  int32
	callCount   atomic.Int32
}

func (m *partialErrorMockClient) RecordObservation(
	_ context.Context,
	req *marketinformationv1.RecordObservationRequest,
) (*marketinformationv1.RecordObservationResponse, error) {
	callNum := m.callCount.Add(1)

	if callNum == m.failOnCall {
		return nil, errGRPC
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordedObs = append(m.recordedObs, req)

	return &marketinformationv1.RecordObservationResponse{
		Observation: &marketinformationv1.MarketPriceObservation{
			Id: "test-obs-id",
		},
	}, nil
}

func (m *partialErrorMockClient) getRecordedObservations() []*marketinformationv1.RecordObservationRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*marketinformationv1.RecordObservationRequest{}, m.recordedObs...)
}

func TestWorker_MultipleTickCycles(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 50 * time.Millisecond, // Short interval for testing
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for at least 3 fetch cycles (initial + 2 ticks)
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(25 * time.Millisecond).
		Until(func() bool {
			return callCount.Load() >= 3
		})
	require.NoError(t, err)

	worker.Stop()

	// Verify multiple fetch cycles occurred
	assert.GreaterOrEqual(t, callCount.Load(), int32(3))
}

func TestWorker_ObservationAttributes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour,
		SourceCode:    "ECB",
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for observations to be recorded
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockClient.recordCallCount.Load() >= 3
		})
	require.NoError(t, err)

	worker.Stop()

	obs := mockClient.getRecordedObservations()
	require.NotEmpty(t, obs)

	// Find USD observation and verify attributes
	var usdObs *marketinformationv1.RecordObservationRequest
	for _, o := range obs {
		if o.DatasetCode == "USD_EUR_FX" {
			usdObs = o
			break
		}
	}
	require.NotNil(t, usdObs, "expected USD_EUR_FX observation")

	// Verify attributes are set
	attrs := make(map[string]string)
	for _, attr := range usdObs.Attributes {
		attrs[attr.Key] = attr.Value
	}

	assert.Contains(t, attrs, "causation_id")
	assert.Contains(t, attrs["causation_id"], "ecb-feed-")
	assert.Equal(t, "D", attrs["frequency"])
	assert.Equal(t, "SP00", attrs["exchange_rate_type"])
	assert.Equal(t, "A", attrs["exchange_rate_suffix"])
	assert.Equal(t, "USD", attrs["base_currency"])
	assert.Equal(t, "EUR", attrs["quote_currency"])
}

func TestWorker_RateLimited(t *testing.T) {
	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 100 * time.Millisecond,
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait until the worker has made at least one fetch attempt before asserting
	err := await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
		return fetchCount.Load() >= 1
	})
	require.NoError(t, err, "worker should make at least one fetch attempt")

	worker.Stop()

	// Verify no observations were recorded due to rate limiting
	obs := mockClient.getRecordedObservations()
	assert.Empty(t, obs)
}

// retryCountingServer tracks fetch attempts and fails N times before succeeding.
type retryCountingServer struct {
	fetchCount   atomic.Int32
	failCount    int32 // Number of times to fail before succeeding
	failWithCode int   // HTTP status code to return on failure (default 500)
	csvData      string
}

func newRetryCountingServer(failCount int, csvData string) *retryCountingServer {
	return &retryCountingServer{
		failCount:    int32(failCount),
		failWithCode: http.StatusInternalServerError,
		csvData:      csvData,
	}
}

func (s *retryCountingServer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	attempt := s.fetchCount.Add(1)
	if attempt <= s.failCount {
		w.WriteHeader(s.failWithCode)
		_, _ = w.Write([]byte("server error"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.csvData))
}

func TestWorker_RetrySuccessAfterTransientFailure(t *testing.T) {
	// Server fails twice (500), then succeeds on third attempt
	retryServer := newRetryCountingServer(2, sampleECBCSV)
	server := httptest.NewServer(retryServer)
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour, // Long interval, we only care about initial fetch
		MaxRetries:    5,             // More than enough retries
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for retry logic to succeed (fail twice, succeed on third)
	// Backoff is 1s, 2s, so we need ~3s plus processing time
	err := await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return mockClient.recordCallCount.Load() >= 3
		})
	require.NoError(t, err, "expected observations to be recorded after retries")

	worker.Stop()

	// Verify server was called 3 times (2 failures + 1 success)
	assert.Equal(t, int32(3), retryServer.fetchCount.Load())

	// Verify observations were recorded successfully
	obs := mockClient.getRecordedObservations()
	assert.GreaterOrEqual(t, len(obs), 3)
}

func TestWorker_MaxRetriesExhausted(t *testing.T) {
	// Server always fails with 500
	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	maxRetries := 3
	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour,
		MaxRetries:    maxRetries,
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for all retries to be exhausted
	// With MaxRetries=3 and backoff of 1s, 2s between attempts, total time is ~3s
	err := await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return fetchCount.Load() >= int32(maxRetries)
		})
	require.NoError(t, err, "expected all retry attempts to be made")

	worker.Stop()

	// Verify server was called exactly MaxRetries times
	assert.Equal(t, int32(maxRetries), fetchCount.Load())

	// Verify no observations were recorded (all attempts failed)
	obs := mockClient.getRecordedObservations()
	assert.Empty(t, obs)
}

func TestWorker_ImmediateFailureOnPermanentError(t *testing.T) {
	// Server returns invalid CSV (parse error = permanent failure, no retry)
	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusOK)
		// Return invalid CSV header that won't parse
		_, _ = w.Write([]byte("invalid,csv,format"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour,
		MaxRetries:    5, // High retry count, but shouldn't be used for permanent errors
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait briefly for initial fetch to fail permanently
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return fetchCount.Load() >= 1
		})
	require.NoError(t, err)

	//nolint:forbidigo // Intentional: wait past first retry backoff (~1s) to verify no retry occurs for permanent error
	time.Sleep(1500 * time.Millisecond)

	worker.Stop()

	// Verify server was called only once (no retries for permanent parse error)
	assert.Equal(t, int32(1), fetchCount.Load())

	// Verify no observations were recorded
	obs := mockClient.getRecordedObservations()
	assert.Empty(t, obs)
}

func TestWorker_ContextCancellationDuringBackoff(t *testing.T) {
	// Server always fails to trigger backoff
	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour,
		MaxRetries:    10, // Many retries, but we'll cancel during first backoff
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for first fetch attempt
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return fetchCount.Load() >= 1
		})
	require.NoError(t, err)

	// Cancel context during backoff wait (before second attempt)
	// First backoff is 1s, so cancel immediately after first failure
	//nolint:forbidigo // Intentional: timed delay to ensure cancellation lands during backoff window, not before first attempt
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Worker should stop gracefully
	worker.Stop()

	// Should have made 1-2 attempts max (cancelled during or shortly after first backoff)
	attempts := fetchCount.Load()
	assert.LessOrEqual(t, attempts, int32(2), "expected at most 2 attempts before cancellation")
}

func TestWorker_RetryOnRateLimiting(t *testing.T) {
	// Server returns 429 twice, then succeeds
	var fetchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := fetchCount.Add(1)
		if attempt <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleECBCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))
	mockClient := newMockMarketInfoClient()

	worker := ecb.NewWorker(client, mockClient, ecb.WorkerConfig{
		FetchInterval: 1 * time.Hour,
		MaxRetries:    5,
	}, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Wait for successful ingestion after rate limiting retries
	err := await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return mockClient.recordCallCount.Load() >= 3
		})
	require.NoError(t, err, "expected observations after rate limit retries")

	worker.Stop()

	// Verify server was called 3 times (2 rate limits + 1 success)
	assert.Equal(t, int32(3), fetchCount.Load())

	// Verify observations were recorded
	obs := mockClient.getRecordedObservations()
	assert.GreaterOrEqual(t, len(obs), 3)
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "nil error",
			err:       nil,
			retryable: false,
		},
		{
			name:      "rate limited error",
			err:       ecb.ErrRateLimited,
			retryable: true,
		},
		{
			name:      "wrapped rate limited error",
			err:       fmt.Errorf("fetch failed: %w", ecb.ErrRateLimited),
			retryable: true, // Rate limited errors are retryable
		},
		{
			name:      "API error (5xx)",
			err:       ecb.ErrAPIError,
			retryable: true,
		},
		{
			name:      "invalid CSV format",
			err:       ecb.ErrInvalidCSVFormat,
			retryable: false,
		},
		{
			name:      "no data error",
			err:       ecb.ErrNoData,
			retryable: false,
		},
		{
			name:      "not configured error",
			err:       ecb.ErrNotConfigured,
			retryable: false,
		},
		{
			name:      "context canceled",
			err:       context.Canceled,
			retryable: false,
		},
		{
			name:      "context deadline exceeded",
			err:       context.DeadlineExceeded,
			retryable: false,
		},
		{
			name:      "generic error defaults to retryable",
			err:       errUnknown,
			retryable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ecb.IsRetryableError(tc.err)
			assert.Equal(t, tc.retryable, result)
		})
	}
}

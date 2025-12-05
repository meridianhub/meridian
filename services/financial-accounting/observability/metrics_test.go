package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockUnaryHandler is a test handler that can be configured to return success or error
type mockUnaryHandler struct {
	resp interface{}
	err  error
}

func (m *mockUnaryHandler) handle(_ context.Context, _ interface{}) (interface{}, error) {
	return m.resp, m.err
}

func TestMetricsInterceptor_Success(t *testing.T) {
	// Create test metrics
	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_fa_requests_total",
			Help: "Test counter",
		},
		[]string{"method", "status"},
	)
	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "test_fa_request_duration_seconds",
			Help:    "Test histogram",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	// Create interceptor
	interceptor := MetricsInterceptor(requestsTotal, requestDuration)

	// Mock handler that succeeds
	handler := &mockUnaryHandler{
		resp: "success",
		err:  nil,
	}

	// Create test context and info
	ctx := context.Background()
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/TestMethod",
	}

	// Execute interceptor
	resp, err := interceptor(ctx, "test-request", info, handler.handle)
	// Verify response
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if resp != "success" {
		t.Errorf("Expected response 'success', got: %v", resp)
	}

	// Verify metrics were recorded
	metric, err := requestsTotal.GetMetricWithLabelValues("/test.Service/TestMethod", "OK")
	if err != nil {
		t.Errorf("Failed to get metric: %v", err)
	}
	if metric == nil {
		t.Error("Expected metric to be created")
	}
}

func TestMetricsInterceptor_Error(t *testing.T) {
	// Create test metrics
	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_fa_requests_total_error",
			Help: "Test counter",
		},
		[]string{"method", "status"},
	)
	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "test_fa_request_duration_seconds_error",
			Help:    "Test histogram",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	// Create interceptor
	interceptor := MetricsInterceptor(requestsTotal, requestDuration)

	// Mock handler that returns gRPC error
	testErr := status.Error(codes.InvalidArgument, "test error")
	handler := &mockUnaryHandler{
		resp: nil,
		err:  testErr,
	}

	// Create test context and info
	ctx := context.Background()
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/ErrorMethod",
	}

	// Execute interceptor
	resp, err := interceptor(ctx, "test-request", info, handler.handle)

	// Verify response
	if err == nil {
		t.Error("Expected error, got nil")
	}
	if !errors.Is(err, testErr) {
		t.Errorf("Expected error to be testErr, got: %v", err)
	}
	if resp != nil {
		t.Errorf("Expected nil response, got: %v", resp)
	}

	// Verify metrics were recorded with error status
	metric, err := requestsTotal.GetMetricWithLabelValues("/test.Service/ErrorMethod", "InvalidArgument")
	if err != nil {
		t.Errorf("Failed to get metric: %v", err)
	}
	if metric == nil {
		t.Error("Expected metric to be created")
	}
}

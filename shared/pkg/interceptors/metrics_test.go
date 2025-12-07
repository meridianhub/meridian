package interceptors

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMetricsInterceptor_Success(t *testing.T) {
	// Create test metrics
	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_shared_requests_total",
			Help: "Test counter",
		},
		[]string{"method", "status"},
	)
	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "test_shared_request_duration_seconds",
			Help:    "Test histogram",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	// Create interceptor
	interceptor := MetricsInterceptor(requestsTotal, requestDuration)

	// Mock handler that succeeds
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "success", nil
	}

	// Create test context and info
	ctx := context.Background()
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/TestMethod",
	}

	// Execute interceptor
	resp, err := interceptor(ctx, "test-request", info, handler)
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
			Name: "test_shared_requests_total_error",
			Help: "Test counter",
		},
		[]string{"method", "status"},
	)
	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "test_shared_request_duration_seconds_error",
			Help:    "Test histogram",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	// Create interceptor
	interceptor := MetricsInterceptor(requestsTotal, requestDuration)

	// Mock handler that returns gRPC error
	testErr := status.Error(codes.InvalidArgument, "test error")
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return nil, testErr
	}

	// Create test context and info
	ctx := context.Background()
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/ErrorMethod",
	}

	// Execute interceptor
	resp, err := interceptor(ctx, "test-request", info, handler)

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

func TestMetricsInterceptor_RecordsDuration(t *testing.T) {
	// Create test metrics
	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_shared_requests_total_duration",
			Help: "Test counter",
		},
		[]string{"method", "status"},
	)
	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "test_shared_request_duration_seconds_duration",
			Help:    "Test histogram",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	// Create interceptor
	interceptor := MetricsInterceptor(requestsTotal, requestDuration)

	// Mock handler that succeeds
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "success", nil
	}

	// Create test context and info
	ctx := context.Background()
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/DurationMethod",
	}

	// Execute interceptor
	_, _ = interceptor(ctx, "test-request", info, handler)

	// Verify duration histogram was recorded
	metric, err := requestDuration.GetMetricWithLabelValues("/test.Service/DurationMethod")
	if err != nil {
		t.Errorf("Failed to get duration metric: %v", err)
	}
	if metric == nil {
		t.Error("Expected duration metric to be created")
	}
}

func TestMetricsInterceptor_StatusCodeMapping(t *testing.T) {
	tests := []struct {
		name           string
		handlerErr     error
		expectedStatus string
	}{
		{
			name:           "OK status",
			handlerErr:     nil,
			expectedStatus: "OK",
		},
		{
			name:           "NotFound status",
			handlerErr:     status.Error(codes.NotFound, "not found"),
			expectedStatus: "NotFound",
		},
		{
			name:           "PermissionDenied status",
			handlerErr:     status.Error(codes.PermissionDenied, "denied"),
			expectedStatus: "PermissionDenied",
		},
		{
			name:           "Internal status",
			handlerErr:     status.Error(codes.Internal, "internal error"),
			expectedStatus: "Internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test metrics with unique names per test
			requestsTotal := prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "test_shared_requests_total_" + tt.name,
					Help: "Test counter",
				},
				[]string{"method", "status"},
			)
			requestDuration := prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "test_shared_request_duration_seconds_" + tt.name,
					Help:    "Test histogram",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"method"},
			)

			// Create interceptor
			interceptor := MetricsInterceptor(requestsTotal, requestDuration)

			// Mock handler
			handler := func(_ context.Context, _ interface{}) (interface{}, error) {
				return "response", tt.handlerErr
			}

			// Create test context and info
			ctx := context.Background()
			info := &grpc.UnaryServerInfo{
				FullMethod: "/test.Service/StatusMethod",
			}

			// Execute interceptor
			_, _ = interceptor(ctx, "test-request", info, handler)

			// Verify correct status code was recorded
			metric, err := requestsTotal.GetMetricWithLabelValues("/test.Service/StatusMethod", tt.expectedStatus)
			if err != nil {
				t.Errorf("Failed to get metric for status %s: %v", tt.expectedStatus, err)
			}
			if metric == nil {
				t.Errorf("Expected metric to be created for status %s", tt.expectedStatus)
			}
		})
	}
}

// Package observability provides Prometheus metrics and health checks for the position-keeping service.
package observability

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// MetricsInterceptor creates a gRPC unary interceptor that records Prometheus metrics.
func MetricsInterceptor(requestsTotal *prometheus.CounterVec, requestDuration *prometheus.HistogramVec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()

		// Call the handler
		resp, err := handler(ctx, req)

		// Record duration
		duration := time.Since(start).Seconds()
		requestDuration.WithLabelValues(info.FullMethod).Observe(duration)

		// Record request count with status
		statusCode := "OK"
		if err != nil {
			st, _ := status.FromError(err)
			statusCode = st.Code().String()
		}
		requestsTotal.WithLabelValues(info.FullMethod, statusCode).Inc()

		return resp, err
	}
}

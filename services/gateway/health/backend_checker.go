package health

import (
	"context"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// CheckClient defines the interface for gRPC health checking.
// This interface exists to allow testing with mocks, since grpc_health_v1.HealthClient
// has methods with unexported parameters that cannot be easily mocked.
type CheckClient interface {
	Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest, opts ...interface{}) (*grpc_health_v1.HealthCheckResponse, error)
}

// grpcHealthClientAdapter wraps grpc_health_v1.HealthClient to implement HealthCheckClient.
type grpcHealthClientAdapter struct {
	client grpc_health_v1.HealthClient
}

// NewGRPCHealthClientAdapter creates a new adapter for a gRPC health client.
func NewGRPCHealthClientAdapter(client grpc_health_v1.HealthClient) CheckClient {
	return &grpcHealthClientAdapter{client: client}
}

func (a *grpcHealthClientAdapter) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest, _ ...interface{}) (*grpc_health_v1.HealthCheckResponse, error) {
	return a.client.Check(ctx, req)
}

// BackendServiceChecker checks the health of a backend gRPC service.
// Backend services are optional dependencies - if unavailable, the gateway
// operates in degraded mode (returning 502 for affected routes).
type BackendServiceChecker struct {
	name         string
	healthClient CheckClient
	timeout      time.Duration
}

// NewBackendServiceChecker creates a new backend service health checker.
//
// Parameters:
//   - name: The service name used for identification in health reports
//   - healthClient: A gRPC health client for the backend service
//   - timeout: Maximum time to wait for health check response
//
// Backend services are optional - failures result in StatusDegraded rather than
// StatusUnhealthy, allowing the gateway to remain ready for routes not requiring
// that backend.
func NewBackendServiceChecker(name string, healthClient CheckClient, timeout time.Duration) *BackendServiceChecker {
	return &BackendServiceChecker{
		name:         name,
		healthClient: healthClient,
		timeout:      timeout,
	}
}

// Name returns the service name used for this checker.
func (b *BackendServiceChecker) Name() string {
	return b.name
}

// Check performs a health check on the backend service using the standard
// gRPC health check protocol.
//
// Health status mapping:
//   - SERVING: StatusHealthy
//   - NOT_SERVING, SERVICE_UNKNOWN: StatusDegraded
//   - Connection error or timeout: StatusDegraded
//
// Backend services are optional dependencies, so failures never return
// StatusUnhealthy. This allows the gateway to remain ready while individual
// backends may be unavailable.
func (b *BackendServiceChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	// Create timeout context for the health check
	checkCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	resp, err := b.healthClient.Check(checkCtx, &grpc_health_v1.HealthCheckRequest{
		Service: "", // Empty service name checks overall health
	})
	responseTime := time.Since(start)

	// Backend services are optional - degrade gracefully if unavailable
	status := health.StatusHealthy
	message := fmt.Sprintf("%s service reachable", b.name)

	if err != nil {
		status = health.StatusDegraded
		if checkCtx.Err() != nil {
			message = fmt.Sprintf("%s service timeout after %s (degraded)", b.name, b.timeout)
			err = checkCtx.Err()
		} else {
			message = fmt.Sprintf("%s service unreachable (degraded): %v", b.name, err)
		}
	} else if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		status = health.StatusDegraded
		message = fmt.Sprintf("%s service not serving (status: %s)", b.name, resp.Status.String())
	}

	return health.ComponentResult{
		Name:         b.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}

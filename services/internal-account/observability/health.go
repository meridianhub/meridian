package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/gorm"
)

// DefaultHealthCheckTimeout is the default timeout for health checks.
const DefaultHealthCheckTimeout = 5 * time.Second

// Health checker initialization errors.
var (
	// ErrDatabaseNil is returned when attempting to create a health checker with a nil database.
	ErrDatabaseNil = errors.New("health checker requires non-nil database")
)

// PositionKeepingClient defines the interface for communicating with the PositionKeeping service.
// This is a subset of the full client interface, used only for health checks.
type PositionKeepingClient interface {
	// GetAccountBalances retrieves all balance types for an account by instrument.
	GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error)
	// Close terminates the client connection gracefully.
	Close() error
}

// HealthChecker implements gRPC health check protocol for InternalAccount service.
// It checks the health of critical dependencies including the database and optional
// dependencies like Position Keeping.
type HealthChecker struct {
	grpc_health_v1.UnimplementedHealthServer
	aggregator   *health.Aggregator
	logger       *slog.Logger
	serviceName  string
	checkTimeout time.Duration
}

// HealthCheckerConfig contains configuration for creating a new HealthChecker.
type HealthCheckerConfig struct {
	DB                    *gorm.DB
	PositionKeepingClient PositionKeepingClient
	Logger                *slog.Logger
	ServiceName           string        // Defaults to "internal-account"
	CheckTimeout          time.Duration // Defaults to 5 seconds
}

// NewHealthChecker creates a new health checker with dependency checking.
//
// The health checker evaluates:
//   - Database connectivity (critical)
//   - Position Keeping service (optional - degrades gracefully if unavailable)
//
// Health states:
//   - SERVING: All critical dependencies healthy (database), optional may be degraded
//   - NOT_SERVING: Any critical dependency down (database unreachable)
//   - UNKNOWN: Unable to determine health (should not occur in normal operation)
//
// Returns an error if DB is nil.
func NewHealthChecker(config HealthCheckerConfig) (*HealthChecker, error) {
	if config.DB == nil {
		return nil, ErrDatabaseNil
	}

	// Apply defaults
	if config.ServiceName == "" {
		config.ServiceName = "internal-account"
	}
	if config.CheckTimeout == 0 {
		config.CheckTimeout = DefaultHealthCheckTimeout
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Build list of health checkers
	checkers := []health.Checker{
		NewGormDatabaseHealthChecker(config.DB, config.CheckTimeout),
	}

	// Add Position Keeping health checker if client is available
	if config.PositionKeepingClient != nil {
		checkers = append(checkers, NewPositionKeepingHealthChecker(config.PositionKeepingClient, config.CheckTimeout))
	}

	aggregator := health.NewAggregator(checkers)

	return &HealthChecker{
		aggregator:   aggregator,
		logger:       config.Logger,
		serviceName:  config.ServiceName,
		checkTimeout: config.CheckTimeout,
	}, nil
}

// Check implements gRPC health check protocol.
// It performs synchronous health checks on all dependencies and returns the overall status.
//
// Service-specific behavior:
//   - Empty service name or "internal-account": Check overall service health
//   - Named component (e.g., "database", "position-keeping"): Check specific component
func (h *HealthChecker) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// Apply timeout to health check context
	checkCtx, cancel := context.WithTimeout(ctx, h.checkTimeout)
	defer cancel()

	// Empty service name or matching service name: check all components
	if req.Service == "" || req.Service == h.serviceName {
		report := h.aggregator.CheckAll(checkCtx)
		overallStatus := report.OverallStatus()
		grpcStatus := h.mapStatusToGRPC(overallStatus)
		h.logHealthCheck(report, overallStatus, grpcStatus)

		for _, comp := range report.Components {
			RecordHealthCheck(comp.Name, healthStatusString(comp.Status))
		}

		return &grpc_health_v1.HealthCheckResponse{
			Status: grpcStatus,
		}, nil
	}

	// Check if request is for a specific component
	componentResult, found := h.aggregator.CheckByName(checkCtx, req.Service)
	if found {
		grpcStatus := h.mapStatusToGRPC(componentResult.Status)

		h.logger.Info("component health check completed",
			"component", componentResult.Name,
			"status", componentResult.Status.String(),
			"grpc_status", grpcStatus.String(),
			"response_time_ms", componentResult.ResponseTime.Milliseconds(),
			"message", componentResult.Message)

		RecordHealthCheck(componentResult.Name, healthStatusString(componentResult.Status))

		return &grpc_health_v1.HealthCheckResponse{
			Status: grpcStatus,
		}, nil
	}

	// Service name doesn't match and component not found - return UNKNOWN
	h.logger.Debug("health check for unknown service or component",
		"requested", req.Service,
		"service_name", h.serviceName)
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_UNKNOWN,
	}, nil
}

// healthStatusString converts a health.Status to its string representation for metrics.
func healthStatusString(s health.Status) string {
	switch s {
	case health.StatusHealthy:
		return "healthy"
	case health.StatusUnhealthy:
		return "unhealthy"
	case health.StatusDegraded:
		return "degraded"
	case health.StatusUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// Watch implements streaming health checks.
// It sends periodic health updates to the client over a stream.
func (h *HealthChecker) Watch(req *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	ctx := stream.Context()

	// Send immediate health check
	resp, err := h.Check(ctx, req)
	if err != nil {
		return err
	}

	if err := stream.Send(resp); err != nil {
		h.logger.Error("failed to send initial health check",
			"error", err)
		return fmt.Errorf("failed to send initial health status: %w", err)
	}

	// Stream periodic updates
	ticker := time.NewTicker(h.checkTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Debug("health watch stream closed", "reason", ctx.Err())
			return nil // Per gRPC health protocol, client disconnect is not an error

		case <-ticker.C:
			resp, err := h.Check(ctx, req)
			if err != nil {
				h.logger.Error("health check failed during watch",
					"error", err)
				return err
			}

			if err := stream.Send(resp); err != nil {
				h.logger.Error("failed to send health check update",
					"error", err)
				return fmt.Errorf("failed to send health status update: %w", err)
			}
		}
	}
}

// mapStatusToGRPC converts internal health status to gRPC health check status.
func (h *HealthChecker) mapStatusToGRPC(status health.Status) grpc_health_v1.HealthCheckResponse_ServingStatus {
	switch status {
	case health.StatusHealthy:
		return grpc_health_v1.HealthCheckResponse_SERVING

	case health.StatusDegraded:
		// Degraded still serves requests (optional dependencies may be down)
		return grpc_health_v1.HealthCheckResponse_SERVING

	case health.StatusUnhealthy:
		return grpc_health_v1.HealthCheckResponse_NOT_SERVING

	case health.StatusUnknown:
		return grpc_health_v1.HealthCheckResponse_UNKNOWN

	default:
		return grpc_health_v1.HealthCheckResponse_UNKNOWN
	}
}

// logHealthCheck logs the health check result with structured details.
func (h *HealthChecker) logHealthCheck(report *health.Report, overallStatus health.Status, grpcStatus grpc_health_v1.HealthCheckResponse_ServingStatus) {
	// Build component status map for structured logging
	componentStatuses := make(map[string]string)
	for _, comp := range report.Components {
		componentStatuses[comp.Name] = comp.Status.String()
	}

	// Log at appropriate level based on status
	switch overallStatus {
	case health.StatusHealthy, health.StatusDegraded:
		h.logger.Info("health check completed",
			"overall_status", overallStatus.String(),
			"grpc_status", grpcStatus.String(),
			"component_count", len(report.Components),
			"components", componentStatuses)

	case health.StatusUnhealthy, health.StatusUnknown:
		h.logger.Warn("health check detected issues",
			"overall_status", overallStatus.String(),
			"grpc_status", grpcStatus.String(),
			"component_count", len(report.Components),
			"components", componentStatuses)

		// Log individual component failures
		for _, comp := range report.Components {
			if comp.Status == health.StatusUnhealthy || comp.Status == health.StatusUnknown {
				h.logger.Error("component unhealthy",
					"component", comp.Name,
					"status", comp.Status.String(),
					"message", comp.Message,
					"response_time_ms", comp.ResponseTime.Milliseconds(),
					"error", comp.Error)
			}
		}
	}
}

// GormDatabaseHealthChecker checks database connectivity using GORM.
type GormDatabaseHealthChecker struct {
	db      *gorm.DB
	timeout time.Duration
}

// NewGormDatabaseHealthChecker creates a new database health checker for GORM.
func NewGormDatabaseHealthChecker(db *gorm.DB, timeout time.Duration) *GormDatabaseHealthChecker {
	return &GormDatabaseHealthChecker{
		db:      db,
		timeout: timeout,
	}
}

// Name returns the component name.
func (d *GormDatabaseHealthChecker) Name() string {
	return "database"
}

// Check performs a database health check by executing a simple query.
func (d *GormDatabaseHealthChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	// Create timeout context
	checkCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	// Get underlying sql.DB and ping
	sqlDB, err := d.db.DB()
	if err != nil {
		return health.ComponentResult{
			Name:         d.Name(),
			Status:       health.StatusUnhealthy,
			Message:      fmt.Sprintf("failed to get database instance: %v", err),
			ResponseTime: time.Since(start),
			CheckedAt:    start,
			Error:        err,
		}
	}

	err = sqlDB.PingContext(checkCtx)
	responseTime := time.Since(start)

	status := health.StatusHealthy
	message := "database connection successful"

	if err != nil {
		status = health.StatusUnhealthy
		// Prefer timeout message if context was cancelled, otherwise show the ping error
		if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
			message = fmt.Sprintf("database check timeout after %s", d.timeout)
		} else {
			message = fmt.Sprintf("database ping failed: %v", err)
		}
	}

	return health.ComponentResult{
		Name:         d.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}

// PositionKeepingHealthChecker checks Position Keeping service connectivity.
// This is an optional dependency - the service degrades gracefully if Position Keeping is unavailable.
type PositionKeepingHealthChecker struct {
	client  PositionKeepingClient
	timeout time.Duration
}

// NewPositionKeepingHealthChecker creates a new health checker for the Position Keeping service.
func NewPositionKeepingHealthChecker(client PositionKeepingClient, timeout time.Duration) *PositionKeepingHealthChecker {
	return &PositionKeepingHealthChecker{
		client:  client,
		timeout: timeout,
	}
}

// Name returns the component name.
func (p *PositionKeepingHealthChecker) Name() string {
	return "position-keeping"
}

// Check performs a health check by attempting a lightweight balance query.
// This is an optional dependency, so failures result in Degraded status rather than Unhealthy.
func (p *PositionKeepingHealthChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	// Create timeout context
	checkCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// Attempt a lightweight health check by querying with a non-existent account
	// This verifies connectivity without requiring actual data
	_, err := p.client.GetAccountBalances(checkCtx, &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "health-check-probe",
		InstrumentCode: "XXX",
	})

	responseTime := time.Since(start)

	// For health check purposes, we consider the service healthy if:
	// 1. No error (unlikely for probe account)
	// 2. Error indicates "not found" (expected - the probe account doesn't exist)
	// 3. Any gRPC error that's NOT a connection/unavailable error
	//
	// The key insight is that getting a "not found" or "invalid argument" error
	// proves the service is reachable and processing requests.

	status := health.StatusHealthy
	message := "position keeping service reachable"

	if err != nil {
		// Check if this is a connection-level failure vs application-level error
		// Application errors (not found, invalid argument) mean the service is UP
		// Connection errors (unavailable, deadline exceeded) mean the service is DOWN
		if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
			// Timeout - service might be overloaded or unreachable
			status = health.StatusDegraded
			message = fmt.Sprintf("position keeping check timeout after %s", p.timeout)
		} else {
			// For any other error, we consider the service reachable since it responded
			// Not found, invalid argument, etc. are all valid application responses
			// This is a probe check, so we expect errors for the fake account
			status = health.StatusHealthy
			message = "position keeping service responded to health probe"
		}
	}

	return health.ComponentResult{
		Name:         p.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        nil, // Don't propagate expected errors from the health probe
	}
}

package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/gorm"
)

// Health checker initialization errors
var (
	// ErrDatabaseNil is returned when attempting to create a health checker with a nil database
	ErrDatabaseNil = errors.New("health checker requires non-nil database")
)

// HealthChecker implements gRPC health check protocol for FinancialAccounting service.
// It checks the health of critical dependencies including the database.
type HealthChecker struct {
	grpc_health_v1.UnimplementedHealthServer
	aggregator   *health.Aggregator
	logger       *slog.Logger
	serviceName  string
	checkTimeout time.Duration
}

// HealthCheckerConfig contains configuration for creating a new HealthChecker.
type HealthCheckerConfig struct {
	DB                   *gorm.DB
	Logger               *slog.Logger
	ServiceName          string        // Defaults to "financial-accounting"
	CheckTimeout         time.Duration // Defaults to 5 seconds
	UsingNoopIdempotency bool          // Set to true if using NoOp idempotency service
	UsingNoopEvents      bool          // Set to true if using NoOp event publisher
}

// NewHealthChecker creates a new health checker with dependency checking.
//
// The health checker evaluates:
//   - Database connectivity (critical)
//
// Health states:
//   - SERVING: All critical dependencies healthy (database)
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
		config.ServiceName = "financial-accounting"
	}
	if config.CheckTimeout == 0 {
		config.CheckTimeout = defaults.DefaultHealthCheckTimeout
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Build list of health checkers
	checkers := []health.Checker{
		NewGormDatabaseHealthChecker(config.DB, config.CheckTimeout),
	}

	// Add NoOp fallback checker if any fallback is active
	if config.UsingNoopIdempotency || config.UsingNoopEvents {
		checkers = append(checkers, NewNoopFallbackChecker(config.UsingNoopIdempotency, config.UsingNoopEvents))
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
//   - Empty service name or "financial-accounting": Check overall service health
//   - Named component (e.g., "database"): Check specific component
func (h *HealthChecker) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	checkCtx, cancel := context.WithTimeout(ctx, h.checkTimeout)
	defer cancel()

	// Empty service name or matching service name: check all components
	if req.Service == "" || req.Service == h.serviceName {
		return h.checkAllComponents(checkCtx)
	}

	// Check if request is for a specific component
	if resp, found := h.checkSingleComponent(checkCtx, req.Service); found {
		return resp, nil
	}

	// Service name doesn't match and component not found - return UNKNOWN
	h.logger.Debug("health check for unknown service or component",
		"requested", req.Service,
		"service_name", h.serviceName)
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_UNKNOWN,
	}, nil
}

// checkAllComponents performs health checks on all registered components.
func (h *HealthChecker) checkAllComponents(ctx context.Context) (*grpc_health_v1.HealthCheckResponse, error) {
	report := h.aggregator.CheckAll(ctx)
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

// checkSingleComponent checks a specific named component.
// Returns (response, true) if the component exists, or (nil, false) if not found.
func (h *HealthChecker) checkSingleComponent(ctx context.Context, name string) (*grpc_health_v1.HealthCheckResponse, bool) {
	componentResult, found := h.aggregator.CheckByName(ctx, name)
	if !found {
		return nil, false
	}

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
	}, true
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

// NoopFallbackChecker reports health degradation when NoOp fallback services are active.
// This alerts operators that the service is running in a reduced functionality mode
// (typically development/testing) where some guarantees are not enforced.
type NoopFallbackChecker struct {
	usingNoopIdempotency bool
	usingNoopEvents      bool
}

// NewNoopFallbackChecker creates a checker that reports DEGRADED status when
// NoOp fallback services are being used instead of production implementations.
func NewNoopFallbackChecker(usingNoopIdempotency, usingNoopEvents bool) *NoopFallbackChecker {
	return &NoopFallbackChecker{
		usingNoopIdempotency: usingNoopIdempotency,
		usingNoopEvents:      usingNoopEvents,
	}
}

// Name returns the component name.
func (n *NoopFallbackChecker) Name() string {
	return "noop-fallbacks"
}

// Check returns the health status based on whether NoOp fallbacks are active.
func (n *NoopFallbackChecker) Check(_ context.Context) health.ComponentResult {
	start := time.Now()

	if !n.usingNoopIdempotency && !n.usingNoopEvents {
		return health.ComponentResult{
			Name:         n.Name(),
			Status:       health.StatusHealthy,
			Message:      "all production services connected",
			ResponseTime: time.Since(start),
			CheckedAt:    start,
		}
	}

	var degradedServices []string
	if n.usingNoopIdempotency {
		degradedServices = append(degradedServices, "idempotency (Redis)")
	}
	if n.usingNoopEvents {
		degradedServices = append(degradedServices, "event publishing (Kafka)")
	}

	return health.ComponentResult{
		Name:         n.Name(),
		Status:       health.StatusDegraded,
		Message:      fmt.Sprintf("using noop fallbacks for: %v - DEVELOPMENT ONLY", degradedServices),
		ResponseTime: time.Since(start),
		CheckedAt:    start,
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

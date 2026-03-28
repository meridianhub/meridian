// Package handler implements gRPC service handlers for the Forecasting service.
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	forecastingv1 "github.com/meridianhub/meridian/api/proto/meridian/forecasting/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/forecasting/domain"
	"github.com/meridianhub/meridian/services/forecasting/starlark"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// maxBatchSize is the maximum number of observations per MDS batch publish.
const maxBatchSize = 1000

// Service construction errors.
var (
	ErrRepoRequired     = errors.New("strategy repository is required")
	ErrRunnerRequired   = errors.New("forecast runner is required")
	ErrMDSRequired      = errors.New("MDS publisher is required")
	ErrBatchPublish     = errors.New("batch publish failed")
	ErrNotActive        = errors.New("strategy is not in ACTIVE status")
	ErrTenantIDRequired = errors.New("tenant context is required")
)

// MDSPublisher abstracts the Market Data Service for publishing observations.
type MDSPublisher interface {
	RecordObservationBatch(
		ctx context.Context,
		observations []*marketinformationv1.BatchObservationEntry,
	) (*marketinformationv1.RecordObservationBatchResponse, error)
}

// Service implements the ForecastingService gRPC service.
type Service struct {
	forecastingv1.UnimplementedForecastingServiceServer
	repo   domain.StrategyRepository
	runner *starlark.ForecastRunner
	mds    MDSPublisher
	logger *slog.Logger
}

// NewService creates a new forecasting service handler.
func NewService(
	repo domain.StrategyRepository,
	runner *starlark.ForecastRunner,
	mds MDSPublisher,
	logger *slog.Logger,
) (*Service, error) {
	if repo == nil {
		return nil, ErrRepoRequired
	}
	if runner == nil {
		return nil, ErrRunnerRequired
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		repo:   repo,
		runner: runner,
		mds:    mds,
		logger: logger,
	}, nil
}

// ComputeForwardCurve executes a forecasting strategy and publishes results to MDS.
func (s *Service) ComputeForwardCurve(ctx context.Context, req *forecastingv1.ComputeForwardCurveRequest) (*forecastingv1.ComputeForwardCurveResponse, error) {
	strategyID, err := uuid.Parse(req.GetStrategyId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid strategy_id: %v", err)
	}

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "tenant context required")
	}

	strategy, err := s.repo.FindByID(ctx, strategyID)
	if err != nil {
		return nil, s.mapDomainError(err, strategyID)
	}

	if strategy.Status() != domain.StrategyStatusActive {
		return nil, status.Errorf(codes.FailedPrecondition,
			"strategy %s is in %s status, must be ACTIVE", strategyID, strategy.Status())
	}

	now := time.Now().UTC()
	if req.GetExecutionTime() != nil {
		now = req.GetExecutionTime().AsTime()
	}

	s.logger.Info("computing forward curve",
		"strategy_id", strategyID,
		"strategy_name", strategy.Name(),
		"strategy_version", strategy.Version(),
		"tenant_id", string(tenantID),
		"execution_time", now,
	)

	input := buildStrategyInput(strategy, now, tenantID)

	computeStart := time.Now()
	points, err := s.runner.ExecuteStrategy(ctx, input)
	if err != nil {
		s.logger.Error("starlark execution failed", "strategy_id", strategyID, "error", err)
		return nil, status.Errorf(codes.Internal, "starlark execution failed: %v", err)
	}
	computeDuration := time.Since(computeStart)

	s.logger.Info("starlark execution completed",
		"strategy_id", strategyID,
		"point_count", len(points),
		"computation_ms", computeDuration.Milliseconds(),
	)

	if err := s.publishToMDS(ctx, strategy, points, now); err != nil {
		s.logger.Error("MDS publish failed", "strategy_id", strategyID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to publish forecast points to MDS: %v", err)
	}

	return buildForwardCurveResponse(strategy, strategyID, points, now, computeDuration), nil
}

// buildStrategyInput constructs the StrategyInput from the strategy domain object and execution context.
func buildStrategyInput(strategy domain.ForecastingStrategy, now time.Time, tenantID tenant.TenantID) starlark.StrategyInput {
	input := starlark.StrategyInput{
		Script:            strategy.StarlarkCode(),
		InputDatasetCodes: strategy.InputDatasetCodes(),
		OutputDatasetCode: strategy.OutputDatasetCode(),
		HorizonHours:      strategy.HorizonHours(),
		GranularityHours:  strategy.GranularityHours(),
		Now:               now,
	}
	if strategy.ReferenceDataResolutionKey() != "" {
		input.ResolutionKey = strategy.ReferenceDataResolutionKey()
		input.TenantID = string(tenantID)
	}
	return input
}

// buildForwardCurveResponse constructs the gRPC response from execution results.
func buildForwardCurveResponse(
	strategy domain.ForecastingStrategy,
	strategyID uuid.UUID,
	points []starlark.ForecastPoint,
	now time.Time,
	computeDuration time.Duration,
) *forecastingv1.ComputeForwardCurveResponse {
	horizon := time.Duration(strategy.HorizonHours()) * time.Hour
	granularity := time.Duration(strategy.GranularityHours()) * time.Hour

	protoPoints := make([]*forecastingv1.ForecastPointProto, len(points))
	for i, p := range points {
		protoPoints[i] = &forecastingv1.ForecastPointProto{
			Timestamp: timestamppb.New(p.Timestamp),
			Value:     p.Value.String(),
			Metadata:  p.Metadata,
		}
	}

	return &forecastingv1.ComputeForwardCurveResponse{
		StrategyId:          strategyID.String(),
		StrategyVersion:     strategy.Version(),
		OutputDatasetCode:   strategy.OutputDatasetCode(),
		PointCount:          int32(len(points)),
		Horizon:             durationpb.New(horizon),
		Granularity:         durationpb.New(granularity),
		ExecutionTime:       timestamppb.New(now),
		ComputationDuration: durationpb.New(computeDuration),
		ForecastPoints:      protoPoints,
	}
}

// publishToMDS publishes forecast points to the Market Data Service as ESTIMATE quality observations.
// Points are batched to respect the MDS batch size limit of 1000.
func (s *Service) publishToMDS(
	ctx context.Context,
	strategy domain.ForecastingStrategy,
	points []starlark.ForecastPoint,
	executionTime time.Time,
) error {
	if s.mds == nil {
		s.logger.Warn("MDS publisher not configured, skipping forecast publish",
			"point_count", len(points))
		return nil
	}
	if len(points) == 0 {
		return nil
	}

	// Build all observation entries
	entries := make([]*marketinformationv1.BatchObservationEntry, len(points))
	for i, p := range points {
		entries[i] = &marketinformationv1.BatchObservationEntry{
			DatasetCode: strategy.OutputDatasetCode(),
			ObservedAt:  timestamppb.New(executionTime),
			ValidFrom:   timestamppb.New(p.Timestamp),
			Value:       p.Value.String(),
			Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
			SourceCode:  "FORECASTING",
			ClientReference: fmt.Sprintf("forecast:%s:v%d:%s",
				strategy.ID(), strategy.Version(), p.Timestamp.Format(time.RFC3339)),
		}
	}

	// Publish in batches
	for start := 0; start < len(entries); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(entries) {
			end = len(entries)
		}

		batch := entries[start:end]
		resp, err := s.mds.RecordObservationBatch(ctx, batch)
		if err != nil {
			return fmt.Errorf("batch publish (offset %d): %w", start, err)
		}

		if resp.GetFailureCount() > 0 {
			return fmt.Errorf("%w: offset %d, %d of %d observations failed",
				ErrBatchPublish, start, resp.GetFailureCount(), resp.GetTotalCount())
		}
	}

	return nil
}

// ScheduledExecutionResult holds the outcome of a scheduled forecast execution.
type ScheduledExecutionResult struct {
	PointCount      int32
	StrategyVersion int64
}

// ComputeForwardCurveInternal executes a forecasting strategy by ID for the
// cron scheduler. Unlike the gRPC method, it extracts the tenant from context
// (set by the scheduler) and returns a simpler result type.
func (s *Service) ComputeForwardCurveInternal(ctx context.Context, strategyID uuid.UUID) (*ScheduledExecutionResult, error) {
	strategy, err := s.repo.FindByID(ctx, strategyID)
	if err != nil {
		return nil, fmt.Errorf("find strategy %s: %w", strategyID, err)
	}

	if strategy.Status() != domain.StrategyStatusActive {
		return nil, fmt.Errorf("strategy %s is %s: %w", strategyID, strategy.Status(), ErrNotActive)
	}

	now := time.Now().UTC()

	s.logger.Info("executing scheduled forecast",
		"strategy_id", strategyID,
		"strategy_name", strategy.Name(),
		"strategy_version", strategy.Version())

	input := starlark.StrategyInput{
		Script:            strategy.StarlarkCode(),
		InputDatasetCodes: strategy.InputDatasetCodes(),
		OutputDatasetCode: strategy.OutputDatasetCode(),
		HorizonHours:      strategy.HorizonHours(),
		GranularityHours:  strategy.GranularityHours(),
		Now:               now,
	}
	if strategy.ReferenceDataResolutionKey() != "" {
		input.ResolutionKey = strategy.ReferenceDataResolutionKey()
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			return nil, fmt.Errorf("strategy %s requires reference data: %w", strategyID, ErrTenantIDRequired)
		}
		input.TenantID = string(tenantID)
	}

	points, err := s.runner.ExecuteStrategy(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("starlark execution: %w", err)
	}

	if err := s.publishToMDS(ctx, strategy, points, now); err != nil {
		return nil, fmt.Errorf("publish to MDS: %w", err)
	}

	return &ScheduledExecutionResult{
		PointCount:      int32(len(points)),
		StrategyVersion: strategy.Version(),
	}, nil
}

// mapDomainError converts domain errors to gRPC status errors.
func (s *Service) mapDomainError(err error, strategyID uuid.UUID) error {
	switch {
	case errors.Is(err, domain.ErrStrategyNotFound):
		return status.Errorf(codes.NotFound, "strategy %s not found", strategyID)
	case errors.Is(err, domain.ErrVersionMismatch):
		return status.Errorf(codes.Aborted, "strategy %s was modified concurrently", strategyID)
	default:
		s.logger.Error("internal error",
			"strategy_id", strategyID,
			"error", err,
		)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

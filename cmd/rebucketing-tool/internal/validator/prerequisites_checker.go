package validator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// DefaultPrerequisitesTimeout is the default timeout for prerequisites check operations.
	DefaultPrerequisitesTimeout = 30 * time.Second
)

// PrerequisitesCheckerConfig holds configuration for the PrerequisitesChecker.
type PrerequisitesCheckerConfig struct {
	// PositionKeepingTarget is the gRPC server address for Position Keeping (e.g., "localhost:50053").
	// If set, overrides Kubernetes DNS-based discovery.
	PositionKeepingTarget string

	// PositionKeepingServiceName is the Kubernetes service name for Position Keeping.
	PositionKeepingServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default").
	Namespace string

	// Timeout is the default timeout for RPC calls (defaults to 30s).
	Timeout time.Duration

	// Logger is used for structured logging.
	Logger *slog.Logger

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// applyDefaults sets default values for unspecified configuration fields.
func (cfg *PrerequisitesCheckerConfig) applyDefaults() {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultPrerequisitesTimeout
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
}

// PrerequisitesChecker validates that all prerequisites for rebucketing are met,
// including availability of raw measurements for recalculation.
type PrerequisitesChecker struct {
	positionKeepingClient positionkeepingv1.PositionKeepingServiceClient
	conn                  *grpc.ClientConn
	timeout               time.Duration
	logger                *slog.Logger
	retryConfig           clients.RetryConfig
}

// NewPrerequisitesChecker creates a new PrerequisitesChecker with gRPC clients.
func NewPrerequisitesChecker(ctx context.Context, cfg PrerequisitesCheckerConfig) (*PrerequisitesChecker, error) {
	cfg.applyDefaults()

	conn, err := createPrerequisitesConnection(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create position keeping connection: %w", err)
	}

	return &PrerequisitesChecker{
		positionKeepingClient: positionkeepingv1.NewPositionKeepingServiceClient(conn),
		conn:                  conn,
		timeout:               cfg.Timeout,
		logger:                cfg.Logger,
		retryConfig:           clients.DefaultRetryConfig(),
	}, nil
}

// createPrerequisitesConnection creates a gRPC connection based on configuration.
func createPrerequisitesConnection(ctx context.Context, cfg PrerequisitesCheckerConfig) (*grpc.ClientConn, error) {
	if cfg.PositionKeepingServiceName != "" {
		return platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
			ServiceName: cfg.PositionKeepingServiceName,
			Namespace:   cfg.Namespace,
			Port:        DefaultPositionKeepingPort,
			DialOptions: cfg.DialOptions,
		})
	}

	if cfg.PositionKeepingTarget != "" {
		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}
		return grpc.NewClient(cfg.PositionKeepingTarget, dialOpts...)
	}

	return nil, ErrTargetOrServiceRequired
}

// PrerequisitesCheckResult contains the result of prerequisites validation.
type PrerequisitesCheckResult struct {
	// MeasurementsAvailable indicates whether raw measurements are available.
	MeasurementsAvailable bool

	// MeasurementCount is the number of raw measurements found.
	MeasurementCount int

	// AffectedAccountIDs lists the account IDs that have positions with this instrument.
	AffectedAccountIDs []string

	// Warnings contains non-fatal issues discovered during validation.
	Warnings []string
}

// CheckPrerequisites validates all prerequisites for rebucketing an instrument version.
//
// This includes:
// 1. Verifying raw measurements exist in Position Keeping
// 2. Identifying all affected accounts
// 3. Checking for any conditions that might cause issues during rebucketing
//
// Returns nil error if all prerequisites are met.
func (c *PrerequisitesChecker) CheckPrerequisites(ctx context.Context, instrumentCode string, instrumentVersion int) (*PrerequisitesCheckResult, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, ErrMissingTenantContext
	}

	c.logger.Info("checking rebucketing prerequisites",
		"step", "prerequisites_check",
		"instrument_code", instrumentCode,
		"instrument_version", instrumentVersion,
		"tenant_id", tenantID.String(),
	)

	result := &PrerequisitesCheckResult{
		MeasurementsAvailable: false,
		MeasurementCount:      0,
		AffectedAccountIDs:    []string{},
		Warnings:              []string{},
	}

	// Step 1: Query Position Keeping for positions with this instrument
	positions, err := c.findPositionsWithInstrumentVersion(ctx, instrumentCode, instrumentVersion)
	if err != nil {
		return nil, &ValidationError{
			Operation: "prerequisites_check",
			Message:   "failed to query positions",
			Cause:     err,
		}
	}

	// Collect unique account IDs
	accountSet := make(map[string]struct{})
	for _, pos := range positions {
		if pos.AccountID != "" {
			accountSet[pos.AccountID] = struct{}{}
		}
	}
	for accountID := range accountSet {
		result.AffectedAccountIDs = append(result.AffectedAccountIDs, accountID)
	}

	// Step 2: Check if raw measurements exist for these positions
	measurementCount, err := c.countRawMeasurements(ctx, positions)
	if err != nil {
		c.logger.Warn("failed to count raw measurements",
			"step", "prerequisites_check",
			"instrument_code", instrumentCode,
			"instrument_version", instrumentVersion,
			"error", err,
		)
		result.Warnings = append(result.Warnings, fmt.Sprintf("measurement count check failed: %v", err))
	}

	result.MeasurementCount = measurementCount
	result.MeasurementsAvailable = measurementCount > 0

	// Step 3: Validate that measurements exist if positions exist
	if len(positions) > 0 && measurementCount == 0 {
		c.logger.Warn("prerequisites check failed - no raw measurements available",
			"step", "prerequisites_check",
			"instrument_code", instrumentCode,
			"instrument_version", instrumentVersion,
			"position_count", len(positions),
			"result", "failed",
		)

		return result, &RawMeasurementsUnavailableError{
			InstrumentCode:    instrumentCode,
			InstrumentVersion: instrumentVersion,
			Reason:            fmt.Sprintf("no raw measurements found for %d positions", len(positions)),
		}
	}

	c.logger.Info("prerequisites check passed",
		"step", "prerequisites_check",
		"instrument_code", instrumentCode,
		"instrument_version", instrumentVersion,
		"position_count", len(positions),
		"measurement_count", measurementCount,
		"affected_accounts", len(result.AffectedAccountIDs),
		"result", "passed",
	)

	return result, nil
}

// positionInfo holds basic information about a financial position.
type positionInfo struct {
	LogID     string
	AccountID string
}

// findPositionsWithInstrumentVersion queries Position Keeping for positions
// that use the specified instrument version.
func (c *PrerequisitesChecker) findPositionsWithInstrumentVersion(ctx context.Context, instrumentCode string, instrumentVersion int) ([]positionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	var positions []positionInfo
	var pageToken string

	for {
		pagePositions, nextToken, err := c.fetchPositionLogsPage(ctx, pageToken, instrumentCode, instrumentVersion)
		if err != nil {
			return nil, err
		}
		positions = append(positions, pagePositions...)

		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}

	return positions, nil
}

// fetchPositionLogsPage fetches a single page of position logs and filters by instrument.
func (c *PrerequisitesChecker) fetchPositionLogsPage(ctx context.Context, pageToken string, instrumentCode string, instrumentVersion int) ([]positionInfo, string, error) {
	var positions []positionInfo
	var nextPageToken string
	var lastErr error

	err := clients.Retry(ctx, c.retryConfig, func() error {
		resp, err := c.positionKeepingClient.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
			Pagination: &commonv1.Pagination{
				PageSize:  100,
				PageToken: pageToken,
			},
		})
		if err != nil {
			lastErr = err
			return err
		}

		positions = c.filterLogsWithInstrument(resp.Logs, instrumentCode, instrumentVersion)

		if resp.Pagination != nil {
			nextPageToken = resp.Pagination.NextPageToken
		}
		return nil
	})
	if err != nil {
		if lastErr != nil {
			return nil, "", lastErr
		}
		return nil, "", err
	}

	return positions, nextPageToken, nil
}

// filterLogsWithInstrument filters position logs that reference the specified instrument.
func (c *PrerequisitesChecker) filterLogsWithInstrument(logs []*positionkeepingv1.FinancialPositionLog, instrumentCode string, instrumentVersion int) []positionInfo {
	var positions []positionInfo
	for _, log := range logs {
		if c.logContainsInstrument(log, instrumentCode, instrumentVersion) {
			positions = append(positions, positionInfo{
				LogID:     log.LogId,
				AccountID: log.AccountId,
			})
		}
	}
	return positions
}

// logContainsInstrument checks if a log has any entry referencing the instrument.
func (c *PrerequisitesChecker) logContainsInstrument(log *positionkeepingv1.FinancialPositionLog, instrumentCode string, instrumentVersion int) bool {
	for _, entry := range log.TransactionLogEntries {
		if containsInstrumentReference(entry, instrumentCode, instrumentVersion) {
			return true
		}
	}
	return false
}

// countRawMeasurements counts the raw measurements associated with the given positions.
func (c *PrerequisitesChecker) countRawMeasurements(ctx context.Context, positions []positionInfo) (int, error) {
	if len(positions) == 0 {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// In a real implementation, we would query measurements by position log ID.
	// For now, we estimate based on transaction log entries in the positions.
	totalMeasurements := 0

	for _, pos := range positions {
		var lastErr error
		err := clients.Retry(ctx, c.retryConfig, func() error {
			resp, err := c.positionKeepingClient.RetrieveFinancialPositionLog(ctx, &positionkeepingv1.RetrieveFinancialPositionLogRequest{
				LogId: pos.LogID,
			})
			if err != nil {
				lastErr = err
				return err
			}

			// Each transaction log entry represents a measurement
			// In practice, there might be a separate measurement store query
			if resp.Log != nil {
				totalMeasurements += len(resp.Log.TransactionLogEntries)
			}

			return nil
		})
		if err != nil {
			// Log but continue - we want to count what we can
			c.logger.Warn("failed to retrieve position log for measurement count",
				"log_id", pos.LogID,
				"error", lastErr,
			)
		}
	}

	return totalMeasurements, nil
}

// ValidateInstrumentNotInUse checks if the instrument is currently being used
// for new transactions (recently created entries).
func (c *PrerequisitesChecker) ValidateInstrumentNotInUse(ctx context.Context, instrumentCode string, instrumentVersion int, lookbackDuration time.Duration) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return ErrMissingTenantContext
	}

	c.logger.Info("checking if instrument is in active use",
		"step", "active_use_check",
		"instrument_code", instrumentCode,
		"instrument_version", instrumentVersion,
		"lookback_duration", lookbackDuration.String(),
		"tenant_id", tenantID.String(),
	)

	activeCount, err := c.countActiveInstrumentUsage(ctx, instrumentCode, instrumentVersion, lookbackDuration)
	if err != nil {
		return err
	}

	if activeCount > 0 {
		c.logger.Warn("instrument is in active use",
			"step", "active_use_check",
			"instrument_code", instrumentCode,
			"instrument_version", instrumentVersion,
			"active_count", activeCount,
			"result", "failed",
		)

		return &InstrumentInUseError{
			InstrumentCode:    instrumentCode,
			InstrumentVersion: instrumentVersion,
			ActiveTradeCount:  activeCount,
		}
	}

	c.logger.Info("instrument is not in active use",
		"step", "active_use_check",
		"instrument_code", instrumentCode,
		"instrument_version", instrumentVersion,
		"result", "passed",
	)

	return nil
}

// countActiveInstrumentUsage counts recent transactions using the specified instrument.
func (c *PrerequisitesChecker) countActiveInstrumentUsage(ctx context.Context, instrumentCode string, instrumentVersion int, lookbackDuration time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	cutoffTime := time.Now().Add(-lookbackDuration)
	activeCount := 0
	var pageToken string

	for {
		var lastErr error
		var nextPageToken string
		var done bool

		err := clients.Retry(ctx, c.retryConfig, func() error {
			resp, err := c.positionKeepingClient.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
				Status: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
				Pagination: &commonv1.Pagination{
					PageSize:  100,
					PageToken: pageToken,
				},
			})
			if err != nil {
				lastErr = err
				return err
			}

			activeCount += c.countRecentInstrumentReferences(resp.Logs, cutoffTime, instrumentCode, instrumentVersion)

			// Check for more pages
			if resp.Pagination != nil && resp.Pagination.NextPageToken != "" {
				nextPageToken = resp.Pagination.NextPageToken
			} else {
				done = true
			}

			return nil
		})
		if err != nil {
			cause := lastErr
			if cause == nil {
				cause = err
			}
			return 0, &ValidationError{
				Operation: "active_use_check",
				Message:   "failed to query recent positions",
				Cause:     cause,
			}
		}

		if done {
			break
		}
		pageToken = nextPageToken
	}

	return activeCount, nil
}

// countRecentInstrumentReferences counts logs with recent instrument references.
func (c *PrerequisitesChecker) countRecentInstrumentReferences(logs []*positionkeepingv1.FinancialPositionLog, cutoffTime time.Time, instrumentCode string, instrumentVersion int) int {
	count := 0
	for _, log := range logs {
		if log.CreatedAt == nil || !log.CreatedAt.AsTime().After(cutoffTime) {
			continue
		}
		if c.logHasInstrumentReference(log, instrumentCode, instrumentVersion) {
			count++
		}
	}
	return count
}

// logHasInstrumentReference checks if a log has entries referencing the instrument.
func (c *PrerequisitesChecker) logHasInstrumentReference(log *positionkeepingv1.FinancialPositionLog, instrumentCode string, instrumentVersion int) bool {
	for _, entry := range log.TransactionLogEntries {
		if containsInstrumentReference(entry, instrumentCode, instrumentVersion) {
			return true
		}
	}
	return false
}

// Close releases the gRPC connection.
func (c *PrerequisitesChecker) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close position keeping connection: %w", err)
		}
	}
	return nil
}

// PrerequisitesCheckerInterface defines the interface for prerequisites checking.
// This allows for easy mocking in tests.
type PrerequisitesCheckerInterface interface {
	CheckPrerequisites(ctx context.Context, instrumentCode string, instrumentVersion int) (*PrerequisitesCheckResult, error)
	ValidateInstrumentNotInUse(ctx context.Context, instrumentCode string, instrumentVersion int, lookbackDuration time.Duration) error
	Close() error
}

// Ensure PrerequisitesChecker implements the interface.
var _ PrerequisitesCheckerInterface = (*PrerequisitesChecker)(nil)

// MockPrerequisitesChecker is a mock implementation for testing.
type MockPrerequisitesChecker struct {
	CheckFunc    func(ctx context.Context, instrumentCode string, instrumentVersion int) (*PrerequisitesCheckResult, error)
	NotInUseFunc func(ctx context.Context, instrumentCode string, instrumentVersion int, lookbackDuration time.Duration) error
	CloseFunc    func() error
}

// CheckPrerequisites implements PrerequisitesCheckerInterface.
func (m *MockPrerequisitesChecker) CheckPrerequisites(ctx context.Context, instrumentCode string, instrumentVersion int) (*PrerequisitesCheckResult, error) {
	if m.CheckFunc != nil {
		return m.CheckFunc(ctx, instrumentCode, instrumentVersion)
	}
	return &PrerequisitesCheckResult{
		MeasurementsAvailable: true,
		MeasurementCount:      1,
	}, nil
}

// ValidateInstrumentNotInUse implements PrerequisitesCheckerInterface.
func (m *MockPrerequisitesChecker) ValidateInstrumentNotInUse(ctx context.Context, instrumentCode string, instrumentVersion int, lookbackDuration time.Duration) error {
	if m.NotInUseFunc != nil {
		return m.NotInUseFunc(ctx, instrumentCode, instrumentVersion, lookbackDuration)
	}
	return nil
}

// Close implements PrerequisitesCheckerInterface.
func (m *MockPrerequisitesChecker) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

// NewMockPrerequisitesChecker creates a mock checker with default passing behavior.
func NewMockPrerequisitesChecker() *MockPrerequisitesChecker {
	return &MockPrerequisitesChecker{}
}

// NewMockPrerequisitesCheckerWithMeasurements creates a mock with specific measurement count.
func NewMockPrerequisitesCheckerWithMeasurements(measurementCount int, accountIDs []string) *MockPrerequisitesChecker {
	return &MockPrerequisitesChecker{
		CheckFunc: func(ctx context.Context, _ string, _ int) (*PrerequisitesCheckResult, error) {
			if _, ok := tenant.FromContext(ctx); !ok {
				return nil, ErrMissingTenantContext
			}
			return &PrerequisitesCheckResult{
				MeasurementsAvailable: measurementCount > 0,
				MeasurementCount:      measurementCount,
				AffectedAccountIDs:    accountIDs,
			}, nil
		},
	}
}

// NewMockPrerequisitesCheckerWithError creates a mock that returns the specified error.
func NewMockPrerequisitesCheckerWithError(err error) *MockPrerequisitesChecker {
	return &MockPrerequisitesChecker{
		CheckFunc: func(ctx context.Context, _ string, _ int) (*PrerequisitesCheckResult, error) {
			if _, ok := tenant.FromContext(ctx); !ok {
				return nil, ErrMissingTenantContext
			}
			return nil, err
		},
	}
}

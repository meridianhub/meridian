package validator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	// DefaultSettlementCheckerTimeout is the default timeout for settlement check operations.
	DefaultSettlementCheckerTimeout = 30 * time.Second

	// DefaultPositionKeepingPort is the default gRPC port for the Position Keeping service.
	DefaultPositionKeepingPort = 50053

	// DefaultFinancialAccountingPort is the default gRPC port for the Financial Accounting service.
	DefaultFinancialAccountingPort = 50052

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"
)

// SettlementCheckerConfig holds configuration for the SettlementLockChecker.
type SettlementCheckerConfig struct {
	// PositionKeepingTarget is the gRPC server address for Position Keeping (e.g., "localhost:50053").
	// If set, overrides Kubernetes DNS-based discovery.
	PositionKeepingTarget string

	// FinancialAccountingTarget is the gRPC server address for Financial Accounting (e.g., "localhost:50052").
	// If set, overrides Kubernetes DNS-based discovery.
	FinancialAccountingTarget string

	// PositionKeepingServiceName is the Kubernetes service name for Position Keeping.
	PositionKeepingServiceName string

	// FinancialAccountingServiceName is the Kubernetes service name for Financial Accounting.
	FinancialAccountingServiceName string

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
func (cfg *SettlementCheckerConfig) applyDefaults() {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultSettlementCheckerTimeout
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
}

// SettlementLockChecker validates that no positions exist in finalized settlements
// before allowing rebucketing operations.
type SettlementLockChecker struct {
	positionKeepingClient     positionkeepingv1.PositionKeepingServiceClient
	financialAccountingClient financialaccountingv1.FinancialAccountingServiceClient
	positionKeepingConn       *grpc.ClientConn
	financialAccountingConn   *grpc.ClientConn
	timeout                   time.Duration
	logger                    *slog.Logger
	retryConfig               clients.RetryConfig
}

// NewSettlementLockChecker creates a new SettlementLockChecker with gRPC clients.
func NewSettlementLockChecker(ctx context.Context, cfg SettlementCheckerConfig) (*SettlementLockChecker, error) {
	cfg.applyDefaults()

	// Create Position Keeping connection
	pkConn, err := createGRPCConnection(ctx, grpcConnectionConfig{
		target:      cfg.PositionKeepingTarget,
		serviceName: cfg.PositionKeepingServiceName,
		namespace:   cfg.Namespace,
		port:        DefaultPositionKeepingPort,
		dialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create position keeping connection: %w", err)
	}

	// Create Financial Accounting connection
	faConn, err := createGRPCConnection(ctx, grpcConnectionConfig{
		target:      cfg.FinancialAccountingTarget,
		serviceName: cfg.FinancialAccountingServiceName,
		namespace:   cfg.Namespace,
		port:        DefaultFinancialAccountingPort,
		dialOptions: cfg.DialOptions,
	})
	if err != nil {
		_ = pkConn.Close()
		return nil, fmt.Errorf("failed to create financial accounting connection: %w", err)
	}

	return &SettlementLockChecker{
		positionKeepingClient:     positionkeepingv1.NewPositionKeepingServiceClient(pkConn),
		financialAccountingClient: financialaccountingv1.NewFinancialAccountingServiceClient(faConn),
		positionKeepingConn:       pkConn,
		financialAccountingConn:   faConn,
		timeout:                   cfg.Timeout,
		logger:                    cfg.Logger,
		retryConfig:               clients.DefaultRetryConfig(),
	}, nil
}

// grpcConnectionConfig holds parameters for creating a gRPC connection.
type grpcConnectionConfig struct {
	target      string
	serviceName string
	namespace   string
	port        int
	dialOptions []grpc.DialOption
}

// createGRPCConnection creates a gRPC connection based on configuration.
func createGRPCConnection(ctx context.Context, cfg grpcConnectionConfig) (*grpc.ClientConn, error) {
	if cfg.serviceName != "" {
		// Use platform gRPC factory for DNS-based load balancing
		return platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
			ServiceName: cfg.serviceName,
			Namespace:   cfg.namespace,
			Port:        cfg.port,
			DialOptions: cfg.dialOptions,
		})
	}

	if cfg.target != "" {
		// Direct connection for local development
		dialOpts := cfg.dialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}
		return grpc.NewClient(cfg.target, dialOpts...)
	}

	return nil, ErrTargetOrServiceRequired
}

// CheckSettlementLock verifies that no positions with the specified instrument version
// exist in finalized settlements.
//
// Returns nil if no settlement lock exists (safe to proceed with rebucketing).
// Returns *SettlementLockError if positions exist in finalized settlements.
// Returns other errors for service communication failures.
func (c *SettlementLockChecker) CheckSettlementLock(ctx context.Context, instrumentCode string, instrumentVersion int) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return ErrMissingTenantContext
	}

	c.logger.Info("checking settlement lock",
		"step", "settlement_lock_check",
		"instrument_code", instrumentCode,
		"instrument_version", instrumentVersion,
		"tenant_id", tenantID.String(),
	)

	// Step 1: Query Position Keeping for positions with this instrument version
	// that have status POSTED (finalized)
	positionsWithInstrument, err := c.findPositionsWithInstrument(ctx, instrumentCode, instrumentVersion)
	if err != nil {
		return &ValidationError{
			Operation: "settlement_lock_check",
			Message:   "failed to query positions",
			Cause:     err,
		}
	}

	if len(positionsWithInstrument) == 0 {
		c.logger.Info("settlement lock check passed - no positions found",
			"step", "settlement_lock_check",
			"instrument_code", instrumentCode,
			"instrument_version", instrumentVersion,
			"result", "passed",
		)
		return nil
	}

	// Step 2: Check if any of these positions are in finalized settlements
	finalizedSettlements, err := c.findFinalizedSettlements(ctx, positionsWithInstrument)
	if err != nil {
		return &ValidationError{
			Operation: "settlement_lock_check",
			Message:   "failed to query finalized settlements",
			Cause:     err,
		}
	}

	if len(finalizedSettlements) > 0 {
		c.logger.Warn("settlement lock check failed - positions exist in finalized settlements",
			"step", "settlement_lock_check",
			"instrument_code", instrumentCode,
			"instrument_version", instrumentVersion,
			"position_count", len(positionsWithInstrument),
			"settlement_count", len(finalizedSettlements),
			"result", "failed",
		)

		return &SettlementLockError{
			InstrumentCode:    instrumentCode,
			InstrumentVersion: instrumentVersion,
			PositionCount:     len(positionsWithInstrument),
			SettlementIDs:     finalizedSettlements,
		}
	}

	c.logger.Info("settlement lock check passed - no finalized settlements",
		"step", "settlement_lock_check",
		"instrument_code", instrumentCode,
		"instrument_version", instrumentVersion,
		"position_count", len(positionsWithInstrument),
		"result", "passed",
	)

	return nil
}

// findPositionsWithInstrument queries Position Keeping for financial position logs
// that reference the specified instrument version.
func (c *SettlementLockChecker) findPositionsWithInstrument(ctx context.Context, instrumentCode string, instrumentVersion int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	var positionLogIDs []string
	var pageToken string

	for {
		pageIDs, nextToken, err := c.fetchPostedPositionLogsPage(ctx, pageToken, instrumentCode, instrumentVersion)
		if err != nil {
			return nil, err
		}
		positionLogIDs = append(positionLogIDs, pageIDs...)

		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}

	return positionLogIDs, nil
}

// fetchPostedPositionLogsPage fetches a single page of posted position logs and filters by instrument.
func (c *SettlementLockChecker) fetchPostedPositionLogsPage(ctx context.Context, pageToken string, instrumentCode string, instrumentVersion int) ([]string, string, error) {
	var logIDs []string
	var nextPageToken string
	var lastErr error

	err := clients.Retry(ctx, c.retryConfig, func() error {
		resp, err := c.positionKeepingClient.ListFinancialPositionLogs(ctx, &positionkeepingv1.ListFinancialPositionLogsRequest{
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			Pagination: &commonv1.Pagination{
				PageSize:  100,
				PageToken: pageToken,
			},
		})
		if err != nil {
			lastErr = err
			return err
		}

		logIDs = c.filterLogIDsWithInstrument(resp.Logs, instrumentCode, instrumentVersion)

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

	return logIDs, nextPageToken, nil
}

// filterLogIDsWithInstrument filters position log IDs that reference the specified instrument.
func (c *SettlementLockChecker) filterLogIDsWithInstrument(logs []*positionkeepingv1.FinancialPositionLog, instrumentCode string, instrumentVersion int) []string {
	var logIDs []string
	for _, log := range logs {
		if logHasInstrumentRef(log, instrumentCode, instrumentVersion) {
			logIDs = append(logIDs, log.LogId)
		}
	}
	return logIDs
}

// logHasInstrumentRef checks if a log has any entry referencing the instrument.
func logHasInstrumentRef(log *positionkeepingv1.FinancialPositionLog, instrumentCode string, instrumentVersion int) bool {
	for _, entry := range log.TransactionLogEntries {
		if containsInstrumentReference(entry, instrumentCode, instrumentVersion) {
			return true
		}
	}
	return false
}

// containsInstrumentReference checks if a transaction log entry references the specified instrument.
func containsInstrumentReference(entry *positionkeepingv1.TransactionLogEntry, instrumentCode string, instrumentVersion int) bool {
	if entry == nil {
		return false
	}

	// Check the reference field which may contain instrument information
	// Format: "instrument:<code>:v<version>" or similar
	expectedRef := fmt.Sprintf("instrument:%s:v%d", instrumentCode, instrumentVersion)
	if entry.Reference == expectedRef {
		return true
	}

	// Also check the description for instrument references
	expectedDesc := fmt.Sprintf("%s v%d", instrumentCode, instrumentVersion)
	if entry.Description != "" && containsSubstring(entry.Description, expectedDesc) {
		return true
	}

	return false
}

// containsSubstring checks if s contains substr (simple substring match).
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findSubstring(s, substr))
}

// findSubstring performs a simple substring search.
func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// findFinalizedSettlements checks which position logs are part of finalized settlements
// by querying the Financial Accounting service.
func (c *SettlementLockChecker) findFinalizedSettlements(ctx context.Context, positionLogIDs []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	var finalizedSettlements []string

	for _, logID := range positionLogIDs {
		settlements, err := c.findSettlementsForPosition(ctx, logID)
		if err != nil {
			// Log but continue - we want to find all possible locks
			c.logger.Warn("failed to query booking logs for position",
				"position_log_id", logID,
				"error", err,
			)
			continue
		}
		finalizedSettlements = append(finalizedSettlements, settlements...)
	}

	return finalizedSettlements, nil
}

// findSettlementsForPosition queries all pages of booking logs for a single position.
func (c *SettlementLockChecker) findSettlementsForPosition(ctx context.Context, logID string) ([]string, error) {
	var settlements []string
	var pageToken string

	for {
		pageSettlements, nextToken, err := c.fetchBookingLogsPage(ctx, pageToken, logID)
		if err != nil {
			return nil, err
		}
		settlements = append(settlements, pageSettlements...)

		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}

	return settlements, nil
}

// fetchBookingLogsPage fetches a single page of booking logs and filters by position reference.
func (c *SettlementLockChecker) fetchBookingLogsPage(ctx context.Context, pageToken string, logID string) ([]string, string, error) {
	var settlements []string
	var nextPageToken string
	var lastErr error

	err := clients.Retry(ctx, c.retryConfig, func() error {
		resp, err := c.financialAccountingClient.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			Pagination: &commonv1.Pagination{
				PageSize:  100,
				PageToken: pageToken,
			},
		})
		if err != nil {
			lastErr = err
			return err
		}

		settlements = filterBookingLogsByPosition(resp.FinancialBookingLogs, logID)

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

	return settlements, nextPageToken, nil
}

// filterBookingLogsByPosition filters booking log IDs that reference the specified position.
func filterBookingLogsByPosition(logs []*financialaccountingv1.FinancialBookingLog, logID string) []string {
	var settlements []string
	for _, bookingLog := range logs {
		if bookingLogReferencesPosition(bookingLog, logID) {
			settlements = append(settlements, bookingLog.Id)
		}
	}
	return settlements
}

// bookingLogReferencesPosition checks if a booking log references a position log.
func bookingLogReferencesPosition(bookingLog *financialaccountingv1.FinancialBookingLog, positionLogID string) bool {
	if bookingLog == nil {
		return false
	}

	// Check if the product_service_reference contains the position log ID
	if bookingLog.ProductServiceReference == positionLogID {
		return true
	}

	// Check postings for position references
	for _, posting := range bookingLog.Postings {
		if posting.PostingResult != "" && containsSubstring(posting.PostingResult, positionLogID) {
			return true
		}
	}

	return false
}

// Close releases all gRPC connections.
func (c *SettlementLockChecker) Close() error {
	var errs []error

	if c.positionKeepingConn != nil {
		if err := c.positionKeepingConn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close position keeping connection: %w", err))
		}
	}

	if c.financialAccountingConn != nil {
		if err := c.financialAccountingConn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close financial accounting connection: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %v", ErrClosingConnections, errs)
	}

	return nil
}

// SettlementLockCheckerInterface defines the interface for settlement lock checking.
// This allows for easy mocking in tests.
type SettlementLockCheckerInterface interface {
	CheckSettlementLock(ctx context.Context, instrumentCode string, instrumentVersion int) error
	Close() error
}

// Ensure SettlementLockChecker implements the interface.
var _ SettlementLockCheckerInterface = (*SettlementLockChecker)(nil)

// MockSettlementLockChecker is a mock implementation for testing.
type MockSettlementLockChecker struct {
	CheckFunc func(ctx context.Context, instrumentCode string, instrumentVersion int) error
	CloseFunc func() error
}

// CheckSettlementLock implements SettlementLockCheckerInterface.
func (m *MockSettlementLockChecker) CheckSettlementLock(ctx context.Context, instrumentCode string, instrumentVersion int) error {
	if m.CheckFunc != nil {
		return m.CheckFunc(ctx, instrumentCode, instrumentVersion)
	}
	return nil
}

// Close implements SettlementLockCheckerInterface.
func (m *MockSettlementLockChecker) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

// NewMockSettlementLockChecker creates a mock checker that returns the specified error.
func NewMockSettlementLockChecker(err error) *MockSettlementLockChecker {
	return &MockSettlementLockChecker{
		CheckFunc: func(ctx context.Context, _ string, _ int) error {
			// Validate tenant context even in mock
			if _, ok := tenant.FromContext(ctx); !ok {
				return ErrMissingTenantContext
			}
			return err
		},
	}
}

// NewMockSettlementLockCheckerWithLock creates a mock that returns a settlement lock error.
func NewMockSettlementLockCheckerWithLock(instrumentCode string, instrumentVersion, positionCount int, settlementIDs []string) *MockSettlementLockChecker {
	return &MockSettlementLockChecker{
		CheckFunc: func(ctx context.Context, code string, version int) error {
			if _, ok := tenant.FromContext(ctx); !ok {
				return ErrMissingTenantContext
			}
			if code == instrumentCode && version == instrumentVersion {
				return &SettlementLockError{
					InstrumentCode:    instrumentCode,
					InstrumentVersion: instrumentVersion,
					PositionCount:     positionCount,
					SettlementIDs:     settlementIDs,
				}
			}
			return nil
		},
	}
}

// IsSettlementLocked is a helper that checks if an error indicates a settlement lock.
func IsSettlementLocked(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if ok && st.Code() == codes.FailedPrecondition {
		return true
	}
	var lockErr *SettlementLockError
	return errors.As(err, &lockErr)
}

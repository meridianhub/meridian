// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/protobuf/proto"
)

// Service errors
var (
	ErrRepositoryNil                = errors.New("repository cannot be nil")
	ErrCurrentAccountClientNil      = errors.New("current account client cannot be nil")
	ErrFinancialAccountingClientNil = errors.New("financial accounting client cannot be nil")
	ErrInternalAccountClientNil     = errors.New("internal account client cannot be nil when internal clearing is enabled")
	ErrPaymentGatewayNil            = errors.New("payment gateway cannot be nil")
	ErrGatewayAccountConfigNil      = errors.New("gateway account config cannot be nil")
	ErrIdempotencyServiceNil        = errors.New("idempotency service cannot be nil")
	ErrAmountRequired               = errors.New("amount is required")
	ErrInvalidNanos                 = errors.New("nanos must be in range [-999999999, 999999999]")
	ErrPaymentRejected              = errors.New("payment rejected by gateway")
	ErrUnexpectedGatewayStatus      = errors.New("unexpected gateway status")
	ErrIdempotencyKeyTooLong        = errors.New("idempotency key exceeds maximum length")
	ErrMalformedLienResponse        = errors.New("current account service returned empty or malformed lien response")
	ErrLedgerPostingFailed          = errors.New("failed to post ledger entries")
	ErrUnsupportedCurrency          = errors.New("unsupported currency for ledger posting")
)

// Kafka topic constants
const (
	TopicPaymentOrderInitiated = "payment-order.initiated.v1"
	TopicPaymentOrderReserved  = "payment-order.reserved.v1"
	TopicPaymentOrderExecuting = "payment-order.executing.v1"
	TopicPaymentOrderCompleted = "payment-order.completed.v1"
	TopicPaymentOrderFailed    = "payment-order.failed.v1"
	TopicPaymentOrderCancelled = "payment-order.cancelled.v1"
	TopicPaymentOrderReversed  = "payment-order.reversed.v1"
)

// Operation result status constants for observability
const (
	opStatusSuccess    = "success"
	opStatusError      = "error"
	opStatusIdempotent = "idempotent"
)

// CurrentAccountClient defines the interface for communicating with the CurrentAccount service
// for lien operations (fund reservation).
type CurrentAccountClient interface {
	// InitiateLien creates a fund reservation on an account
	InitiateLien(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error)
	// TerminateLien releases a reservation without executing
	TerminateLien(ctx context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error)
	// ExecuteLien converts a reservation to an actual debit
	ExecuteLien(ctx context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error)
	// Close terminates the client connection
	Close() error
}

// FinancialAccountingClient defines the interface for communicating with the FinancialAccounting service
// for ledger posting operations on payment completion.
type FinancialAccountingClient interface {
	// InitiateFinancialBookingLog creates a new financial booking log
	InitiateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error)
	// CaptureLedgerPosting creates a new ledger posting entry
	CaptureLedgerPosting(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error)
	// UpdateFinancialBookingLog updates an existing booking log (e.g., to transition status to POSTED)
	UpdateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error)
	// Close terminates the client connection
	Close() error
}

// InternalAccountClient defines the interface for communicating with the Internal Account service
// for clearing account resolution. This is an optional dependency for internal clearing operations.
type InternalAccountClient interface {
	// GetBalance retrieves the current balance for an internal account.
	// Used to query clearing account balances for reconciliation.
	GetBalance(ctx context.Context, req *internalaccountv1.GetBalanceRequest) (*internalaccountv1.GetBalanceResponse, error)
	// RetrieveInternalAccount fetches account details by ID.
	// Used to verify clearing account configuration.
	RetrieveInternalAccount(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error)
	// ListInternalAccounts queries accounts with filtering.
	// Used to discover clearing accounts by type and instrument.
	ListInternalAccounts(ctx context.Context, req *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error)
	// Close terminates the client connection
	Close() error
}

// ReferenceDataClient defines the interface for communicating with the Reference Data service
// to fetch instrument definitions and CEL expressions for bucket-aware solvency validation.
type ReferenceDataClient interface {
	// RetrieveInstrument fetches an instrument definition by code.
	// Returns the fungibility_key_expression needed for bucket evaluation.
	RetrieveInstrument(ctx context.Context, code string) (*InstrumentInfo, error)

	// GetSaga fetches a saga definition by name and version.
	// If version is 0, returns the ACTIVE version.
	GetSaga(ctx context.Context, name string, version int) (*SagaDefinition, error)

	// Close terminates the client connection
	Close() error
}

// InstrumentInfo contains the subset of instrument definition needed for payment processing.
// This is a simplified view of the full InstrumentDefinition from reference-data service.
type InstrumentInfo struct {
	// Code is the unique instrument code (e.g., "USD", "RICE_V1").
	Code string
	// Version is the schema version of the instrument definition.
	Version int32
	// FungibilityKeyExpression is the CEL expression for generating bucket keys.
	// Empty string means fully fungible (no bucketing).
	FungibilityKeyExpression string
}

// SagaDefinition represents a saga script definition from reference-data.
type SagaDefinition struct {
	// ID is the unique identifier for this saga definition (UUID).
	ID string
	// Name is the saga identifier (e.g., "payment_execution").
	Name string
	// Version is the saga version number.
	Version int
	// Script is the Starlark source code.
	Script string
	// Status is the lifecycle status (ACTIVE, DEPRECATED, etc.).
	Status string
}

// KafkaPublisher defines the interface for publishing protobuf messages to Kafka.
// This abstraction allows for mocking in tests and alternative implementations.
type KafkaPublisher interface {
	// Publish sends a protobuf message to the specified Kafka topic.
	Publish(ctx context.Context, topic string, key string, msg proto.Message) error
}

// Configuration defaults
const (
	// DefaultSagaTimeout is the default timeout for payment saga orchestration.
	// This allows for typical payment gateway latency plus retries.
	DefaultSagaTimeout = 5 * time.Minute

	// DefaultPageSize is the default number of items per page for list operations.
	DefaultPageSize = 50

	// DefaultMaxPageSize is the maximum allowed page size for list operations.
	DefaultMaxPageSize = 1000

	// DefaultMaxIdempotencyKeyLength is the maximum allowed length for idempotency keys.
	DefaultMaxIdempotencyKeyLength = 256

	// DefaultLienExecutionMaxRetries is the maximum number of retry attempts for ExecuteLien.
	DefaultLienExecutionMaxRetries = 5

	// DefaultLienExecutionRetryTimeout is the timeout for the entire retry sequence.
	DefaultLienExecutionRetryTimeout = 2 * time.Minute

	// lienStatusUpdateMaxRetries is the number of times to retry status updates on version conflict.
	lienStatusUpdateMaxRetries = 5
	// lienStatusUpdateBackoffBase is the base duration for exponential backoff between retries.
	lienStatusUpdateBackoffBase = 100 * time.Millisecond
	// lienStatusUpdateTimeout is the timeout for the entire status update operation.
	lienStatusUpdateTimeout = defaults.DefaultRPCTimeout
)

// Money conversion constants for Google Money proto (nanos have 9 decimal places)
const (
	// nanosPerCent is the number of nanos in one cent (1 cent = 0.01 = 10^7 nanos)
	nanosPerCent = 10000000
	// nanosRoundingOffset is half a cent in nanos, used for rounding
	nanosRoundingOffset = 5000000
)

// Redis idempotency constants
const (
	// idempotencyNamespace is the Redis key namespace for payment-order idempotency
	idempotencyNamespace = "payment-order"

	// idempotencyPendingTTL is how long a pending idempotency record remains valid
	idempotencyPendingTTL = 5 * time.Minute

	// idempotencyResultTTL is how long completed results are cached
	idempotencyResultTTL = 24 * time.Hour
)

// Service implements the PaymentOrderService gRPC service
type Service struct {
	pb.UnimplementedPaymentOrderServiceServer
	repo                      persistence.Repository
	currentAccountClient      CurrentAccountClient
	financialAccountingClient FinancialAccountingClient
	internalAccountClient     InternalAccountClient // Optional - for internal clearing operations
	referenceDataClient       ReferenceDataClient   // Optional - for bucket-aware solvency validation
	paymentGateway            gateway.PaymentGateway
	gatewayAccountConfig      *config.GatewayAccountConfig
	kafkaPublisher            KafkaPublisher
	idempotencyService        idempotency.Service
	logger                    *slog.Logger
	tracer                    *observability.Tracer
	sagaTimeout               time.Duration
	defaultPageSize           int
	maxPageSize               int
	maxIdempotencyKeyLength   int
	lienExecutionRetryConfig  *sharedclients.RetryConfig // nil means use default
	orchestrator              *PaymentOrchestrator       // Handles payment saga orchestration
	internalClearingEnabled   bool                       // Feature flag for internal clearing operations
}

// Config contains configuration for creating a new Service
type Config struct {
	Repository                persistence.Repository
	CurrentAccountClient      CurrentAccountClient
	FinancialAccountingClient FinancialAccountingClient
	InternalAccountClient     InternalAccountClient // Optional - for internal clearing operations
	ReferenceDataClient       ReferenceDataClient   // Optional - for bucket-aware solvency validation
	PaymentGateway            gateway.PaymentGateway
	GatewayAccountConfig      *config.GatewayAccountConfig
	KafkaPublisher            KafkaPublisher
	IdempotencyService        idempotency.Service
	Logger                    *slog.Logger
	Tracer                    *observability.Tracer
	// SagaTimeout is the maximum duration for saga orchestration.
	// If zero, DefaultSagaTimeout is used.
	SagaTimeout time.Duration
	// DefaultPageSize is the default number of items per page. If zero, DefaultPageSize is used.
	DefaultPageSize int
	// MaxPageSize is the maximum allowed page size. If zero, DefaultMaxPageSize is used.
	MaxPageSize int
	// MaxIdempotencyKeyLength is the maximum allowed idempotency key length.
	// If zero, DefaultMaxIdempotencyKeyLength is used.
	MaxIdempotencyKeyLength int
	// LienExecutionRetryConfig configures retry behavior for async lien execution.
	// If nil, default retry config is used. Primarily useful for testing.
	LienExecutionRetryConfig *sharedclients.RetryConfig
	// InternalClearingEnabled enables internal clearing operations (default: false).
	// When enabled, the service uses InternalAccountClient for clearing account resolution.
	InternalClearingEnabled bool

	// HandlerRegistry is an optional external handler registry with cross-service Starlark
	// handlers (e.g., party.get_default_payment_method). When provided, the orchestrator
	// registers its internal payment_order.* handlers on this registry so saga scripts
	// have access to all registered handlers. When nil, a new empty registry is created.
	HandlerRegistry *saga.HandlerRegistry

	// SagaExecutionLogger persists saga execution records for audit. Optional.
	SagaExecutionLogger domain.SagaExecutionLogger

	// SagaOrchestrationEnabled controls whether the Starlark saga orchestration
	// path is used. When false (default), the orchestrator marks payment orders
	// as failed since Go-based orchestration was removed. Set
	// USE_SAGA_ORCHESTRATION=true to enable.
	SagaOrchestrationEnabled bool
}

// NewService creates a new payment order service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithConfig.
// Returns ErrRepositoryNil if the repository is nil.
// Returns ErrIdempotencyServiceNil if idempotencyService is nil.
func NewService(repo persistence.Repository, idempotencyService idempotency.Service) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	if idempotencyService == nil {
		return nil, ErrIdempotencyServiceNil
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Create minimal orchestrator for testing - external clients may be nil
	// but orchestrator methods check for nil before use
	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger: logger,
		Repo:   repo,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create payment orchestrator: %w", err)
	}

	return &Service{
		repo:                    repo,
		idempotencyService:      idempotencyService,
		logger:                  logger,
		sagaTimeout:             DefaultSagaTimeout,
		defaultPageSize:         DefaultPageSize,
		maxPageSize:             DefaultMaxPageSize,
		maxIdempotencyKeyLength: DefaultMaxIdempotencyKeyLength,
		orchestrator:            orchestrator,
	}, nil
}

// NewServiceWithConfig creates a new payment order service with full configuration.
// Validates all required dependencies and applies defaults where appropriate.
func NewServiceWithConfig(cfg Config) (*Service, error) {
	// Validate required dependencies
	if cfg.Repository == nil {
		return nil, ErrRepositoryNil
	}
	if cfg.CurrentAccountClient == nil {
		return nil, ErrCurrentAccountClientNil
	}
	if cfg.FinancialAccountingClient == nil {
		return nil, ErrFinancialAccountingClientNil
	}
	if cfg.PaymentGateway == nil {
		return nil, ErrPaymentGatewayNil
	}
	if cfg.GatewayAccountConfig == nil {
		return nil, ErrGatewayAccountConfigNil
	}
	if cfg.IdempotencyService == nil {
		return nil, ErrIdempotencyServiceNil
	}
	// KafkaPublisher is optional - nil is handled gracefully by publishEvent
	// InternalAccountClient is optional but required if internal clearing is enabled
	if cfg.InternalClearingEnabled && cfg.InternalAccountClient == nil {
		return nil, ErrInternalAccountClientNil
	}

	// Apply default logger if not provided
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Apply defaults for optional config values
	sagaTimeout := cfg.SagaTimeout
	if sagaTimeout == 0 {
		sagaTimeout = DefaultSagaTimeout
	}

	defaultPageSize := cfg.DefaultPageSize
	if defaultPageSize == 0 {
		defaultPageSize = DefaultPageSize
	}

	maxPageSize := cfg.MaxPageSize
	if maxPageSize == 0 {
		maxPageSize = DefaultMaxPageSize
	}

	maxIdempotencyKeyLength := cfg.MaxIdempotencyKeyLength
	if maxIdempotencyKeyLength == 0 {
		maxIdempotencyKeyLength = DefaultMaxIdempotencyKeyLength
	}

	// Create the payment orchestrator with all dependencies
	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:                    logger,
		Repo:                      cfg.Repository,
		CurrentAccountClient:      cfg.CurrentAccountClient,
		PaymentGateway:            cfg.PaymentGateway,
		FinancialAccountingClient: cfg.FinancialAccountingClient,
		InternalAccountClient:     cfg.InternalAccountClient,
		ReferenceDataClient:       cfg.ReferenceDataClient,
		GatewayAccountConfig:      cfg.GatewayAccountConfig,
		KafkaPublisher:            cfg.KafkaPublisher,
		LienExecutionRetryConfig:  cfg.LienExecutionRetryConfig,
		InternalClearingEnabled:   cfg.InternalClearingEnabled,
		HandlerRegistry:           cfg.HandlerRegistry,
		SagaExecutionLogger:       cfg.SagaExecutionLogger,
		SagaOrchestrationEnabled:  cfg.SagaOrchestrationEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create payment orchestrator: %w", err)
	}

	return &Service{
		repo:                      cfg.Repository,
		currentAccountClient:      cfg.CurrentAccountClient,
		financialAccountingClient: cfg.FinancialAccountingClient,
		internalAccountClient:     cfg.InternalAccountClient, // Optional - may be nil
		referenceDataClient:       cfg.ReferenceDataClient,   // Optional - may be nil
		paymentGateway:            cfg.PaymentGateway,
		gatewayAccountConfig:      cfg.GatewayAccountConfig,
		kafkaPublisher:            cfg.KafkaPublisher,
		idempotencyService:        cfg.IdempotencyService,
		logger:                    logger,
		tracer:                    cfg.Tracer,
		sagaTimeout:               sagaTimeout,
		defaultPageSize:           defaultPageSize,
		maxPageSize:               maxPageSize,
		maxIdempotencyKeyLength:   maxIdempotencyKeyLength,
		lienExecutionRetryConfig:  cfg.LienExecutionRetryConfig, // nil means use default
		orchestrator:              orchestrator,
		internalClearingEnabled:   cfg.InternalClearingEnabled,
	}, nil
}

// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	sagaschema "github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"google.golang.org/protobuf/proto"
)

// Orchestrator configuration errors.
// Core errors are re-exported from shared/pkg/clients for consistency across services.
// When service startup fails due to these errors, the application will:
// 1. Exit with a non-zero status code
// 2. Log the specific error with context about which dependency is missing
// 3. Enter crash loop backoff in Kubernetes until the configuration is fixed
var (
	ErrOrchestratorLoggerNil = sharedclients.ErrConfigLoggerNil
	ErrOrchestratorRepoNil   = sharedclients.ErrConfigRepositoryNil
)

// Runtime configuration errors for optional dependencies.
// These are checked at runtime during Orchestrate() with graceful error handling.
var (
	ErrGatewayAccountConfigNotSet      = errors.New("gateway account config not configured")
	ErrFinancialAccountingClientNotSet = errors.New("financial accounting client not configured")
	ErrNilBookingLogResponse           = errors.New("financial accounting returned nil booking log")
)

// Parameter validation errors for PostLedgerEntriesFromParams.
var (
	ErrMissingPaymentOrderID     = errors.New("missing or invalid payment_order_id")
	ErrMissingDebtorAccountID    = errors.New("missing or invalid debtor_account_id")
	ErrMissingGatewayReferenceID = errors.New("missing or invalid gateway_reference_id")
	ErrMissingAmountCents        = errors.New("missing or invalid amount_cents")
	ErrMissingCurrency           = errors.New("missing or invalid currency")
	ErrMissingIdempotencyKey     = errors.New("missing or invalid idempotency_key")
	ErrParamKeyNotFound          = errors.New("param key not found")
	ErrParamInvalidType          = errors.New("param has invalid type")
)

// PaymentOrchestrator encapsulates payment saga orchestration logic.
// It handles the multi-step payment workflow including fund reservation,
// gateway communication, ledger posting, and lien execution.
//
// Methods are organized across files by responsibility:
//   - payment_orchestrator.go: struct, config, constructor, event publishing
//   - payment_orchestrator_saga.go: saga orchestration, state transitions, failure handling
//   - payment_orchestrator_ledger.go: double-entry ledger posting (standard and clearing flows)
//   - payment_orchestrator_lien.go: async lien execution with retry and distributed locking
type PaymentOrchestrator struct {
	logger                    *slog.Logger
	repo                      persistence.Repository
	currentAccountClient      CurrentAccountClient
	paymentGateway            gateway.PaymentGateway
	financialAccountingClient FinancialAccountingClient
	internalAccountClient     InternalAccountClient // Optional - for internal clearing
	referenceDataClient       ReferenceDataClient   // Optional - for bucket-aware solvency and GetSaga()
	bucketEvaluator           *BucketEvaluator      // Cached CEL evaluator for bucket IDs
	accountResolver           *AccountResolver      // Optional - resolves clearing accounts dynamically
	gatewayAccountConfig      *config.GatewayAccountConfig
	kafkaPublisher            KafkaPublisher
	lienExecutionRetryConfig  *sharedclients.RetryConfig
	internalClearingEnabled   bool
	lockClient                LockClient // Distributed lock client for preventing concurrent lien execution

	// Starlark saga execution fields
	starlarkRunner           *saga.StarlarkSagaRunner // Executes saga scripts
	handlerRegistry          *saga.HandlerRegistry    // Registry of payment-order handlers
	sagaExecutionLogger      domain.SagaExecutionLogger
	sagaOrchestrationEnabled bool // Feature flag: when true, use Starlark saga orchestration
}

// PaymentOrchestratorConfig contains dependencies for creating a PaymentOrchestrator
type PaymentOrchestratorConfig struct {
	Logger                    *slog.Logger
	Repo                      persistence.Repository
	CurrentAccountClient      CurrentAccountClient
	PaymentGateway            gateway.PaymentGateway
	FinancialAccountingClient FinancialAccountingClient
	InternalAccountClient     InternalAccountClient // Optional - for internal clearing
	ReferenceDataClient       ReferenceDataClient   // Optional - for bucket-aware solvency validation
	AccountResolver           *AccountResolver      // Optional - auto-created if InternalAccountClient is provided
	GatewayAccountConfig      *config.GatewayAccountConfig
	KafkaPublisher            KafkaPublisher
	LienExecutionRetryConfig  *sharedclients.RetryConfig
	InternalClearingEnabled   bool
	LockClient                LockClient // Distributed lock client for preventing concurrent lien execution

	// HandlerRegistry is an optional external handler registry with cross-service handlers
	// (e.g., party.get_default_payment_method, current_account.*). When provided, the
	// orchestrator registers its internal payment_order.* handlers on this registry and
	// uses it for the StarlarkSagaRunner, giving saga scripts access to all handlers.
	// When nil, a new empty registry is created (backward compatible).
	HandlerRegistry *saga.HandlerRegistry

	// SagaExecutionLogger persists saga execution records for audit. Optional.
	SagaExecutionLogger domain.SagaExecutionLogger

	// SagaOrchestrationEnabled controls whether the Starlark saga orchestration
	// path is used. When false, a stub error is returned to callers of
	// ExecutePaymentSaga, and Orchestrate uses the existing Go-based flow.
	SagaOrchestrationEnabled bool
}

// NewPaymentOrchestrator creates a new payment orchestrator with the given dependencies.
// Returns an error if required dependencies (Logger, Repo) are nil. CurrentAccountClient and
// PaymentGateway are validated at runtime in Orchestrate() with graceful error handling.
//
// If InternalAccountClient is provided but AccountResolver is nil, an AccountResolver
// is automatically created using the client and logger.
func NewPaymentOrchestrator(cfg PaymentOrchestratorConfig) (*PaymentOrchestrator, error) {
	if cfg.Logger == nil {
		return nil, ErrOrchestratorLoggerNil
	}
	if cfg.Repo == nil {
		return nil, ErrOrchestratorRepoNil
	}

	accountResolver, err := resolveOrCreateAccountResolver(cfg)
	if err != nil {
		return nil, err
	}

	bucketEvaluator, err := NewBucketEvaluator(cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket evaluator: %w", err)
	}

	handlerRegistry, handlerDeps, err := buildHandlerRegistry(cfg, bucketEvaluator)
	if err != nil {
		return nil, err
	}

	starlarkRunner, err := buildStarlarkRunner(cfg, handlerRegistry)
	if err != nil {
		return nil, err
	}

	orchestrator := &PaymentOrchestrator{
		logger:                    cfg.Logger,
		repo:                      cfg.Repo,
		currentAccountClient:      cfg.CurrentAccountClient,
		paymentGateway:            cfg.PaymentGateway,
		financialAccountingClient: cfg.FinancialAccountingClient,
		internalAccountClient:     cfg.InternalAccountClient,
		referenceDataClient:       cfg.ReferenceDataClient,
		bucketEvaluator:           bucketEvaluator,
		accountResolver:           accountResolver,
		gatewayAccountConfig:      cfg.GatewayAccountConfig,
		kafkaPublisher:            cfg.KafkaPublisher,
		lienExecutionRetryConfig:  cfg.LienExecutionRetryConfig,
		internalClearingEnabled:   cfg.InternalClearingEnabled,
		lockClient:                cfg.LockClient,
		starlarkRunner:            starlarkRunner,
		handlerRegistry:           handlerRegistry,
		sagaExecutionLogger:       cfg.SagaExecutionLogger,
		sagaOrchestrationEnabled:  cfg.SagaOrchestrationEnabled,
	}

	// Set orchestrator reference in handler deps for PostLedgerEntries callback
	handlerDeps.Orchestrator = orchestrator

	return orchestrator, nil
}

// resolveOrCreateAccountResolver returns the configured AccountResolver or auto-creates one
// when InternalAccountClient is provided but AccountResolver is nil.
func resolveOrCreateAccountResolver(cfg PaymentOrchestratorConfig) (*AccountResolver, error) {
	if cfg.AccountResolver != nil {
		return cfg.AccountResolver, nil
	}
	if cfg.InternalAccountClient == nil {
		return nil, nil
	}
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: cfg.InternalAccountClient,
		Logger: cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create account resolver: %w", err)
	}
	return resolver, nil
}

// buildHandlerRegistry creates the handler registry and registers payment-order handlers.
func buildHandlerRegistry(cfg PaymentOrchestratorConfig, bucketEvaluator *BucketEvaluator) (*saga.HandlerRegistry, *PaymentOrderHandlerDeps, error) {
	handlerRegistry := cfg.HandlerRegistry
	if handlerRegistry == nil {
		handlerRegistry = saga.NewHandlerRegistry()
	}

	handlerDeps := &PaymentOrderHandlerDeps{
		CurrentAccountClient:      cfg.CurrentAccountClient,
		PaymentGateway:            cfg.PaymentGateway,
		FinancialAccountingClient: cfg.FinancialAccountingClient,
		ReferenceDataClient:       cfg.ReferenceDataClient,
		BucketEvaluator:           bucketEvaluator,
		LienExecutionRetryConfig:  cfg.LienExecutionRetryConfig,
		Logger:                    cfg.Logger,
		Orchestrator:              nil, // Will be set after orchestrator creation
	}

	if err := RegisterPaymentOrderHandlers(handlerRegistry, handlerDeps); err != nil {
		return nil, nil, fmt.Errorf("failed to register payment order handlers: %w", err)
	}

	return handlerRegistry, handlerDeps, nil
}

// buildStarlarkRunner creates the Starlark runtime and saga runner.
func buildStarlarkRunner(cfg PaymentOrchestratorConfig, handlerRegistry *saga.HandlerRegistry) (*saga.StarlarkSagaRunner, error) {
	runtime, err := saga.NewRuntime(cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create starlark runtime: %w", err)
	}

	serviceModules, err := sagaschema.BuildServiceModules(handlerRegistry)
	if err != nil {
		return nil, fmt.Errorf("failed to build service modules: %w", err)
	}

	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       handlerRegistry,
		ServiceModules: serviceModules,
		Logger:         cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create starlark saga runner: %w", err)
	}

	return runner, nil
}

// publishEvent publishes a Kafka event if the publisher is configured.
// This is best-effort/fire-and-forget: errors are logged but not retried or persisted.
func (o *PaymentOrchestrator) publishEvent(ctx context.Context, topic string, key string, event proto.Message) {
	if o.kafkaPublisher == nil {
		return
	}
	if err := o.kafkaPublisher.Publish(ctx, topic, key, event); err != nil {
		o.logger.Error("failed to publish event",
			"topic", topic,
			"key", key,
			"error", err)
	} else {
		o.logger.Info("published event",
			"topic", topic,
			"key", key)
	}
}

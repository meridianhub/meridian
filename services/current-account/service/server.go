// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/santhosh-tekuri/jsonschema/v5"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// Sentinel errors are defined in errors.go for better organization.
// See errors.go for ErrRepositoryNil, ErrNilPositionLog, etc.

// Operation status constants for consistency across the service
// Note: Some constants are shared with lien_service.go in this package
const (
	operationStatusSuccess         = "success"
	operationStatusFailed          = "failed"
	operationStatusInvalidCurrency = "invalid_currency"
	opStatusIdempotent             = "idempotent"
	// Shared constants - also defined in lien_service.go:
	// opStatusAccountNotFound, opStatusRetrieveFailed, opStatusCurrencyMismatch,
	// opStatusInvalidAmount, opStatusSaveFailed, opStatusInsufficientFunds
	opStatusAmountOverflow          = "amount_overflow"
	opStatusAccountFrozen           = "account_frozen"
	opStatusAccountClosed           = "account_closed"
	opStatusMissingAccountID        = "missing_account_id"
	opStatusMissingAmount           = "missing_amount"
	opStatusMissingWithdrawalID     = "missing_withdrawal_id"
	opStatusMissingIdentifier       = "missing_identifier"
	opStatusNotImplemented          = "not_implemented"
	opStatusWithdrawalFailed        = "withdrawal_failed"
	opStatusWithdrawalNotFound      = "withdrawal_not_found"
	opStatusSagaFailed              = "saga_failed"
	opStatusInvalidStatusTransition = "invalid_status_transition"
)

// Redis idempotency constants
const (
	// idempotencyNamespace is the Redis key namespace for current-account idempotency
	idempotencyNamespace = "current-account"

	// idempotencyPendingTTL is how long a pending idempotency record remains valid
	idempotencyPendingTTL = 5 * time.Minute

	// idempotencyResultTTL is how long completed results are cached
	idempotencyResultTTL = 24 * time.Hour
)

// Kafka topic aliases for account lifecycle events.
// These reference the centralized topic registry in shared/platform/events/topics.
const (
	// TopicAccountFrozen is the Kafka topic for account frozen events.
	TopicAccountFrozen = topics.CurrentAccountAccountFrozenV1
	// TopicAccountUnfrozen is the Kafka topic for account unfrozen events.
	TopicAccountUnfrozen = topics.CurrentAccountAccountUnfrozenV1
	// TopicAccountClosed is the Kafka topic for account closed events.
	TopicAccountClosed = topics.CurrentAccountAccountClosedV1
)

// AccountEventPublisher defines the interface for publishing account lifecycle events.
// Implementations should handle serialization, delivery, and error handling.
type AccountEventPublisher interface {
	// PublishWithTenant publishes a protobuf message with tenant context to a Kafka topic.
	// The key is used for partitioning - events with the same key go to the same partition.
	PublishWithTenant(ctx context.Context, topic, key string, msg proto.Message) error
}

// NoOpAccountEventPublisher is a no-operation implementation of AccountEventPublisher.
// Useful for testing and scenarios where event publishing is not configured.
type NoOpAccountEventPublisher struct{}

// WebhookNotifier defines the interface for sending webhook notifications.
// Used for regulatory compliance notifications on account lifecycle events.
type WebhookNotifier interface {
	// NotifyAccountFrozen sends a webhook notification for an account freeze event.
	// Returns nil if the tenant has no webhook URL configured (not an error case).
	NotifyAccountFrozen(ctx context.Context, tenantID, accountID, reason string, timestamp time.Time) error

	// NotifyAccountClosed sends a webhook notification for an account closure event.
	// Returns nil if the tenant has no webhook URL configured (not an error case).
	NotifyAccountClosed(ctx context.Context, tenantID, accountID, reason string, balance *WebhookBalanceInfo, timestamp time.Time) error
}

// WebhookBalanceInfo contains account balance details for webhook payloads.
type WebhookBalanceInfo struct {
	// Amount is the balance amount in minor units (e.g., cents).
	Amount int64
	// CurrencyCode is the ISO 4217 currency code.
	CurrencyCode string
}

// NoOpWebhookNotifier is a no-operation implementation of WebhookNotifier.
// Useful for testing and scenarios where webhook notifications are not configured.
type NoOpWebhookNotifier struct{}

// NotifyAccountFrozen does nothing and always returns nil.
func (n *NoOpWebhookNotifier) NotifyAccountFrozen(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

// NotifyAccountClosed does nothing and always returns nil.
func (n *NoOpWebhookNotifier) NotifyAccountClosed(_ context.Context, _, _, _ string, _ *WebhookBalanceInfo, _ time.Time) error {
	return nil
}

// PublishWithTenant does nothing and always returns nil.
func (p *NoOpAccountEventPublisher) PublishWithTenant(_ context.Context, _, _ string, _ proto.Message) error {
	return nil
}

// CachedAccountType holds a resolved product type definition with precompiled programs.
// This mirrors the fields from cache.CachedAccountType that the service needs.
type CachedAccountType struct {
	Definition         *accounttype.Definition
	EligibilityProgram cel.Program
	CompiledSchema     *jsonschema.Schema
}

// AccountTypeCache defines the interface for resolving product type definitions.
// Implemented by cache.LocalAccountTypeCache (via an adapter).
type AccountTypeCache interface {
	GetOrLoad(ctx context.Context, tenantID tenant.TenantID, code string) (*CachedAccountType, error)
}

// Option is a functional option for configuring optional Service dependencies.
type Option func(*Service)

// WithInstrumentGetter sets the instrument getter for instrument resolution from Reference Data.
// Required for account creation - the service returns FailedPrecondition if not configured.
func WithInstrumentGetter(getter InstrumentGetter) Option {
	return func(s *Service) {
		s.instrumentGetter = getter
	}
}

// WithNotificationSagaHandler injects a real notification.send saga handler,
// replacing the default stub that returns errHandlerNotImplemented.
//
// Constructor-only: this option is consumed during saga runner construction
// in NewServiceWithExistingClients. Calling ApplyOptions with this option
// after construction has no effect on the already-built saga runner.
func WithNotificationSagaHandler(handler saga.Handler) Option {
	return func(s *Service) {
		s.notificationHandler = handler
	}
}

// WithAccountTypeCache sets the account type cache for product type resolution.
func WithAccountTypeCache(cache AccountTypeCache) Option {
	return func(s *Service) {
		s.accountTypeCache = cache
	}
}

// WithValuationFeatureRepo sets the valuation feature repository for VF seeding.
func WithValuationFeatureRepo(repo *persistence.ValuationFeatureRepository) Option {
	return func(s *Service) {
		s.valuationFeatureRepo = repo
	}
}

// WithValuationEngine sets the valuation engine for executing valuation logic.
func WithValuationEngine(engine ValuationEngine) Option {
	return func(s *Service) {
		s.valuationEngine = engine
	}
}

// WithEventPublisher sets the event publisher for lifecycle events.
func WithEventPublisher(publisher AccountEventPublisher) Option {
	return func(s *Service) {
		s.eventPublisher = publisher
	}
}

// WithWebhookNotifier sets the webhook notifier for lifecycle events.
func WithWebhookNotifier(notifier WebhookNotifier) Option {
	return func(s *Service) {
		s.webhookNotifier = notifier
	}
}

// ApplyOptions applies functional options to the service.
// This allows optional dependencies to be set after construction.
func (s *Service) ApplyOptions(opts ...Option) {
	for _, opt := range opts {
		opt(s)
	}
}

// Service implements the CurrentAccountService gRPC service
type Service struct {
	pb.UnimplementedCurrentAccountServiceServer
	repo                   *persistence.Repository
	lienRepo               *persistence.LienRepository
	withdrawalRepo         *persistence.WithdrawalRepository
	valuationFeatureRepo   *persistence.ValuationFeatureRepository // ValuationFeature repository for valuation method assignments
	outboxRepo             events.OutboxRepository                 // Outbox repository for reliable event delivery
	outboxPublisher        *events.OutboxPublisher                 // OutboxPublisher for transactional event writing
	db                     *gorm.DB                                // Database connection for transaction management
	posKeepingClient       PositionKeepingClient
	finAcctClient          FinancialAccountingClient
	partyClient            PartyClient
	instrumentGetter       InstrumentGetter // Required: resolves instrument properties from Reference Data
	accountTypeCache       AccountTypeCache // Optional: resolves product type definitions
	valuationEngine        ValuationEngine  // Optional: executes valuation method logic
	accountConfig          *config.AccountConfig
	idempotencyService     idempotency.Service
	eventPublisher         AccountEventPublisher // Optional: publishes lifecycle events to Kafka (deprecated: use outboxPublisher)
	webhookNotifier        WebhookNotifier       // Optional: sends webhook notifications for lifecycle events
	logger                 *slog.Logger
	tracer                 *observability.Tracer
	notificationHandler    saga.Handler            // Optional: real notification.send handler (replaces stub)
	depositOrchestrator    *DepositOrchestrator    // Handles deposit saga orchestration
	withdrawalOrchestrator *WithdrawalOrchestrator // Handles withdrawal saga orchestration
}

// Config contains configuration for creating a new Service with external clients
type Config struct {
	Repository                 *persistence.Repository
	LienRepository             *persistence.LienRepository
	WithdrawalRepository       *persistence.WithdrawalRepository
	ValuationFeatureRepository *persistence.ValuationFeatureRepository
	// Namespace is the Kubernetes namespace for service discovery (e.g., "default")
	Namespace string
	// PositionKeepingServiceName is the Kubernetes service name (e.g., "position-keeping")
	PositionKeepingServiceName string
	// PositionKeepingPort is the service port (e.g., 50053)
	PositionKeepingPort int
	// FinancialAccountingServiceName is the Kubernetes service name (e.g., "financial-accounting")
	FinancialAccountingServiceName string
	// FinancialAccountingPort is the service port (e.g., 50052)
	FinancialAccountingPort int
	// PartyServiceName is the Kubernetes service name (e.g., "party")
	PartyServiceName string
	// PartyPort is the service port (e.g., 50055)
	PartyPort int
	Logger    *slog.Logger
	Tracer    *observability.Tracer
}

// NewService creates a new current account service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithClients.
// Returns an error if repository is nil.
func NewService(repo *persistence.Repository, lienRepo *persistence.LienRepository) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// NewServiceWithIdempotency creates a new current account service with idempotency support.
// This is primarily used for testing idempotency paths.
// Returns an error if repository is nil.
func NewServiceWithIdempotency(repo *persistence.Repository, lienRepo *persistence.LienRepository, idempotencyService idempotency.Service) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:               repo,
		lienRepo:           lienRepo,
		idempotencyService: idempotencyService,
		logger:             slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// NewServiceWithValuationFeatures creates a new current account service with valuation feature support.
// This is primarily used for testing valuation feature operations.
// Returns an error if repository is nil.
func NewServiceWithValuationFeatures(repo *persistence.Repository, valuationFeatureRepo *persistence.ValuationFeatureRepository) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:                 repo,
		valuationFeatureRepo: valuationFeatureRepo,
		logger:               slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// NewServiceWithExistingClients creates a new service with pre-created client instances.
// This constructor is useful when clients need to be shared with other components
// (e.g., health checkers) to avoid creating duplicate connections.
//
// Optional parameters:
//   - accountConfig: Static clearing account configuration (environment variables)
//   - accountResolver: Dynamic clearing account resolution from Internal Account service
//   - fungibilityValidator: Validates fungibility for non-fungible instruments
//
// If both accountConfig and accountResolver are provided, the resolver takes precedence
// with fallback to static config.
func NewServiceWithExistingClients(
	repo *persistence.Repository,
	lienRepo *persistence.LienRepository,
	withdrawalRepo *persistence.WithdrawalRepository,
	outboxRepo events.OutboxRepository, // Outbox repository for reliable event delivery
	db *gorm.DB, // Database connection for transaction management
	posKeepingClient PositionKeepingClient,
	finAcctClient FinancialAccountingClient,
	partyClient PartyClient,
	accountConfig *config.AccountConfig,
	idempotencyService idempotency.Service,
	logger *slog.Logger,
	tracer *observability.Tracer,
	accountResolver *AccountResolver, // Optional: dynamic clearing account resolution
	fungibilityValidator *FungibilityValidator, // Optional: validates fungibility for non-fungible instruments
	opts ...Option,
) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}

	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Pre-scan options for saga handler configuration (needed before buildSagaRunner).
	var handlerOpts []RegisterCurrentAccountHandlersOption
	probe := &Service{}
	for _, opt := range opts {
		if opt != nil {
			opt(probe)
		}
	}
	if probe.notificationHandler != nil {
		handlerOpts = append(handlerOpts, WithNotificationHandler(probe.notificationHandler))
	}

	// Initialize saga infrastructure (scripts, registry, runtime, runner)
	sagaRunner, depositScript, withdrawalScript, err := buildSagaRunner(logger, handlerOpts...)
	if err != nil {
		return nil, err
	}

	// Create orchestrators
	depositOrchestrator, withdrawalOrchestrator, err := buildOrchestrators(
		logger, repo, posKeepingClient, finAcctClient, accountConfig,
		accountResolver, fungibilityValidator, sagaRunner, depositScript, withdrawalScript,
	)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		repo:                   repo,
		lienRepo:               lienRepo,
		withdrawalRepo:         withdrawalRepo,
		outboxRepo:             outboxRepo,
		outboxPublisher:        events.NewOutboxPublisher("current-account"),
		db:                     db,
		posKeepingClient:       posKeepingClient,
		finAcctClient:          finAcctClient,
		partyClient:            partyClient,
		accountConfig:          accountConfig,
		idempotencyService:     idempotencyService,
		logger:                 logger,
		tracer:                 tracer,
		depositOrchestrator:    depositOrchestrator,
		withdrawalOrchestrator: withdrawalOrchestrator,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc, nil
}

// buildSagaRunner initializes the saga infrastructure: loads scripts, registers handlers, and creates the runner.
func buildSagaRunner(logger *slog.Logger, handlerOpts ...RegisterCurrentAccountHandlersOption) (*saga.StarlarkSagaRunner, string, string, error) {
	depositScript, err := loadSagaAsset(filepath.Join("services", "reference-data", "saga", "defaults", "deposit", "v1.0.0.star"))
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to load deposit saga script: %w", err)
	}
	withdrawalScript, err := loadSagaAsset(filepath.Join("services", "reference-data", "saga", "defaults", "withdrawal", "v1.0.0.star"))
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to load withdrawal saga script: %w", err)
	}

	handlerRegistry := saga.NewHandlerRegistry()
	if err := RegisterCurrentAccountHandlers(handlerRegistry, handlerOpts...); err != nil {
		return nil, "", "", fmt.Errorf("failed to register saga handlers: %w", err)
	}

	serviceModules, err := schema.BuildServiceModules(handlerRegistry)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to build service modules: %w", err)
	}

	runtime, err := saga.NewRuntime(logger)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create saga runtime: %w", err)
	}

	sagaRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       handlerRegistry,
		ServiceModules: serviceModules,
		Logger:         logger,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create saga runner: %w", err)
	}

	return sagaRunner, depositScript, withdrawalScript, nil
}

// buildOrchestrators creates the deposit and withdrawal orchestrators.
func buildOrchestrators(
	logger *slog.Logger,
	repo *persistence.Repository,
	posKeepingClient PositionKeepingClient,
	finAcctClient FinancialAccountingClient,
	accountConfig *config.AccountConfig,
	accountResolver *AccountResolver,
	fungibilityValidator *FungibilityValidator,
	sagaRunner *saga.StarlarkSagaRunner,
	depositScript, withdrawalScript string,
) (*DepositOrchestrator, *WithdrawalOrchestrator, error) {
	depositOrchestrator, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		PosKeepingClient:     posKeepingClient,
		FinAcctClient:        finAcctClient,
		AccountConfig:        accountConfig,
		AccountResolver:      accountResolver,
		FungibilityValidator: fungibilityValidator,
		SagaRunner:           sagaRunner,
		DepositScript:        depositScript,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create deposit orchestrator: %w", err)
	}

	withdrawalOrchestrator, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		PosKeepingClient:     posKeepingClient,
		FinAcctClient:        finAcctClient,
		AccountConfig:        accountConfig,
		AccountResolver:      accountResolver,
		FungibilityValidator: fungibilityValidator,
		SagaRunner:           sagaRunner,
		WithdrawalScript:     withdrawalScript,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create withdrawal orchestrator: %w", err)
	}

	return depositOrchestrator, withdrawalOrchestrator, nil
}

// loadSagaAsset loads a saga asset (script or schema) from a configurable base directory.
// Resolves assets from SAGA_ASSET_DIR environment variable if set, otherwise falls back
// to the directory containing the executable. This makes asset loading independent of
// build paths and working directory, supporting containerized deployments.
func loadSagaAsset(relativePath string) (string, error) {
	baseDir := os.Getenv("SAGA_ASSET_DIR")
	if baseDir == "" {
		// Fallback to executable directory
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("failed to resolve saga asset dir: %w", err)
		}
		baseDir = filepath.Dir(exe)
	}

	assetPath := filepath.Join(baseDir, relativePath)
	content, err := os.ReadFile(assetPath)
	if err != nil {
		return "", fmt.Errorf("failed to read saga asset %s: %w", assetPath, err)
	}
	return string(content), nil
}

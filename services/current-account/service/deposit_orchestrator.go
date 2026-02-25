// Package service implements gRPC services for the current account domain
//
//nolint:staticcheck // Uses AmountCents() for logging (deprecated for backward compatibility)
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DepositOrchestrator encapsulates deposit saga orchestration logic.
// It handles the multi-step deposit workflow including position keeping logging,
// ledger posting with double-entry bookkeeping, and account balance persistence.
type DepositOrchestrator struct {
	logger               *slog.Logger
	repo                 *persistence.Repository
	posKeepingClient     PositionKeepingClient
	finAcctClient        FinancialAccountingClient
	accountConfig        *config.AccountConfig
	accountResolver      *AccountResolver
	fungibilityValidator *FungibilityValidator
	sagaRunner           *saga.StarlarkSagaRunner
	depositScript        string
	sagaResolver         *refsaga.ProductTypeSagaResolver
}

// DepositOrchestratorConfig contains dependencies for creating a DepositOrchestrator
type DepositOrchestratorConfig struct {
	Logger           *slog.Logger
	Repo             *persistence.Repository
	PosKeepingClient PositionKeepingClient
	FinAcctClient    FinancialAccountingClient
	AccountConfig    *config.AccountConfig
	// AccountResolver enables dynamic clearing account resolution from Internal Account service.
	// If provided, takes precedence over AccountConfig for clearing account lookup.
	// If nil, falls back to static AccountConfig environment variables.
	AccountResolver *AccountResolver
	// FungibilityValidator validates fungibility for non-fungible instruments.
	// If nil, fungibility validation is skipped (fully fungible instruments only).
	FungibilityValidator *FungibilityValidator
	// SagaRunner executes Starlark saga definitions.
	SagaRunner *saga.StarlarkSagaRunner
	// DepositScript is the Starlark script for the default deposit saga.
	// Used when SagaResolver is nil or the account has no ProductTypeCode.
	DepositScript string
	// SagaResolver optionally resolves the saga definition from the registry
	// based on the account's ProductTypeCode. When set and the account has a
	// ProductTypeCode, the resolved script is used instead of DepositScript.
	SagaResolver *refsaga.ProductTypeSagaResolver
}

// NewDepositOrchestrator creates a new deposit orchestrator with the given dependencies.
// Returns an error if required dependencies (Logger, Repo, PosKeepingClient, FinAcctClient, SagaRunner, DepositScript) are nil.
func NewDepositOrchestrator(cfg DepositOrchestratorConfig) (*DepositOrchestrator, error) {
	if cfg.Logger == nil {
		return nil, ErrOrchestratorLoggerNil
	}
	if cfg.Repo == nil {
		return nil, ErrOrchestratorRepositoryNil
	}
	if cfg.PosKeepingClient == nil {
		return nil, ErrOrchestratorPosKeepingClientNil
	}
	if cfg.FinAcctClient == nil {
		return nil, ErrOrchestratorFinAcctClientNil
	}
	if cfg.SagaRunner == nil {
		return nil, ErrOrchestratorSagaRunnerNil
	}
	if cfg.DepositScript == "" {
		return nil, ErrOrchestratorDepositScriptEmpty
	}
	return &DepositOrchestrator{
		logger:               cfg.Logger,
		repo:                 cfg.Repo,
		posKeepingClient:     cfg.PosKeepingClient,
		finAcctClient:        cfg.FinAcctClient,
		accountConfig:        cfg.AccountConfig,
		accountResolver:      cfg.AccountResolver,
		fungibilityValidator: cfg.FungibilityValidator,
		sagaRunner:           cfg.SagaRunner,
		depositScript:        cfg.DepositScript,
		sagaResolver:         cfg.SagaResolver,
	}, nil
}

// Orchestrate executes the deposit saga with compensation on failure.
//
// Saga Steps (executed strictly sequentially - no concurrent execution):
//  1. log_position: Create CREDIT entry in PositionKeeping service (balance source of truth)
//  2. post_ledger: Create booking log and dual ledger postings in FinancialAccounting service
//  3. save_account: Persist account metadata (status, version) - balance NOT stored locally
//
// The saga uses the SagaOrchestrator which ensures steps run one at a time. Domain objects
// (account, amount) are safely shared across steps since only one step executes at a time.
//
// Compensation Order (LIFO - Last In, First Out):
//   - save_account fails → compensate post_ledger (reverse postings), then log_position
//   - post_ledger fails → compensate log_position only
//   - log_position fails → no compensation needed
//
// Thread Safety: This method is not thread-safe for concurrent calls with the same account.
// Callers must use optimistic locking (version field) or database-level locking when
// processing deposits for the same account concurrently. The repository layer enforces
// optimistic locking via ErrVersionConflict.
//
// Parameters:
//   - attributes: Optional key-value pairs for fungibility validation. For non-fungible
//     instruments (e.g., RICE-KG with batch tracking), both debit and credit sides
//     of the double-entry must have matching fungibility keys. If nil, no fungibility
//     validation is performed (suitable for fully fungible instruments like USD).
func (o *DepositOrchestrator) Orchestrate(ctx context.Context, account domain.CurrentAccount, amount domain.Money, transactionID string, attributes map[string]string) (*pb.ExecuteDepositResponse, error) {
	sagaStart := time.Now()
	sagaStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordSagaDuration("deposit", sagaStatus, time.Since(sagaStart))
	}()

	// Extract or generate correlation ID
	correlationID := sharedclients.ExtractCorrelationID(ctx)
	if correlationID == "" {
		correlationID = uuid.New().String()
		o.logger.Info("generated new correlation ID", "correlation_id", correlationID)
		ctx = observability.WithCorrelationID(ctx, correlationID)
	} else {
		o.logger.Info("using existing correlation ID", "correlation_id", correlationID)
	}

	// Validate fungibility before starting the saga.
	// For double-entry deposits: DEBIT from clearing account, CREDIT to customer account
	// Both sides use the same attributes since this is a single incoming deposit.
	if o.fungibilityValidator != nil {
		instrumentCode := string(amount.Currency())
		if err := o.fungibilityValidator.ValidateDoubleEntry(ctx, instrumentCode, 1, attributes, attributes); err != nil {
			sagaStatus = operationStatusFailed
			o.logger.Error("fungibility validation failed",
				"account_id", account.AccountID(),
				"transaction_id", transactionID,
				"instrument", instrumentCode,
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "fungibility validation failed: %v", err)
		}
		o.logger.Debug("fungibility validation passed",
			"account_id", account.AccountID(),
			"instrument", instrumentCode)
	}

	// Resolve clearing account ID (dynamic resolver preferred, fallback to static config)
	clearingAccountID := o.resolveClearingAccountID(ctx, string(amount.Currency()))

	// Parse correlation ID safely - fallback to generated ID if invalid
	correlationUUID, parseErr := uuid.Parse(correlationID)
	if parseErr != nil {
		correlationUUID = uuid.New()
		correlationID = correlationUUID.String()
		o.logger.Warn("invalid correlation ID format; generated new one",
			"correlation_id", correlationID,
			"error", parseErr)
		ctx = observability.WithCorrelationID(ctx, correlationID)
	}

	// Prepare saga input
	input := saga.RunnerInput{
		SagaExecutionID: uuid.New(),
		CorrelationID:   correlationUUID,
		Input: map[string]interface{}{
			"account_id":             account.AccountID(),
			"account_identification": account.AccountIdentification(),
			"amount":                 amount.Amount().String(), // Decimal as string
			"currency":               string(amount.Currency()),
			"transaction_id":         transactionID,
			"clearing_account_id":    clearingAccountID,
			"attributes":             attributes,
		},
	}

	// Inject handler dependencies into context
	ctx = context.WithValue(ctx, ContextKeyHandlerDeps, &CurrentAccountHandlerDeps{
		Logger:           o.logger,
		PosKeepingClient: o.posKeepingClient,
		FinAcctClient:    o.finAcctClient,
		Repo:             o.repo,
	})
	ctx = context.WithValue(ctx, ContextKeyAccount, account)

	// Resolve saga script: use ProductTypeSagaResolver when configured and account has a product type,
	// otherwise fall back to the static default deposit script.
	depositScript, err := o.resolveDepositScript(ctx, account)
	if err != nil {
		sagaStatus = operationStatusFailed
		o.logger.Error("failed to resolve deposit saga",
			"account_id", account.AccountID(),
			"product_type_code", account.ProductTypeCode(),
			"error", err)
		return nil, status.Errorf(codes.NotFound, "deposit saga not found for product type: %v", err)
	}

	// Execute saga via StarlarkSagaRunner
	o.logger.Info("executing deposit saga via Starlark",
		"account_id", account.AccountID(),
		"transaction_id", transactionID,
		"saga_execution_id", input.SagaExecutionID,
		"correlation_id", correlationID,
		"product_type_code", account.ProductTypeCode())

	output, err := o.sagaRunner.ExecuteSaga(ctx, "current_account_deposit", depositScript, input)
	if err != nil {
		sagaStatus = operationStatusFailed
		o.logger.Error("deposit saga failed",
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "deposit saga failed: %v", err)
	}

	// Handle saga result
	if !output.Success {
		sagaStatus = operationStatusFailed
		caobservability.RecordSagaFailure("deposit", "saga_execution")

		o.logger.Error("deposit saga failed",
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"error", output.Error)

		return nil, status.Errorf(codes.Internal,
			"deposit transaction failed: %s", output.Error)
	}

	o.logger.Info("deposit saga completed successfully",
		"account_id", account.AccountID(),
		"transaction_id", transactionID,
		"correlation_id", correlationID,
		"saga_execution_id", input.SagaExecutionID)

	// Return successful response
	return &pb.ExecuteDepositResponse{
		AccountId:        account.AccountID(),
		TransactionId:    transactionID,
		NewBalance:       toMoneyAmount(account.Balance()),
		AvailableBalance: toMoneyAmount(account.AvailableBalance()),
		Status:           pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
	}, nil
}

// resolveClearingAccountID resolves the clearing account ID for deposit operations.
// Priority:
//  1. AccountResolver (dynamic lookup from Internal Account service)
//  2. AccountConfig (static environment variable fallback)
//
// Returns empty string if neither is configured (single-entry mode).
// All error cases are handled internally with fallback behavior.
func (o *DepositOrchestrator) resolveClearingAccountID(ctx context.Context, currency string) string {
	// Try dynamic resolver first (preferred)
	if o.accountResolver != nil {
		accountID, err := o.accountResolver.GetDepositClearingAccount(ctx, currency)
		if err != nil {
			// Log but don't fail - allow fallback to static config
			o.logger.Warn("dynamic clearing account resolution failed, trying static config",
				"currency", currency,
				"error", err)
		} else {
			o.logger.Debug("resolved clearing account dynamically",
				"currency", currency,
				"account_id", accountID)
			return accountID
		}
	}

	// Fallback to static config
	if o.accountConfig != nil && o.accountConfig.DepositClearingAccountID != "" {
		o.logger.Debug("using static clearing account from config",
			"account_id", o.accountConfig.DepositClearingAccountID)
		return o.accountConfig.DepositClearingAccountID
	}

	// Neither configured - single-entry mode
	o.logger.Debug("no clearing account configured, operating in single-entry mode")
	return ""
}

// resolveDepositScript returns the saga script to execute for the given account.
//
// Resolution order:
//  1. If SagaResolver is configured and the account has a ProductTypeCode, resolve the
//     saga definition from the registry using ProductTypeSagaResolver. The definition's
//     ResolvedScript is used. If the resolver returns ErrSagaNotFound, the error is
//     propagated (fail-fast: a prefixed product type must have a corresponding saga).
//  2. Otherwise, fall back to the static default DepositScript.
func (o *DepositOrchestrator) resolveDepositScript(ctx context.Context, account domain.CurrentAccount) (string, error) {
	if o.sagaResolver == nil || account.ProductTypeCode() == "" {
		return o.depositScript, nil
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		// No tenant context — fall back to static script
		return o.depositScript, nil
	}

	def, err := o.sagaResolver.ResolveForProductType(ctx, tenantID, account.ProductTypeCode(), "deposit")
	if err != nil {
		if errors.Is(err, refsaga.ErrSagaNotFound) {
			// Prefix is defined but no saga found — fail fast per PRD convention
			return "", err
		}
		// ErrNotFound (product type or generic saga missing) — fall back to static script
		o.logger.Warn("saga resolution fell through to default deposit script",
			"product_type_code", account.ProductTypeCode(),
			"error", err)
		return o.depositScript, nil
	}

	if def.ResolvedScript == "" {
		// Definition has no resolved script — fall back to static script
		o.logger.Warn("resolved saga definition has no script, using default",
			"saga_name", def.Name,
			"product_type_code", account.ProductTypeCode())
		return o.depositScript, nil
	}

	o.logger.Debug("using product-type-specific deposit saga",
		"saga_name", def.Name,
		"product_type_code", account.ProductTypeCode())
	return def.ResolvedScript, nil
}

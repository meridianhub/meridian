// Package service implements gRPC services for the current account domain
//
//meridian:large-file - known oversized file; split tracked in backlog
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Posting direction constants.
const (
	directionDebit     = "DEBIT"
	directionCredit    = "CREDIT"
	sagaTypeDeposit    = "deposit"
	sagaTypeWithdrawal = "withdrawal"
)

// Sentinel errors for handler operations.
var (
	errHandlerDepsNotFound   = errors.New("handler dependencies not found in context")
	errInvalidHandlerDeps    = errors.New("invalid handler dependencies type")
	errAccountNotFound       = errors.New("account not found in context")
	errInvalidAccountType    = errors.New("invalid account type")
	errInvalidDirection      = errors.New("invalid direction")
	errNilPositionLog        = errors.New("nil position log from service")
	errNilBookingLog         = errors.New("nil booking log from service")
	errNilPosting            = errors.New("nil posting from service")
	errInvalidStatus         = errors.New("invalid status")
	errMissingParameter      = errors.New("missing required parameter")
	errInvalidParameterType  = errors.New("invalid parameter type")
	errHandlerNotImplemented = errors.New("handler not implemented")
	errPosKeepingClientNil   = errors.New("position keeping client not available - delegated to saga layer")
	errFinAcctClientNil      = errors.New("financial accounting client not available - delegated to saga layer")
)

// CurrentAccountHandlerDeps contains dependencies needed by Current Account saga handlers.
// These are injected into the StarlarkContext at runtime.
type CurrentAccountHandlerDeps struct {
	Logger           *slog.Logger
	PosKeepingClient PositionKeepingClient
	FinAcctClient    FinancialAccountingClient
	Repo             *persistence.Repository
}

// ContextKey type for type-safe context keys.
type contextKey string

// Context keys for handler dependencies.
const (
	// ContextKeyHandlerDeps is the key for CurrentAccountHandlerDeps in StarlarkContext.
	ContextKeyHandlerDeps contextKey = "current_account_handler_deps"
	// ContextKeyAccount is the key for the domain.CurrentAccount in StarlarkContext.
	ContextKeyAccount contextKey = "current_account"
)

// stubNotImplemented returns a stub handler that returns an error indicating the handler is not implemented.
// This is used for handlers defined in the schema but not yet implemented in this service.
func stubNotImplemented(handlerName string) saga.Handler {
	return func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, fmt.Errorf("%w: %s", errHandlerNotImplemented, handlerName)
	}
}

// RegisterCurrentAccountHandlersOption configures optional handler overrides
// for RegisterCurrentAccountHandlers.
type RegisterCurrentAccountHandlersOption func(*registerOptions)

type registerOptions struct {
	notificationHandler saga.Handler
}

// WithNotificationHandler replaces the notification.send stub with a real handler.
func WithNotificationHandler(handler saga.Handler) RegisterCurrentAccountHandlersOption {
	return func(o *registerOptions) {
		o.notificationHandler = handler
	}
}

// RegisterCurrentAccountHandlers registers all Current Account-specific step handlers
// with the given HandlerRegistry. These handlers are used by the Starlark
// saga runtime to execute withdrawal and deposit operations.
//
// Handler naming convention matches the handler schema:
//   - position_keeping.* for position service handlers
//   - financial_accounting.* for financial accounting handlers
//   - current_account.* for domain-specific handlers
func RegisterCurrentAccountHandlers(registry *saga.HandlerRegistry, opts ...RegisterCurrentAccountHandlersOption) error {
	options := &registerOptions{}
	for _, opt := range opts {
		opt(options)
	}

	notifHandler := stubNotImplemented("notification.send")
	if options.notificationHandler != nil {
		notifHandler = options.notificationHandler
	}

	handlers := []struct {
		name     string
		handler  saga.Handler
		metadata *saga.HandlerMetadata
	}{
		// Position Keeping handlers (global namespace)
		{"position_keeping.initiate_log", currentAccountPositionKeepingInitiateLog, &saga.HandlerMetadata{
			Category:             saga.HandlerCategoryIngestion,
			CompensationStrategy: "auto",
			HasAutoCompensation:  true,
			Compensate:           "position_keeping.cancel_log",
		}},
		{"position_keeping.update_log", stubNotImplemented("position_keeping.update_log"), &saga.HandlerMetadata{
			Category:             saga.HandlerCategoryIngestion,
			CompensationStrategy: "none",
		}},
		{"position_keeping.cancel_log", currentAccountPositionKeepingCancelLog, &saga.HandlerMetadata{
			Category:             saga.HandlerCategoryIngestion,
			CompensationStrategy: "none",
		}},

		// Financial Accounting handlers (global namespace)
		{"financial_accounting.post_entries", stubNotImplemented("financial_accounting.post_entries"), nil},
		{"financial_accounting.reverse_entries", stubNotImplemented("financial_accounting.reverse_entries"), nil},
		{"financial_accounting.create_booking", stubNotImplemented("financial_accounting.create_booking"), nil},
		{"financial_accounting.initiate_booking_log", currentAccountFinAcctInitiateBookingLog, &saga.HandlerMetadata{
			Category:             saga.HandlerCategorySettlement,
			CompensationStrategy: "none",
		}},
		{"financial_accounting.capture_posting", currentAccountFinAcctCapturePosting, &saga.HandlerMetadata{
			Category:             saga.HandlerCategorySettlement,
			CompensationStrategy: "auto",
			Compensate:           "financial_accounting.compensate_posting",
			HasAutoCompensation:  true,
		}},
		{"financial_accounting.update_booking_log", currentAccountFinAcctUpdateBookingLog, &saga.HandlerMetadata{
			Category:             saga.HandlerCategorySettlement,
			CompensationStrategy: "none",
		}},
		{"financial_accounting.compensate_posting", currentAccountFinAcctCompensatePosting, &saga.HandlerMetadata{
			Category:             saga.HandlerCategorySettlement,
			CompensationStrategy: "none",
		}},

		// Current Account domain handlers
		{"current_account.save", currentAccountRepositorySave, nil},

		// Control handler (stub - implemented in client package for cross-service use)
		{"current_account.control", stubNotImplemented("current_account.control"), nil},

		// Lien handlers (stubs - not yet implemented but required by schema)
		{"current_account.create_lien", stubNotImplemented("current_account.create_lien"), nil},
		{"current_account.execute_lien", stubNotImplemented("current_account.execute_lien"), nil},
		{"current_account.terminate_lien", stubNotImplemented("current_account.terminate_lien"), nil},

		// Platform-wide handlers (stubs - defined in schema for other services)
		{"notification.send", notifHandler, nil},
		{"payment_order.create_lien", stubNotImplemented("payment_order.create_lien"), nil},
		{"payment_order.execute_lien", stubNotImplemented("payment_order.execute_lien"), nil},
		{"payment_order.post_ledger_entries", stubNotImplemented("payment_order.post_ledger_entries"), nil},
		{"payment_order.send_to_gateway", stubNotImplemented("payment_order.send_to_gateway"), nil},
		{"payment_order.terminate_lien", stubNotImplemented("payment_order.terminate_lien"), nil},
		{"repository.save", stubNotImplemented("repository.save"), nil},
		{"valuation_engine.valuate", stubNotImplemented("valuation_engine.valuate"), nil},

		// Reconciliation handlers (stubs - defined in schema for reconciliation service)
		{"reconciliation.initiate_run", stubNotImplemented("reconciliation.initiate_run"), nil},
		{"reconciliation.execute_run", stubNotImplemented("reconciliation.execute_run"), nil},
		{"reconciliation.retrieve_run", stubNotImplemented("reconciliation.retrieve_run"), nil},
		{"reconciliation.cancel_run", stubNotImplemented("reconciliation.cancel_run"), nil},
		{"reconciliation.assert_balance", stubNotImplemented("reconciliation.assert_balance"), nil},
		{"reconciliation.initiate_dispute", stubNotImplemented("reconciliation.initiate_dispute"), nil},

		// Party handlers (stubs - defined in schema for party service)
		{"party.get_default_payment_method", stubNotImplemented("party.get_default_payment_method"), nil},

		// Operational Gateway handlers (stubs - defined in schema for operational gateway service)
		{"operational_gateway.dispatch_instruction", stubNotImplemented("operational_gateway.dispatch_instruction"), nil},
		{"operational_gateway.cancel_instruction", stubNotImplemented("operational_gateway.cancel_instruction"), nil},
		{"operational_gateway.get_instruction", stubNotImplemented("operational_gateway.get_instruction"), nil},

		// Financial Gateway handlers (stubs - defined in schema for financial gateway service)
		{"financial_gateway.dispatch_payment", stubNotImplemented("financial_gateway.dispatch_payment"), nil},
		{"financial_gateway.cancel_payment", stubNotImplemented("financial_gateway.cancel_payment"), nil},
		{"financial_gateway.dispatch_refund", stubNotImplemented("financial_gateway.dispatch_refund"), nil},

		// Forecasting handlers (stubs - defined in schema for forecasting service)
		{"forecasting.compute_forward_curve", stubNotImplemented("forecasting.compute_forward_curve"), nil},

		// Market Information handlers (stubs - defined in schema for market information service)
		{"market_information.publish_observation", stubNotImplemented("market_information.publish_observation"), nil},
		{"market_information.query_latest", stubNotImplemented("market_information.query_latest"), nil},
		{"market_information.manage_dataset", stubNotImplemented("market_information.manage_dataset"), nil},

		// Reference Data handlers (stubs - defined in schema for reference data service)
		{"reference_data.register_instrument", stubNotImplemented("reference_data.register_instrument"), nil},
		{"reference_data.delete_instrument", stubNotImplemented("reference_data.delete_instrument"), nil},
		{"reference_data.register_account_type", stubNotImplemented("reference_data.register_account_type"), nil},
		{"reference_data.delete_account_type", stubNotImplemented("reference_data.delete_account_type"), nil},
		{"reference_data.register_valuation_rule", stubNotImplemented("reference_data.register_valuation_rule"), nil},
		{"reference_data.register_saga_definition", stubNotImplemented("reference_data.register_saga_definition"), nil},

		// Internal Account handlers (stubs - defined in schema for internal account service)
		{"internal_account.initiate", stubNotImplemented("internal_account.initiate"), nil},
	}

	for _, h := range handlers {
		if err := registry.RegisterWithMetadata(h.name, h.handler, h.metadata); err != nil {
			return fmt.Errorf("failed to register handler %s: %w", h.name, err)
		}
	}

	return nil
}

// getDeps extracts handler dependencies from the StarlarkContext.
func getDeps(ctx *saga.StarlarkContext) (*CurrentAccountHandlerDeps, error) {
	val := ctx.Value(ContextKeyHandlerDeps)
	if val == nil {
		return nil, errHandlerDepsNotFound
	}
	deps, ok := val.(*CurrentAccountHandlerDeps)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errInvalidHandlerDeps, val)
	}
	return deps, nil
}

// getAccount extracts the domain account from StarlarkContext.
// The account is stored as a value type (domain.CurrentAccount is immutable).
func getAccount(ctx *saga.StarlarkContext) (domain.CurrentAccount, error) {
	val := ctx.Value(ContextKeyAccount)
	if val == nil {
		return domain.CurrentAccount{}, errAccountNotFound
	}
	account, ok := val.(domain.CurrentAccount)
	if !ok {
		return domain.CurrentAccount{}, fmt.Errorf("%w: got %T", errInvalidAccountType, val)
	}
	return account, nil
}

// decimalToMoneyAmount converts a decimal amount and currency to a proto MoneyAmount.
func decimalToMoneyAmount(amount decimal.Decimal, currency string) *commonpb.MoneyAmount {
	// Convert decimal to units and nanos
	// Amount is in major units (e.g., 100.50 = 100 units + 500000000 nanos)
	units := amount.IntPart()
	remainder := amount.Sub(decimal.NewFromInt(units))
	nanos := remainder.Mul(decimal.NewFromInt(1e9)).IntPart()

	return &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: currency,
			Units:        units,
			Nanos:        int32(nanos),
		},
	}
}

// currentAccountPositionKeepingInitiateLog creates a position log entry for a withdrawal/deposit.
//
// Required params:
//   - account_id: string - The account identifier
//   - amount: decimal.Decimal - The transaction amount
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - direction: string - "DEBIT" or "CREDIT"
//   - transaction_id: string - The saga transaction ID
//
// Returns:
//
//	map[string]any{
//	  "log_id":  string - The created position log ID
//	  "version": int64  - The position log version
//	  "status":  string - "INITIATED"
//	}
func currentAccountPositionKeepingInitiateLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.position_keeping.initiate_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Extract required parameters
	// Accept either position_id (schema primary) or account_id (legacy alias)
	accountID, ok := params["position_id"].(string)
	if !ok || accountID == "" {
		accountID, ok = params["account_id"].(string)
		if !ok || accountID == "" {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: position_id or account_id", errMissingParameter))
		}
	}

	amount, err := requireDecimal(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Accept instrument_code (preferred) or currency (deprecated alias)
	currency := optionalString(params, "instrument_code")
	if currency == "" {
		currency = optionalString(params, "currency")
	}
	if currency == "" {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: instrument_code", errMissingParameter))
	}

	direction, err := requireString(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Validate direction
	var pbDirection commonpb.PostingDirection
	switch direction {
	case directionDebit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_DEBIT
	case directionCredit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_CREDIT
	default:
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", errInvalidDirection, direction))
	}

	// Determine description based on direction
	description := fmt.Sprintf("Deposit to account %s", accountID)
	idempKeyPrefix := sagaTypeDeposit
	if direction == directionDebit {
		description = fmt.Sprintf("Withdrawal from account %s", accountID)
		idempKeyPrefix = sagaTypeWithdrawal
	}

	// Extract optional valuation_analysis parameter
	var attributes map[string]string
	if valuationAnalysis, ok := params["valuation_analysis"]; ok && valuationAnalysis != nil {
		// Marshal valuation_analysis to JSON for storage in attributes
		bytes, marshalErr := json.Marshal(valuationAnalysis)
		if marshalErr != nil {
			deps.Logger.Warn("failed to marshal valuation_analysis",
				"error", marshalErr,
				"transaction_id", transactionID)
		} else {
			attributes = map[string]string{
				"valuation_analysis": string(bytes),
			}
			deps.Logger.Debug("including valuation_analysis in position attributes",
				"transaction_id", transactionID,
				"analysis_size", len(bytes))
		}
	}

	deps.Logger.Info("executing position_keeping.initiate_log",
		"account_id", accountID,
		"transaction_id", transactionID,
		"direction", direction,
		"has_valuation_analysis", attributes != nil)

	// Create proto amount
	protoAmount := decimalToMoneyAmount(amount, currency)

	// Build transaction log entry
	initialEntry := &positionkeepingv1.TransactionLogEntry{
		EntryId:       uuid.New().String(),
		TransactionId: transactionID,
		AccountId:     accountID,
		Amount:        protoAmount,
		Direction:     pbDirection,
		Timestamp:     timestamppb.Now(),
		Description:   description,
		Attributes:    attributes,
	}

	// Call Position Keeping service
	if deps.PosKeepingClient == nil {
		return nil, wrapHandlerError(handlerName, errPosKeepingClientNil)
	}
	resp, err := deps.PosKeepingClient.InitiateFinancialPositionLog(ctx,
		&positionkeepingv1.InitiateFinancialPositionLogRequest{
			AccountId:    accountID,
			InitialEntry: initialEntry,
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("%s-%s-%s", idempKeyPrefix, accountID, transactionID),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("position_keeping", "initiate_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to log position: %w", err))
	}
	if resp.Log == nil {
		caobservability.RecordExternalServiceError("position_keeping", "initiate_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: transaction %s", errNilPositionLog, transactionID))
	}

	deps.Logger.Info("position_keeping.initiate_log completed",
		"log_id", resp.Log.LogId,
		"version", resp.Log.Version,
		"transaction_id", transactionID)

	return map[string]any{
		"log_id":  resp.Log.LogId,
		"version": resp.Log.Version,
		"status":  "INITIATED",
	}, nil
}

// currentAccountPositionKeepingCancelLog cancels a position log entry (compensation).
//
// Required params:
//   - log_id: string - The position log ID to cancel
//   - version: int64 - The position log version
//   - transaction_id: string - The saga transaction ID
//   - account_id: string - The account ID
//   - direction: string - "DEBIT" or "CREDIT" (for idempotency key)
func currentAccountPositionKeepingCancelLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.position_keeping.cancel_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	logID, err := requireString(params, "log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	version, err := requireInt64(params, "version")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireString(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	direction, err := requireString(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	idempKeyPrefix := sagaTypeDeposit
	sagaType := sagaTypeDeposit
	if direction == directionDebit {
		idempKeyPrefix = sagaTypeWithdrawal
		sagaType = sagaTypeWithdrawal
	}

	if deps.PosKeepingClient == nil {
		return nil, wrapHandlerError(handlerName, errPosKeepingClientNil)
	}

	deps.Logger.Info("compensating position_keeping.cancel_log",
		"log_id", logID,
		"version", version,
		"transaction_id", transactionID)

	_, err = deps.PosKeepingClient.UpdateFinancialPositionLog(ctx,
		&positionkeepingv1.UpdateFinancialPositionLogRequest{
			LogId:   logID,
			Version: version,
			StatusUpdate: &positionkeepingv1.StatusTracking{
				CurrentStatus:   commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
				StatusUpdatedAt: timestamppb.Now(),
				StatusReason:    fmt.Sprintf("Saga compensation for failed %s transaction %s", sagaType, transactionID),
			},
			AuditEntry: &positionkeepingv1.AuditTrailEntry{
				AuditId:   uuid.New().String(),
				Timestamp: timestamppb.Now(),
				UserId:    "system",
				Action:    "saga_compensation",
				Details:   fmt.Sprintf("Cancelled position log due to %s saga failure for transaction %s", sagaType, transactionID),
			},
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("compensate-%s-%s-%s", idempKeyPrefix, accountID, transactionID),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("position_keeping", "compensate_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to compensate position log: %w", err))
	}

	caobservability.RecordSagaCompensation(sagaType, "log_position")

	deps.Logger.Info("position_keeping.cancel_log completed",
		"log_id", logID)

	return map[string]any{
		"log_id": logID,
		"status": "CANCELLED",
	}, nil
}

// currentAccountFinAcctInitiateBookingLog creates a financial booking log.
//
// Required params:
//   - account_id: string - The account identifier
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - transaction_id: string - The saga transaction ID
//   - transaction_type: string - "WITHDRAWAL" or "DEPOSIT"
func currentAccountFinAcctInitiateBookingLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.initiate_booking_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireString(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Accept instrument_code (preferred) or currency (deprecated alias)
	currency := optionalString(params, "instrument_code")
	if currency == "" {
		currency = optionalString(params, "currency")
	}
	if currency == "" {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: instrument_code", errMissingParameter))
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionType, err := requireString(params, "transaction_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.initiate_booking_log",
		"account_id", accountID,
		"transaction_id", transactionID,
		"transaction_type", transactionType)

	resp, err := deps.FinAcctClient.InitiateFinancialBookingLog(ctx,
		&financialaccountingv1.InitiateFinancialBookingLogRequest{
			FinancialAccountType:    "CURRENT",
			ProductServiceReference: accountID,
			BusinessUnitReference:   "current-account-service",
			ChartOfAccountsRules:    transactionType,
			BaseInstrumentCode:      currency,
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("booking-log-%s", transactionID),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("financial_accounting", "initiate_booking_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to initiate booking log: %w", err))
	}
	if resp.FinancialBookingLog == nil {
		caobservability.RecordExternalServiceError("financial_accounting", "initiate_booking_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: transaction %s", errNilBookingLog, transactionID))
	}

	deps.Logger.Info("financial_accounting.initiate_booking_log completed",
		"booking_log_id", resp.FinancialBookingLog.Id,
		"transaction_id", transactionID)

	return map[string]any{
		"booking_log_id": resp.FinancialBookingLog.Id,
		"status":         "CREATED",
	}, nil
}

// currentAccountFinAcctCapturePosting captures a ledger posting (debit or credit).
//
// Required params:
//   - booking_log_id: string - The booking log ID
//   - account_id: string - The account to post to
//   - amount: decimal.Decimal - The posting amount
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - direction: string - "DEBIT" or "CREDIT"
//   - transaction_id: string - The saga transaction ID
//   - posting_type: string - "debit" or "credit" (for idempotency key suffix)
func currentAccountFinAcctCapturePosting(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.capture_posting"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	bookingLogID, err := requireString(params, "booking_log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireString(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amount, err := requireDecimal(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Accept instrument_code (preferred) or currency (deprecated alias)
	currency := optionalString(params, "instrument_code")
	if currency == "" {
		currency = optionalString(params, "currency")
	}
	if currency == "" {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: instrument_code", errMissingParameter))
	}

	direction, err := requireString(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	postingType, err := requireString(params, "posting_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	var pbDirection commonpb.PostingDirection
	switch direction {
	case directionDebit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_DEBIT
	case directionCredit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_CREDIT
	default:
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", errInvalidDirection, direction))
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.capture_posting",
		"booking_log_id", bookingLogID,
		"account_id", accountID,
		"direction", direction,
		"posting_type", postingType)

	protoAmount := decimalToMoneyAmount(amount, currency)

	resp, err := deps.FinAcctClient.CaptureLedgerPosting(ctx,
		&financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: bookingLogID,
			PostingDirection:      pbDirection,
			PostingAmount:         protoAmount.Amount,
			AccountId:             accountID,
			ValueDate:             timestamppb.Now(),
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("%s-%s", transactionID, postingType),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("financial_accounting", "capture_"+postingType+"_posting")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to capture %s posting: %w", postingType, err))
	}
	if resp.LedgerPosting == nil {
		caobservability.RecordExternalServiceError("financial_accounting", "capture_"+postingType+"_posting")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s posting for transaction %s", errNilPosting, postingType, transactionID))
	}

	deps.Logger.Info("financial_accounting.capture_posting completed",
		"posting_id", resp.LedgerPosting.Id,
		"posting_type", postingType,
		"transaction_id", transactionID)

	return map[string]any{
		"posting_id": resp.LedgerPosting.Id,
		"status":     "POSTED",
	}, nil
}

// currentAccountFinAcctUpdateBookingLog updates a booking log status (typically to POSTED).
//
// Required params:
//   - booking_log_id: string - The booking log ID
//   - status: string - The new status (e.g., "POSTED", "CANCELLED")
func currentAccountFinAcctUpdateBookingLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.update_booking_log"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	bookingLogID, err := requireString(params, "booking_log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	statusStr, err := requireString(params, "status")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	var pbStatus commonpb.TransactionStatus
	switch statusStr {
	case "POSTED":
		pbStatus = commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED
	case "CANCELLED":
		pbStatus = commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED
	default:
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", errInvalidStatus, statusStr))
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.update_booking_log",
		"booking_log_id", bookingLogID,
		"status", statusStr)

	_, err = deps.FinAcctClient.UpdateFinancialBookingLog(ctx,
		&financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     bookingLogID,
			Status: pbStatus,
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("update-booking-log-%s-%s", bookingLogID, statusStr),
			},
		},
	)
	if err != nil {
		caobservability.RecordExternalServiceError("financial_accounting", "update_booking_log")
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to update booking log: %w", err))
	}

	deps.Logger.Info("financial_accounting.update_booking_log completed",
		"booking_log_id", bookingLogID,
		"status", statusStr)

	return map[string]any{
		"booking_log_id": bookingLogID,
		"status":         statusStr,
	}, nil
}

// currentAccountFinAcctCompensatePosting creates a compensating posting entry.
//
// Required params:
//   - booking_log_id: string - The booking log ID
//   - account_id: string - The account to post to
//   - amount: decimal.Decimal - The posting amount
//   - instrument_code: string - Instrument code (e.g., "GBP", "kWh"). Replaces currency.
//   - currency: string - Deprecated: use instrument_code instead.
//   - direction: string - "DEBIT" or "CREDIT" (opposite of original)
//   - transaction_id: string - The saga transaction ID
//   - posting_type: string - "debit" or "credit" (original posting type being compensated)
func currentAccountFinAcctCompensatePosting(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.financial_accounting.compensate_posting"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	bookingLogID, err := requireString(params, "booking_log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireString(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amount, err := requireDecimal(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Accept instrument_code (preferred) or currency (deprecated alias)
	currency := optionalString(params, "instrument_code")
	if currency == "" {
		currency = optionalString(params, "currency")
	}
	if currency == "" {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: instrument_code", errMissingParameter))
	}

	direction, err := requireString(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	postingType, err := requireString(params, "posting_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	var pbDirection commonpb.PostingDirection
	switch direction {
	case directionDebit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_DEBIT
	case directionCredit:
		pbDirection = commonpb.PostingDirection_POSTING_DIRECTION_CREDIT
	default:
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", errInvalidDirection, direction))
	}

	if deps.FinAcctClient == nil {
		return nil, wrapHandlerError(handlerName, errFinAcctClientNil)
	}

	deps.Logger.Info("executing financial_accounting.compensate_posting",
		"booking_log_id", bookingLogID,
		"account_id", accountID,
		"direction", direction,
		"posting_type", postingType)

	protoAmount := decimalToMoneyAmount(amount, currency)

	_, err = deps.FinAcctClient.CaptureLedgerPosting(ctx,
		&financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: bookingLogID,
			PostingDirection:      pbDirection,
			PostingAmount:         protoAmount.Amount,
			AccountId:             accountID,
			ValueDate:             timestamppb.Now(),
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("COMP-%s-%s", transactionID, postingType),
			},
		},
	)
	if err != nil {
		// CRITICAL: Manual intervention required - ledger may be inconsistent
		deps.Logger.Error("CRITICAL: failed to compensate posting - manual ledger reconciliation required",
			"booking_log_id", bookingLogID,
			"account_id", accountID,
			"transaction_id", transactionID,
			"error", err,
			"runbook", "docs/runbooks/saga-failure-recovery.md")
		caobservability.RecordInlineCompensationFailure("current_account", postingType)
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to compensate %s posting: %w", postingType, err))
	}

	deps.Logger.Info("financial_accounting.compensate_posting completed",
		"booking_log_id", bookingLogID,
		"posting_type", postingType)

	return map[string]any{
		"status": "COMPENSATED",
	}, nil
}

// currentAccountRepositorySave persists the account to the database.
//
// This handler retrieves the account from the StarlarkContext (injected at runtime)
// and saves it using the repository.
//
// Required context values:
//   - ContextKeyAccount: *domain.CurrentAccount
//
// Required params:
//   - account_id: string - For logging purposes
//   - transaction_id: string - For logging purposes
func currentAccountRepositorySave(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.repository.save"

	deps, err := getDeps(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	account, err := getAccount(ctx)
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireString(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireString(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	deps.Logger.Info("executing repository.save",
		"account_id", accountID,
		"transaction_id", transactionID,
		"version", account.Version())

	if err := deps.Repo.Save(ctx, account); err != nil {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to save account: %w", err))
	}

	deps.Logger.Info("repository.save completed",
		"account_id", accountID,
		"version", account.Version())

	return map[string]any{
		"account_id": accountID,
		"version":    account.Version(),
		"status":     "SAVED",
	}, nil
}

// Helper functions for parameter extraction

// optionalString extracts a string parameter, returning empty string if absent or wrong type.
func optionalString(params map[string]any, key string) string {
	val, ok := params[key]
	if !ok || val == nil {
		return ""
	}
	str, ok := val.(string)
	if !ok {
		return ""
	}
	return str
}

func requireString(params map[string]any, key string) (string, error) {
	val, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", errMissingParameter, key)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be string, got %T", errInvalidParameterType, key, val)
	}
	return str, nil
}

func requireDecimal(params map[string]any, key string) (decimal.Decimal, error) {
	val, ok := params[key]
	if !ok {
		return decimal.Zero, fmt.Errorf("%w: %s", errMissingParameter, key)
	}
	switch v := val.(type) {
	case decimal.Decimal:
		return v, nil
	case string:
		d, err := decimal.NewFromString(v)
		if err != nil {
			return decimal.Zero, fmt.Errorf("%w: %s must be decimal, got invalid string: %w", errInvalidParameterType, key, err)
		}
		return d, nil
	case float64:
		return decimal.NewFromFloat(v), nil
	case int:
		return decimal.NewFromInt(int64(v)), nil
	case int64:
		return decimal.NewFromInt(v), nil
	default:
		return decimal.Zero, fmt.Errorf("%w: %s must be decimal, got %T", errInvalidParameterType, key, val)
	}
}

func requireInt64(params map[string]any, key string) (int64, error) {
	val, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", errMissingParameter, key)
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%w: %s must be int64, got %T", errInvalidParameterType, key, val)
	}
}

func wrapHandlerError(handlerName string, err error) error {
	return fmt.Errorf("%s: %w", handlerName, err)
}

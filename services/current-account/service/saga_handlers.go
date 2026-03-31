package service

import (
	"errors"
	"fmt"
	"log/slog"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
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

	handlers := currentAccountCoreHandlers(notifHandler)
	handlers = append(handlers, currentAccountStubHandlers()...)

	for _, h := range handlers {
		if err := registry.RegisterWithMetadata(h.name, h.handler, h.metadata); err != nil {
			return fmt.Errorf("failed to register handler %s: %w", h.name, err)
		}
	}
	return nil
}

type handlerEntry struct {
	name     string
	handler  saga.Handler
	metadata *saga.HandlerMetadata
}

// currentAccountCoreHandlers returns the implemented handlers for position keeping,
// financial accounting, and current account domain operations.
func currentAccountCoreHandlers(notifHandler saga.Handler) []handlerEntry {
	return []handlerEntry{
		// Position Keeping handlers
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

		// Financial Accounting handlers
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
		{"current_account.control", stubNotImplemented("current_account.control"), nil},
		{"current_account.create_lien", stubNotImplemented("current_account.create_lien"), nil},
		{"current_account.execute_lien", stubNotImplemented("current_account.execute_lien"), nil},
		{"current_account.terminate_lien", stubNotImplemented("current_account.terminate_lien"), nil},

		// Correspondence (BIAN-aligned, replaces notification)
		{"correspondence.initiate_outbound", notifHandler, nil},

		// Deprecated alias for backward compatibility
		{"notification.send", notifHandler, nil},
	}
}

// currentAccountStubHandlers returns stub handlers for cross-service operations
// that are defined in the schema but implemented by other services.
func currentAccountStubHandlers() []handlerEntry {
	return []handlerEntry{
		{"payment_order.create_lien", stubNotImplemented("payment_order.create_lien"), nil},
		{"payment_order.execute_lien", stubNotImplemented("payment_order.execute_lien"), nil},
		{"payment_order.post_ledger_entries", stubNotImplemented("payment_order.post_ledger_entries"), nil},
		{"payment_order.send_to_gateway", stubNotImplemented("payment_order.send_to_gateway"), nil},
		{"payment_order.terminate_lien", stubNotImplemented("payment_order.terminate_lien"), nil},
		{"repository.save", stubNotImplemented("repository.save"), nil},
		{"valuation_engine.valuate", stubNotImplemented("valuation_engine.valuate"), nil},
		{"reconciliation.initiate_run", stubNotImplemented("reconciliation.initiate_run"), nil},
		{"reconciliation.execute_run", stubNotImplemented("reconciliation.execute_run"), nil},
		{"reconciliation.retrieve_run", stubNotImplemented("reconciliation.retrieve_run"), nil},
		{"reconciliation.cancel_run", stubNotImplemented("reconciliation.cancel_run"), nil},
		{"reconciliation.assert_balance", stubNotImplemented("reconciliation.assert_balance"), nil},
		{"reconciliation.initiate_dispute", stubNotImplemented("reconciliation.initiate_dispute"), nil},
		{"party.get_default_payment_method", stubNotImplemented("party.get_default_payment_method"), nil},
		{"operational_gateway.dispatch_instruction", stubNotImplemented("operational_gateway.dispatch_instruction"), nil},
		{"operational_gateway.cancel_instruction", stubNotImplemented("operational_gateway.cancel_instruction"), nil},
		{"operational_gateway.get_instruction", stubNotImplemented("operational_gateway.get_instruction"), nil},
		{"financial_gateway.dispatch_payment", stubNotImplemented("financial_gateway.dispatch_payment"), nil},
		{"financial_gateway.cancel_payment", stubNotImplemented("financial_gateway.cancel_payment"), nil},
		{"financial_gateway.dispatch_refund", stubNotImplemented("financial_gateway.dispatch_refund"), nil},
		{"forecasting.compute_forward_curve", stubNotImplemented("forecasting.compute_forward_curve"), nil},
		{"market_information.publish_observation", stubNotImplemented("market_information.publish_observation"), nil},
		{"market_information.query_latest", stubNotImplemented("market_information.query_latest"), nil},
		{"market_information.manage_dataset", stubNotImplemented("market_information.manage_dataset"), nil},
		{"reference_data.register_instrument", stubNotImplemented("reference_data.register_instrument"), nil},
		{"reference_data.delete_instrument", stubNotImplemented("reference_data.delete_instrument"), nil},
		{"reference_data.register_account_type", stubNotImplemented("reference_data.register_account_type"), nil},
		{"reference_data.delete_account_type", stubNotImplemented("reference_data.delete_account_type"), nil},
		{"reference_data.register_valuation_rule", stubNotImplemented("reference_data.register_valuation_rule"), nil},
		{"reference_data.register_saga_definition", stubNotImplemented("reference_data.register_saga_definition"), nil},
		{"internal_account.initiate", stubNotImplemented("internal_account.initiate"), nil},
	}
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

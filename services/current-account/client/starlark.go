// Package client provides Starlark service bindings for Current Account.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Current Account service integration.
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrInvalidControlAction is returned when an unsupported control action is provided.
var ErrInvalidControlAction = errors.New("current_account.control: invalid action, must be FREEZE, UNFREEZE, or CLOSE")

// RegisterStarlarkHandlers registers all Starlark service bindings for Current Account.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register Current Account handlers
// with the saga execution engine. Each handler includes metadata for conservation rule
// enforcement and operational categorization.
//
// Example usage:
//
//	registry := saga.NewHandlerRegistry()
//	client, cleanup, _ := client.New(client.Config{...})
//	defer cleanup()
//	err := RegisterStarlarkHandlers(registry, client)
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
	handlers := map[string]struct {
		handler  saga.Handler
		metadata saga.HandlerMetadata
	}{
		"current_account.create_lien": {
			handler: createLienHandler(client),
			metadata: saga.HandlerMetadata{
				Category:    saga.HandlerCategorySettlement,
				Description: "Create a lien (hold) on an account for a specified amount",
				Compensate:  "current_account.terminate_lien",
				// Liens reserve funds in specific currencies (USD, EUR, etc.)
				// ProducesInstruments represents currencies that can be blocked/reserved
				ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"},
				ProtoRequestType:    (*currentaccountv1.InitiateLienRequest)(nil),
				ProtoResponseType:   (*currentaccountv1.InitiateLienResponse)(nil),
				ParamOverrides: map[string]saga.ParamOverride{
					"amount": {Type: "Decimal"},
				},
				Version: 1,
			},
		},
		"current_account.execute_lien": {
			handler: executeLienHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Execute (consume) a previously created lien",
				CompensationStrategy: "none",
				// Execute doesn't produce new instruments, just converts reservation to debit
				ProducesInstruments: []string{},
				ProtoRequestType:    (*currentaccountv1.ExecuteLienRequest)(nil),
				ProtoResponseType:   (*currentaccountv1.ExecuteLienResponse)(nil),
				Version:             1,
			},
		},
		"current_account.terminate_lien": {
			handler: terminateLienHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Terminate (release) a lien without execution (compensation handler)",
				CompensationStrategy: "none",
				// Termination doesn't produce instruments, just releases them
				ProducesInstruments: []string{},
				ProtoRequestType:    (*currentaccountv1.TerminateLienRequest)(nil),
				ProtoResponseType:   (*currentaccountv1.TerminateLienResponse)(nil),
				Version:             1,
			},
		},
		"current_account.save": {
			handler: saveHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Persist current account metadata for a transaction",
				CompensationStrategy: "none",
				// Save is persistence only, doesn't produce instruments
				ProducesInstruments: []string{},
				ProtoRequestType:    (*currentaccountv1.UpdateCurrentAccountRequest)(nil),
				ProtoResponseType:   (*currentaccountv1.UpdateCurrentAccountResponse)(nil),
				Version:             1,
			},
		},
		"current_account.control": {
			handler: controlHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Perform lifecycle control action on an account (FREEZE, UNFREEZE, CLOSE)",
				CompensationStrategy: "saga_managed",
				// Control actions (freeze/unfreeze/close) don't produce instruments
				ProducesInstruments: []string{},
				ProtoRequestType:    (*currentaccountv1.ControlCurrentAccountRequest)(nil),
				ProtoResponseType:   (*currentaccountv1.ControlCurrentAccountResponse)(nil),
				ParamOverrides: map[string]saga.ParamOverride{
					"action": {Type: "enum"},
				},
				Version: 1,
			},
		},
	}

	for name, h := range handlers {
		if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
			return fmt.Errorf("failed to register %s: %w", name, err)
		}
	}
	return nil
}

// createLienHandler creates a fund reservation on an account via gRPC.
// This handler adapts Starlark parameters to the InitiateLien RPC call,
// propagating saga metadata for idempotency, tracing, and bi-temporal queries.
//
// Liens are used in the Payment Order saga to reserve funds before external
// payment execution, ensuring funds are available atomically.
//
// Parameters:
//   - account_id (string): The account identifier to place the lien on
//   - amount (decimal): The amount to reserve
//   - currency (string): The currency code (e.g., "USD", "EUR")
//   - payment_order_reference (string): The Payment Order creating this lien (optional)
//
// Returns a map containing:
//   - lien_id: The unique lien identifier
//   - account_id: The account identifier
//   - amount: The reserved amount as decimal
//   - currency: The currency code
//   - status: Always "ACTIVE" for newly created liens
func createLienHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// 1. Parse Starlark params using helper functions from shared/pkg/saga
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}

		amount, err := saga.RequireDecimalParam(params, "amount")
		if err != nil {
			return nil, err
		}

		currency, err := saga.RequireStringParam(params, "currency")
		if err != nil {
			return nil, err
		}

		// payment_order_reference is optional
		paymentOrderRef := ""
		if val, ok := params["payment_order_reference"].(string); ok {
			paymentOrderRef = val
		}

		// 2. Prepare client context with saga metadata propagation
		clientCtx := prepareClientContext(ctx)

		// 3. Build the request with MoneyAmount proto
		req := &currentaccountv1.InitiateLienRequest{
			AccountId: accountID,
			Amount: &commonv1.MoneyAmount{
				Amount: convertDecimalToMoney(amount, currency),
			},
			PaymentOrderReference: paymentOrderRef,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: ctx.IdempotencyKey,
			},
		}

		// 4. Call REAL gRPC client
		resp, err := client.InitiateLien(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("current_account.create_lien: %w", err)
		}

		// 5. Convert response to Starlark format (map[string]any)
		lien := resp.GetLien()
		lienAmount := lien.GetAmount().GetAmount()
		result := map[string]any{
			"lien_id":    lien.GetLienId(),
			"account_id": lien.GetAccountId(),
			"amount":     convertMoneyToDecimal(lienAmount),
			"currency":   lienAmount.GetCurrencyCode(),
			"status":     "ACTIVE",
		}

		// Add valuation_analysis if basis is present (atomic valuation flow)
		if basis := resp.GetBasis(); basis != nil {
			result["valuation_analysis"] = ConvertValuationAnalysisToMap(basis)
		}

		return result, nil
	}
}

// executeLienHandler converts a reservation to an actual debit atomically.
// Called when the external payment is confirmed as settled.
//
// Parameters:
//   - lien_id (string): The lien identifier to execute
//
// Returns a map containing:
//   - lien_id: The lien identifier
//   - status: Always "EXECUTED"
//   - new_balance: The account balance after debit
func executeLienHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		lienID, err := saga.RequireStringParam(params, "lien_id")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)
		resp, err := client.ExecuteLien(clientCtx, &currentaccountv1.ExecuteLienRequest{
			LienId: lienID,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: ctx.IdempotencyKey,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("current_account.execute_lien: %w", err)
		}

		lien := resp.GetLien()
		newBalance := resp.GetNewBalance().GetAmount()
		return map[string]any{
			"lien_id":     lien.GetLienId(),
			"status":      "EXECUTED",
			"new_balance": convertMoneyToDecimal(newBalance),
			"currency":    newBalance.GetCurrencyCode(),
		}, nil
	}
}

// terminateLienHandler releases a reservation without executing (compensation).
// Called during saga compensation when the external payment fails or is cancelled.
//
// Parameters:
//   - lien_id (string): The lien identifier to terminate
//   - reason (string): Explanation for termination (optional, for audit trail)
//
// Returns a map containing:
//   - lien_id: The lien identifier
//   - status: Always "TERMINATED"
//   - available_balance: The account's available balance after release
func terminateLienHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		lienID, err := saga.RequireStringParam(params, "lien_id")
		if err != nil {
			return nil, err
		}

		// reason is optional
		reason := ""
		if val, ok := params["reason"].(string); ok {
			reason = val
		}

		clientCtx := prepareClientContext(ctx)
		resp, err := client.TerminateLien(clientCtx, &currentaccountv1.TerminateLienRequest{
			LienId: lienID,
			Reason: reason,
		})
		if err != nil {
			return nil, fmt.Errorf("current_account.terminate_lien: %w", err)
		}

		lien := resp.GetLien()
		availableBalance := resp.GetAvailableBalance().GetAmount()
		return map[string]any{
			"lien_id":           lien.GetLienId(),
			"status":            "TERMINATED",
			"available_balance": convertMoneyToDecimal(availableBalance),
			"currency":          availableBalance.GetCurrencyCode(),
		}, nil
	}
}

// saveHandler persists account state updates.
// This handler wraps UpdateCurrentAccount for saga script compatibility.
//
// Parameters:
//   - account_id (string): The account identifier to update
//
// Returns a map containing:
//   - account_id: The account identifier
//   - status: Always "SAVED"
func saveHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}

		req := &currentaccountv1.UpdateCurrentAccountRequest{
			AccountId: accountID,
		}

		clientCtx := prepareClientContext(ctx)

		resp, err := client.UpdateCurrentAccount(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("current_account.save: %w", err)
		}

		facility := resp.GetFacility()
		return map[string]any{
			"account_id": facility.GetAccountId(),
			"status":     "SAVED",
		}, nil
	}
}

// controlHandler performs lifecycle control actions (FREEZE, UNFREEZE, CLOSE) on an account.
// Used by dunning sagas to freeze accounts after payment failures and unfreeze on resolution.
//
// Parameters:
//   - account_id (string): The account identifier to control
//   - action (string): The control action - "FREEZE", "UNFREEZE", or "CLOSE"
//   - reason (string): Explanation for the action (min 10 chars for FREEZE/CLOSE)
//
// Returns a map containing:
//   - account_id: The account identifier
//   - new_status: The account status after the action
//   - action_timestamp: ISO 8601 timestamp of the action
func controlHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}

		action, err := saga.RequireStringParam(params, "action")
		if err != nil {
			return nil, err
		}

		reason, err := saga.RequireStringParam(params, "reason")
		if err != nil {
			return nil, err
		}

		// Validate party scope (tenant isolation)
		if err := ctx.ValidatePartyAccessFromString(accountID); err != nil {
			return nil, fmt.Errorf("current_account.control: %w", err)
		}

		// Map string action to proto enum
		var controlAction currentaccountv1.ControlAction
		switch action {
		case "FREEZE":
			controlAction = currentaccountv1.ControlAction_CONTROL_ACTION_FREEZE
		case "UNFREEZE":
			controlAction = currentaccountv1.ControlAction_CONTROL_ACTION_UNFREEZE
		case "CLOSE":
			controlAction = currentaccountv1.ControlAction_CONTROL_ACTION_CLOSE
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidControlAction, action)
		}

		clientCtx := prepareClientContext(ctx)

		resp, err := client.ControlCurrentAccount(clientCtx, &currentaccountv1.ControlCurrentAccountRequest{
			AccountId:     accountID,
			ControlAction: controlAction,
			Reason:        reason,
		})
		if err != nil {
			return nil, fmt.Errorf("current_account.control: %w", err)
		}

		facility := resp.GetFacility()
		actionTS := resp.GetActionTimestamp()

		// Map proto status to string
		newStatus := facility.GetAccountStatus().String()
		// Strip the "ACCOUNT_STATUS_" prefix for a cleaner Starlark result
		if len(newStatus) > len("ACCOUNT_STATUS_") {
			newStatus = newStatus[len("ACCOUNT_STATUS_"):]
		}

		result := map[string]any{
			"account_id": facility.GetAccountId(),
			"new_status": newStatus,
		}

		if actionTS != nil {
			result["action_timestamp"] = formatTimestamp(actionTS)
		}

		return result, nil
	}
}

// prepareClientContext enriches the gRPC client context with saga metadata.
// This function centralizes metadata propagation logic used by all handlers.
//
// Propagated metadata:
//   - Idempotency key: Ensures duplicate saga replays don't create duplicate records
//   - Knowledge_at timestamp: Enables bi-temporal queries (what we knew at a specific time)
//   - Correlation ID: Links all related operations across the distributed system for tracing
//
// The propagation functions (clients.PropagateIdempotencyKey, etc.) add this metadata
// to the gRPC context's outgoing metadata headers, which downstream services can extract.
// contextKey is a type for context keys to avoid collisions
type contextKey string

// correlationIDContextKey is the typed context key for correlation ID
const correlationIDContextKey contextKey = "x-correlation-id"

func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context

	// Add correlation ID to context value so the client's PropagateCorrelationID can extract it
	clientCtx = context.WithValue(clientCtx, correlationIDContextKey, ctx.CorrelationID.String())

	// Propagate idempotency key and knowledge_at timestamp
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)

	return clientCtx
}

// convertDecimalToMoney converts shopspring/decimal.Decimal to google.type.Money.
// Money proto uses units (int64) and nanos (int32) to represent the amount.
func convertDecimalToMoney(d decimal.Decimal, currencyCode string) *money.Money {
	// Convert decimal to units and nanos
	// Example: 123.456789 -> units: 123, nanos: 456789000
	units := d.IntPart()
	fraction := d.Sub(decimal.NewFromInt(units))
	nanos := fraction.Mul(decimal.NewFromInt(1_000_000_000)).IntPart()

	return &money.Money{
		CurrencyCode: currencyCode,
		Units:        units,
		Nanos:        int32(nanos),
	}
}

// convertMoneyToDecimal converts google.type.Money to shopspring/decimal.Decimal.
// This is the inverse of convertDecimalToMoney, used when receiving gRPC responses.
func convertMoneyToDecimal(m *money.Money) decimal.Decimal {
	if m == nil {
		return decimal.Zero
	}
	// Convert units and nanos back to decimal
	// Example: units: 123, nanos: 456789000 -> 123.456789
	units := decimal.NewFromInt(m.Units)
	nanos := decimal.NewFromInt(int64(m.Nanos)).Div(decimal.NewFromInt(1_000_000_000))
	return units.Add(nanos)
}

// ConvertValuationAnalysisToMap converts a proto ValuationAnalysis to a Starlark-compatible map.
// This preserves the full audit trail from atomic valuation so saga scripts can forward it
// to downstream services (e.g., Position Keeping for ledger attribution).
//
// Exported so that other service handlers (e.g., payment-order) that call InitiateLien
// can also expose valuation_analysis in their saga results.
func ConvertValuationAnalysisToMap(va *currentaccountv1.ValuationAnalysis) map[string]any {
	result := map[string]any{
		"method_id":        va.GetMethodId(),
		"method_version":   va.GetMethodVersion(),
		"calculation_path": va.GetCalculationPath(),
		"degraded_mode":    va.GetDegradedMode(),
	}

	// Convert applied_rates map[string]string to map[string]any for Starlark compatibility
	if rates := va.GetAppliedRates(); len(rates) > 0 {
		ratesMap := make(map[string]any, len(rates))
		for k, v := range rates {
			ratesMap[k] = v
		}
		result["applied_rates"] = ratesMap
	}

	// Convert observation_ids
	if ids := va.GetObservationIds(); len(ids) > 0 {
		result["observation_ids"] = ids
	}

	// Convert timestamps to RFC3339 strings
	if ts := va.GetComputedAt(); ts != nil {
		result["computed_at"] = formatTimestamp(ts)
	}
	if ts := va.GetKnowledgeAt(); ts != nil {
		result["knowledge_at"] = formatTimestamp(ts)
	}

	// Convert market_data_qualities
	if qualities := va.GetMarketDataQualities(); len(qualities) > 0 {
		qualityMaps := make([]map[string]any, 0, len(qualities))
		for _, q := range qualities {
			qm := map[string]any{
				"source":            q.GetSource(),
				"quality_level":     q.GetQualityLevel(),
				"staleness_seconds": q.GetStalenessSeconds(),
			}
			if ts := q.GetObservedAt(); ts != nil {
				qm["observed_at"] = formatTimestamp(ts)
			}
			qualityMaps = append(qualityMaps, qm)
		}
		result["market_data_qualities"] = qualityMaps
	}

	// Convert warnings
	if warnings := va.GetWarnings(); len(warnings) > 0 {
		warningMaps := make([]map[string]any, 0, len(warnings))
		for _, w := range warnings {
			warningMaps = append(warningMaps, map[string]any{
				"code":     w.GetCode(),
				"message":  w.GetMessage(),
				"severity": w.GetSeverity(),
			})
		}
		result["warnings"] = warningMaps
	}

	return result
}

// formatTimestamp converts a protobuf Timestamp to RFC3339 string.
func formatTimestamp(ts *timestamppb.Timestamp) string {
	return ts.AsTime().UTC().Format(time.RFC3339)
}

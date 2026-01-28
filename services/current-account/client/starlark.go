// Package client provides Starlark service bindings for Current Account.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Current Account service integration.
package client

import (
	"context"
	"fmt"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
)

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
				Category: saga.HandlerCategorySettlement,
				// Liens reserve funds in specific currencies (USD, EUR, etc.)
				// ProducesInstruments represents currencies that can be blocked/reserved
				ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"},
			},
		},
		"current_account.execute_lien": {
			handler: executeLienHandler(client),
			metadata: saga.HandlerMetadata{
				Category: saga.HandlerCategorySettlement,
				// Execute doesn't produce new instruments, just converts reservation to debit
				ProducesInstruments: []string{},
			},
		},
		"current_account.terminate_lien": {
			handler: terminateLienHandler(client),
			metadata: saga.HandlerMetadata{
				Category: saga.HandlerCategorySettlement,
				// Termination doesn't produce instruments, just releases them
				ProducesInstruments: []string{},
			},
		},
		"current_account.save": {
			handler: saveHandler(client),
			metadata: saga.HandlerMetadata{
				Category: saga.HandlerCategorySettlement,
				// Save is persistence only, doesn't produce instruments
				ProducesInstruments: []string{},
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
		return map[string]any{
			"lien_id":    lien.GetLienId(),
			"account_id": lien.GetAccountId(),
			"amount":     convertMoneyToDecimal(lienAmount),
			"currency":   lienAmount.GetCurrencyCode(),
			"status":     "ACTIVE",
		}, nil
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
//   - overdraft_limit (decimal): New overdraft limit (optional)
//   - overdraft_enabled (bool): Enable/disable overdraft (optional)
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

		// Build request - all fields are optional except account_id
		req := &currentaccountv1.UpdateCurrentAccountRequest{
			AccountId: accountID,
		}

		// Optional overdraft_limit
		if overdraftLimitVal, ok := params["overdraft_limit"]; ok {
			if overdraftLimit, err := saga.RequireDecimalParam(map[string]any{"overdraft_limit": overdraftLimitVal}, "overdraft_limit"); err == nil && !overdraftLimit.IsZero() {
				// Get currency from params - it should be provided when overdraft_limit is set
				currency := ""
				if currencyVal, ok := params["currency"].(string); ok {
					currency = currencyVal
				}
				req.OverdraftLimit = &commonv1.MoneyAmount{
					Amount: convertDecimalToMoney(overdraftLimit, currency),
				}
			}
		}

		// Optional overdraft_enabled
		if overdraftEnabled, ok := params["overdraft_enabled"].(bool); ok {
			req.OverdraftEnabled = &overdraftEnabled
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

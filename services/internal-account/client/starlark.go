// Package client provides Starlark service bindings for Internal Account.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Internal Account service integration.
package client

import (
	"context"
	"errors"
	"fmt"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

var (
	// ErrCounterpartyAttributesNotMap is returned when counterparty_attributes is not a map.
	ErrCounterpartyAttributesNotMap = errors.New("counterparty_attributes must be a map[string]any")
	// ErrCounterpartyAttributeValueNotString is returned when a counterparty_attributes value is not a string.
	ErrCounterpartyAttributeValueNotString = errors.New("counterparty_attributes value must be a string")
)

// RegisterStarlarkHandlers registers all Starlark service bindings for Internal Account.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register Internal Account handlers
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
		"internal_account.retrieve": {
			handler: retrieveHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             "", // Read operations don't have a specific category
				Description:          "Retrieve an internal account by ID",
				CompensationStrategy: "none",
				// Read operations don't produce instruments
				ProducesInstruments: []string{},
				ProtoRequestType:    (*internalaccountv1.RetrieveInternalAccountRequest)(nil),
				ProtoResponseType:   (*internalaccountv1.RetrieveInternalAccountResponse)(nil),
				Version:             1,
			},
		},
		"internal_account.get_balance": {
			handler: getBalanceHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             "", // Read operations don't have a specific category
				Description:          "Query the current balance for an internal account",
				CompensationStrategy: "none",
				// Balance queries don't produce instruments
				ProducesInstruments: []string{},
				ProtoRequestType:    (*internalaccountv1.GetBalanceRequest)(nil),
				ProtoResponseType:   (*internalaccountv1.GetBalanceResponse)(nil),
				Version:             1,
			},
		},
		"internal_account.initiate": {
			handler: initiateHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Initiate a new internal account",
				CompensationStrategy: "none",
				// Internal accounts don't produce new instruments - they hold existing ones
				// The account creation itself doesn't mint instruments
				ProducesInstruments: []string{},
				ProtoRequestType:    (*internalaccountv1.InitiateInternalAccountRequest)(nil),
				ProtoResponseType:   (*internalaccountv1.InitiateInternalAccountResponse)(nil),
				Version:             1,
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

// retrieveHandler fetches an internal account by ID via gRPC.
// This handler adapts Starlark parameters to the RetrieveInternalAccount RPC call,
// propagating saga metadata for tracing and bi-temporal queries.
//
// Parameters:
//   - account_id (string): The account identifier to retrieve
//
// Returns a map containing:
//   - account_id: The account identifier
//   - account_code: The business-friendly account code
//   - name: The human-readable account name
//   - account_type: The account type (e.g., "NOSTRO", "VOSTRO", "CLEARING")
//   - status: The account status (e.g., "ACTIVE", "SUSPENDED", "CLOSED")
//   - instrument_code: The instrument code (e.g., "USD", "KWH")
func retrieveHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// 1. Parse Starlark params
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}

		// 2. Prepare client context with saga metadata propagation
		clientCtx := prepareClientContext(ctx)

		// 3. Build the request
		req := &internalaccountv1.RetrieveInternalAccountRequest{
			AccountId: accountID,
		}

		// 4. Call gRPC client
		resp, err := client.RetrieveInternalAccount(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("internal_account.retrieve: %w", err)
		}

		// 5. Convert response to Starlark format
		return convertFacilityToStarlark(resp.GetFacility().GetAccountId(), resp.GetFacility()), nil
	}
}

// getBalanceHandler queries the current balance for an internal account via gRPC.
// This handler adapts Starlark parameters to the GetBalance RPC call,
// which delegates to Position Keeping service for the balance computation.
//
// Parameters:
//   - account_id (string): The account identifier to query balance for
//
// Returns a map containing:
//   - account_id: The account identifier
//   - instrument_code: The instrument code (e.g., "USD", "KWH")
//   - quantity: The balance amount as a decimal string
//   - as_of: The timestamp when the balance was calculated
func getBalanceHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// 1. Parse Starlark params
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}

		// 2. Prepare client context with saga metadata propagation
		clientCtx := prepareClientContext(ctx)

		// 3. Build the request
		req := &internalaccountv1.GetBalanceRequest{
			AccountId: accountID,
		}

		// 4. Call gRPC client
		resp, err := client.GetBalance(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("internal_account.get_balance: %w", err)
		}

		// 5. Convert response to Starlark format
		balance := resp.GetCurrentBalance()
		return map[string]any{
			"account_id":      resp.GetAccountId(),
			"instrument_code": balance.GetInstrumentCode(),
			"amount":          balance.GetAmount(),
			"as_of":           resp.GetAsOf().AsTime(),
		}, nil
	}
}

// initiateHandler creates a new internal account via gRPC.
// This handler adapts Starlark parameters to the InitiateInternalAccount RPC call,
// propagating saga metadata for idempotency, tracing, and bi-temporal queries.
//
// Parameters:
//   - account_code (string): The business-friendly account code (required)
//   - name (string): The human-readable account name (required)
//   - product_type_code (string): The product type code from the Product Directory (required)
//   - instrument_code (string): The instrument code (e.g., "USD", "KWH") (required)
//   - description (string): Additional context about the account's purpose (optional)
//   - counterparty_id (string): Counterparty ID for NOSTRO/VOSTRO (optional)
//   - counterparty_name (string): Counterparty name for NOSTRO/VOSTRO (optional)
//   - counterparty_external_ref (string): External account reference at counterparty (optional)
//
// Returns a map containing:
//   - account_id: The generated unique identifier
//   - account_code: The business-friendly account code
//   - name: The human-readable account name
//   - behavior_class: The behavior class (e.g., "NOSTRO", "VOSTRO", "CLEARING")
//   - status: Always "ACTIVE" for newly created accounts
//   - instrument_code: The instrument code
func initiateHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		// 1. Parse and validate required params
		req, productTypeCode, err := parseInitiateParams(params)
		if err != nil {
			return nil, err
		}

		// 2. Add counterparty details if needed (for NOSTRO/VOSTRO behavior classes)
		if err := addCounterpartyDetails(req, params, productTypeCode); err != nil {
			return nil, err
		}

		// 3. Prepare client context with saga metadata propagation
		clientCtx := prepareClientContext(ctx)

		// 4. Call gRPC client
		resp, err := client.InitiateInternalAccount(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("internal_account.initiate: %w", err)
		}

		// 5. Convert response to Starlark format
		return convertFacilityToStarlark(resp.GetAccountId(), resp.GetFacility()), nil
	}
}

// parseInitiateParams extracts and validates required parameters for account initiation.
func parseInitiateParams(params map[string]any) (*internalaccountv1.InitiateInternalAccountRequest, string, error) {
	accountCode, err := saga.RequireStringParam(params, "account_code")
	if err != nil {
		return nil, "", err
	}

	name, err := saga.RequireStringParam(params, "name")
	if err != nil {
		return nil, "", err
	}

	productTypeCode, err := saga.RequireStringParam(params, "product_type_code")
	if err != nil {
		return nil, "", err
	}

	instrumentCode, err := saga.RequireStringParam(params, "instrument_code")
	if err != nil {
		return nil, "", err
	}

	// Parse optional description
	description := getOptionalString(params, "description")

	req := &internalaccountv1.InitiateInternalAccountRequest{
		AccountCode:     accountCode,
		Name:            name,
		ProductTypeCode: productTypeCode,
		InstrumentCode:  instrumentCode,
		Description:     description,
	}

	return req, productTypeCode, nil
}

// addCounterpartyDetails adds counterparty details to the request if needed.
// The counterparty_id parameter triggers addition of counterparty details.
// Counterparty type is determined from the product_type_code prefix (NOSTRO/VOSTRO).
func addCounterpartyDetails(req *internalaccountv1.InitiateInternalAccountRequest, params map[string]any, productTypeCode string) error {
	// Parse counterparty details if provided
	counterpartyID := getOptionalString(params, "counterparty_id")
	if counterpartyID == "" {
		return nil
	}

	// Determine counterparty type from product type code prefix
	counterpartyType := internalaccountv1.CounterpartyType_COUNTERPARTY_TYPE_NOSTRO
	if len(productTypeCode) >= 6 && productTypeCode[:6] == "VOSTRO" {
		counterpartyType = internalaccountv1.CounterpartyType_COUNTERPARTY_TYPE_VOSTRO
	}

	// Parse optional attributes map (e.g., swift_code, bic_code)
	attributes := map[string]string{}
	if raw, ok := params["counterparty_attributes"]; ok {
		rawMap, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: got %T", ErrCounterpartyAttributesNotMap, raw)
		}
		for k, v := range rawMap {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("%w: key %s got %T", ErrCounterpartyAttributeValueNotString, k, v)
			}
			attributes[k] = s
		}
	}

	req.CounterpartyDetails = &internalaccountv1.CounterpartyDetails{
		CounterpartyId:          counterpartyID,
		CounterpartyName:        getOptionalString(params, "counterparty_name"),
		CounterpartyExternalRef: getOptionalString(params, "counterparty_external_ref"),
		Attributes:              attributes,
		CounterpartyType:        counterpartyType,
	}

	return nil
}

// getOptionalString extracts an optional string parameter from params.
func getOptionalString(params map[string]any, key string) string {
	if val, ok := params[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// convertFacilityToStarlark converts a facility proto to Starlark map format.
func convertFacilityToStarlark(accountID string, facility *internalaccountv1.InternalAccountFacility) map[string]any {
	return map[string]any{
		"account_id":      accountID,
		"account_code":    facility.GetAccountCode(),
		"name":            facility.GetName(),
		"behavior_class":  facility.GetBehaviorClass(),
		"status":          convertAccountStatusToString(facility.GetAccountStatus()),
		"instrument_code": facility.GetInstrumentCode(),
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
type contextKey string

const correlationIDContextKey contextKey = "x-correlation-id"

func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context

	// Add correlation ID to context value so the client's PropagateCorrelationID can extract it
	clientCtx = context.WithValue(clientCtx, correlationIDContextKey, ctx.CorrelationID.String())

	// Propagate idempotency key and knowledge_at timestamp
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)

	// PropagateCorrelationID is called by the Client methods
	return clientCtx
}

const (
	// accountStatusUnspecified is the string representation for unspecified statuses.
	accountStatusUnspecified = "UNSPECIFIED"
)

// convertAccountStatusToString converts the proto enum to a human-readable string.
func convertAccountStatusToString(s internalaccountv1.InternalAccountStatus) string {
	switch s {
	case internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE:
		return "ACTIVE"
	case internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED:
		return "SUSPENDED"
	case internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED:
		return "CLOSED"
	case internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED:
		return accountStatusUnspecified
	default:
		return accountStatusUnspecified
	}
}

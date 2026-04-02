package applier

import (
	"context"
	"fmt"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// prepareCallContext enriches the gRPC call context with saga metadata:
// tenant ID (from PartyScope), idempotency key, knowledge_at, and correlation ID.
// This is required for loopback gRPC calls where the interceptor chain does not
// automatically inject tenant context.
func prepareCallContext(ctx *saga.StarlarkContext) context.Context {
	callCtx := ctx.Context

	// Propagate idempotency key
	callCtx = clients.PropagateIdempotencyKey(callCtx, ctx.IdempotencyKey)

	// Propagate tenant ID from PartyScope to gRPC metadata
	if ctx.PartyScope != nil && ctx.PartyScope.TenantID != "" {
		callCtx = metadata.AppendToOutgoingContext(callCtx, tenant.TenantIDKey, ctx.PartyScope.TenantID)
	}

	return callCtx
}

// ReferenceDataClient wraps the reference-data gRPC clients to implement
// ReferenceDataService for use as a saga handler dependency.
//
// The client translates between the flat map[string]any parameter convention used
// by saga handlers and the typed proto messages required by the gRPC services.
//
// It combines three proto services into one adapter:
//   - ReferenceDataService for instrument lifecycle
//   - AccountTypeRegistryService for account type lifecycle
//   - SagaRegistryService for saga definition registration
type ReferenceDataClient struct {
	instruments  referencedatav1.ReferenceDataServiceClient
	accountTypes referencedatav1.AccountTypeRegistryServiceClient
	sagas        sagav1.SagaRegistryServiceClient
}

// NewReferenceDataClient creates a new ReferenceDataClient from gRPC connections.
// refDataConn targets the reference-data service (instruments + account types).
// sagaConn targets the saga registry service (saga definitions).
// Both may be the same connection in the unified binary.
func NewReferenceDataClient(refDataConn, sagaConn *grpc.ClientConn) *ReferenceDataClient {
	return &ReferenceDataClient{
		instruments:  referencedatav1.NewReferenceDataServiceClient(refDataConn),
		accountTypes: referencedatav1.NewAccountTypeRegistryServiceClient(refDataConn),
		sagas:        sagav1.NewSagaRegistryServiceClient(sagaConn),
	}
}

// RegisterInstrument implements ReferenceDataService.
// Converts Starlark params to a RegisterInstrumentRequest and calls the gRPC service.
func (c *ReferenceDataClient) RegisterInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &referencedatav1.RegisterInstrumentRequest{}
	req.Code, _ = params["instrument_code"].(string)
	req.DisplayName, _ = params["display_name"].(string)
	req.Description, _ = params["description"].(string)

	if dimStr, ok := params["dimension"].(string); ok {
		req.Dimension = parseDimension(dimStr)
	}
	if dp, ok := toInt32(params["decimal_places"]); ok {
		req.Precision = dp
	}

	callCtx := prepareCallContext(ctx)
	resp, err := c.instruments.RegisterInstrument(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("register instrument: %w", err)
	}

	inst := resp.GetInstrument()
	return map[string]any{
		"instrument_code": inst.GetCode(),
		"version":         inst.GetVersion(),
		"status":          inst.GetStatus().String(),
	}, nil
}

// ActivateInstrument implements ReferenceDataService.
// Converts Starlark params to an ActivateInstrumentRequest and calls the gRPC service.
// Uses proactive idempotency: checks current state before attempting activation.
func (c *ReferenceDataClient) ActivateInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	code, _ := params["instrument_code"].(string)
	var version int32 = 1
	if v, ok := toInt32(params["version"]); ok {
		version = v
	}

	callCtx := prepareCallContext(ctx)

	// Proactive check: if already ACTIVE, return success immediately.
	existing, lookupErr := c.instruments.RetrieveInstrument(callCtx, &referencedatav1.RetrieveInstrumentRequest{
		Code:    code,
		Version: version,
	})
	if lookupErr == nil && existing.GetInstrument().GetStatus() == referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
		inst := existing.GetInstrument()
		return map[string]any{
			"instrument_code": inst.GetCode(),
			"version":         inst.GetVersion(),
			"status":          inst.GetStatus().String(),
		}, nil
	}

	// Proceed with activation.
	resp, err := c.instruments.ActivateInstrument(callCtx, &referencedatav1.ActivateInstrumentRequest{
		Code:    code,
		Version: version,
	})
	if err != nil {
		// Reactive fallback: if FailedPrecondition and instrument is ACTIVE, treat as success.
		if status.Code(err) == codes.FailedPrecondition {
			retryLookup, retryErr := c.instruments.RetrieveInstrument(callCtx, &referencedatav1.RetrieveInstrumentRequest{
				Code:    code,
				Version: version,
			})
			if retryErr == nil && retryLookup.GetInstrument().GetStatus() == referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
				inst := retryLookup.GetInstrument()
				return map[string]any{
					"instrument_code": inst.GetCode(),
					"version":         inst.GetVersion(),
					"status":          inst.GetStatus().String(),
				}, nil
			}
		}
		return nil, fmt.Errorf("activate instrument: %w", err)
	}

	inst := resp.GetInstrument()
	return map[string]any{
		"instrument_code": inst.GetCode(),
		"version":         inst.GetVersion(),
		"status":          inst.GetStatus().String(),
	}, nil
}

// DeleteInstrument implements ReferenceDataService.
// Maps to DeprecateInstrument in the proto (compensation handler).
func (c *ReferenceDataClient) DeleteInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &referencedatav1.DeprecateInstrumentRequest{}
	req.Code, _ = params["instrument_code"].(string)
	if v, ok := toInt32(params["version"]); ok {
		req.Version = v
	} else {
		req.Version = 1
	}

	callCtx := prepareCallContext(ctx)
	resp, err := c.instruments.DeprecateInstrument(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("delete instrument: %w", err)
	}

	inst := resp.GetInstrument()
	return map[string]any{
		"instrument_code": inst.GetCode(),
		"status":          inst.GetStatus().String(),
	}, nil
}

// RegisterAccountType implements ReferenceDataService.
// Creates a draft account type and immediately activates it.
// Uses proactive idempotency: checks if already ACTIVE before creating a draft.
func (c *ReferenceDataClient) RegisterAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	code, _ := params["code"].(string)
	callCtx := prepareCallContext(ctx)

	// Proactive check: if already ACTIVE, return success immediately.
	existing, lookupErr := c.accountTypes.GetActiveDefinition(callCtx, &referencedatav1.GetActiveDefinitionRequest{
		Code: code,
	})
	if lookupErr == nil && existing.GetDefinition().GetStatus() == referencedatav1.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE {
		return accountTypeResult(existing.GetDefinition()), nil
	}

	// Proceed with create + activate flow.
	draftResp, err := c.accountTypes.CreateDraft(callCtx, buildCreateDraftRequest(params))
	if err != nil {
		// Reactive fallback: if AlreadyExists (race condition), look up the active definition.
		if status.Code(err) == codes.AlreadyExists {
			return c.handleAccountTypeAlreadyExists(callCtx, code)
		}
		return nil, fmt.Errorf("create account type draft: %w", err)
	}

	// Activate the draft (or reactivate if DEPRECATED)
	activateResp, err := c.accountTypes.ActivateAccountType(callCtx, &referencedatav1.ActivateAccountTypeRequest{
		Id: draftResp.GetDefinition().GetId(),
	})
	if err != nil {
		// Reactive fallback: if FailedPrecondition and account type is ACTIVE, treat as success.
		if status.Code(err) == codes.FailedPrecondition {
			retryLookup, retryErr := c.accountTypes.GetAccountType(callCtx, &referencedatav1.GetAccountTypeRequest{
				Code: code,
			})
			if retryErr == nil && retryLookup.GetDefinition().GetStatus() == referencedatav1.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE {
				return accountTypeResult(retryLookup.GetDefinition()), nil
			}
		}
		return nil, fmt.Errorf("activate account type: %w", err)
	}
	return accountTypeResult(activateResp.GetDefinition()), nil
}

// buildCreateDraftRequest converts Starlark params to a CreateDraftRequest.
func buildCreateDraftRequest(params map[string]any) *referencedatav1.CreateDraftRequest {
	req := &referencedatav1.CreateDraftRequest{}
	req.Code, _ = params["code"].(string)
	req.DisplayName, _ = params["display_name"].(string)
	req.Description, _ = params["description"].(string)
	req.InstrumentCode, _ = params["instrument_code"].(string)
	req.DefaultSagaPrefix, _ = params["default_saga_prefix"].(string)
	req.ValidationCel, _ = params["validation_cel"].(string)
	req.EligibilityCel, _ = params["eligibility_cel"].(string)

	if bcStr, ok := params["behavior_class"].(string); ok {
		req.BehaviorClass = parseBehaviorClass(bcStr)
	}
	if nbStr, ok := params["normal_balance"].(string); ok {
		req.NormalBalance = parseNormalBalance(nbStr)
	}
	if id, ok := params["default_conversion_method_id"].(string); ok {
		req.DefaultConversionMethodId = id
	}
	if v, ok := toInt32(params["default_conversion_method_version"]); ok {
		req.DefaultConversionMethodVersion = v
	}
	return req
}

// accountTypeResult converts an AccountTypeDefinition to a saga result map.
func accountTypeResult(def *referencedatav1.AccountTypeDefinition) map[string]any {
	return map[string]any{
		"id":      def.GetId(),
		"code":    def.GetCode(),
		"version": def.GetVersion(),
		"status":  def.GetStatus().String(),
	}
}

// DeleteAccountType implements ReferenceDataService.
// Maps to DeprecateAccountType in the proto (compensation handler).
func (c *ReferenceDataClient) DeleteAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &referencedatav1.DeprecateAccountTypeRequest{}
	req.Id, _ = params["id"].(string)

	callCtx := prepareCallContext(ctx)
	resp, err := c.accountTypes.DeprecateAccountType(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("delete account type: %w", err)
	}

	def := resp.GetDefinition()
	return map[string]any{
		"code":   def.GetCode(),
		"status": def.GetStatus().String(),
	}, nil
}

// handleAccountTypeAlreadyExists resolves the existing active account type by code
// so that downstream saga steps receive the id. Returns an error if the lookup fails.
func (c *ReferenceDataClient) handleAccountTypeAlreadyExists(ctx context.Context, code string) (any, error) {
	resp, err := c.accountTypes.GetActiveDefinition(ctx, &referencedatav1.GetActiveDefinitionRequest{
		Code: code,
	})
	if err != nil {
		return nil, fmt.Errorf("create account type draft: account type already exists but lookup failed: %w", err)
	}
	return accountTypeResult(resp.GetDefinition()), nil
}

// RegisterValuationRule implements ReferenceDataService.
// Valuation rules are recorded as part of the manifest version store.
// This handler acknowledges the rule without a separate gRPC backend.
func (c *ReferenceDataClient) RegisterValuationRule(_ *saga.StarlarkContext, params map[string]any) (any, error) {
	fromInstrument, _ := params["from_instrument"].(string)
	toInstrument, _ := params["to_instrument"].(string)
	return map[string]any{
		"from_instrument": fromInstrument,
		"to_instrument":   toInstrument,
		"status":          "REGISTERED",
	}, nil
}

// RegisterSagaDefinition implements ReferenceDataService.
// Creates a saga draft and activates it via the SagaRegistryService.
// Uses proactive idempotency: checks if already ACTIVE before creating a draft.
func (c *ReferenceDataClient) RegisterSagaDefinition(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	sagaName, _ := params["saga_name"].(string)

	callCtx := prepareCallContext(ctx)

	// Proactive check: if already ACTIVE, return success immediately.
	existing, lookupErr := c.sagas.GetActiveSaga(callCtx, &sagav1.GetActiveSagaRequest{
		Name: sagaName,
	})
	if lookupErr == nil {
		s := existing.GetSaga()
		if s.GetStatus() == sagav1.SagaStatus_SAGA_STATUS_ACTIVE {
			return map[string]any{
				"saga_name": s.GetName(),
				"saga_id":   s.GetId(),
				"status":    s.GetStatus().String(),
			}, nil
		}
	}

	// Proceed with create + activate flow.
	draftReq := &sagav1.CreateSagaDraftRequest{}
	draftReq.Name = sagaName
	draftReq.DisplayName, _ = params["display_name"].(string)
	draftReq.Description, _ = params["description"].(string)
	draftReq.Script, _ = params["script"].(string)

	draftResp, err := c.sagas.CreateSagaDraft(callCtx, draftReq)
	if err != nil {
		// Reactive fallback: treat AlreadyExists as success (belt and suspenders).
		if status.Code(err) == codes.AlreadyExists {
			return c.handleSagaAlreadyExists(callCtx, sagaName)
		}
		return nil, fmt.Errorf("create saga draft: %w", err)
	}

	sagaDef := draftResp.GetSaga()

	// Activate the saga
	activateResp, err := c.sagas.ActivateSaga(callCtx, &sagav1.ActivateSagaRequest{
		Id: sagaDef.GetId(),
	})
	if err != nil {
		return nil, fmt.Errorf("activate saga: %w", err)
	}

	activeSaga := activateResp.GetSaga()
	return map[string]any{
		"saga_name": activeSaga.GetName(),
		"saga_id":   activeSaga.GetId(),
		"status":    activeSaga.GetStatus().String(),
	}, nil
}

// handleSagaAlreadyExists resolves the existing active saga by name so that
// downstream saga steps receive the saga_id. Returns an error if the lookup fails.
func (c *ReferenceDataClient) handleSagaAlreadyExists(ctx context.Context, name string) (any, error) {
	resp, err := c.sagas.GetActiveSaga(ctx, &sagav1.GetActiveSagaRequest{
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("create saga draft: saga already exists but lookup failed: %w", err)
	}
	s := resp.GetSaga()
	return map[string]any{
		"saga_name": s.GetName(),
		"saga_id":   s.GetId(),
		"status":    s.GetStatus().String(),
	}, nil
}

// parseDimension converts a string dimension name to the proto enum value.
func parseDimension(s string) referencedatav1.Dimension {
	if v, ok := referencedatav1.Dimension_value[s]; ok {
		return referencedatav1.Dimension(v)
	}
	// Try with DIMENSION_ prefix
	if v, ok := referencedatav1.Dimension_value["DIMENSION_"+s]; ok {
		return referencedatav1.Dimension(v)
	}
	return referencedatav1.Dimension_DIMENSION_UNSPECIFIED
}

// parseBehaviorClass converts a string to the proto BehaviorClass enum value.
func parseBehaviorClass(s string) referencedatav1.BehaviorClass {
	if v, ok := referencedatav1.BehaviorClass_value[s]; ok {
		return referencedatav1.BehaviorClass(v)
	}
	if v, ok := referencedatav1.BehaviorClass_value["BEHAVIOR_CLASS_"+s]; ok {
		return referencedatav1.BehaviorClass(v)
	}
	return referencedatav1.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED
}

// parseNormalBalance converts a string to the proto NormalBalance enum value.
func parseNormalBalance(s string) referencedatav1.NormalBalance {
	if v, ok := referencedatav1.NormalBalance_value[s]; ok {
		return referencedatav1.NormalBalance(v)
	}
	if v, ok := referencedatav1.NormalBalance_value["NORMAL_BALANCE_"+s]; ok {
		return referencedatav1.NormalBalance(v)
	}
	return referencedatav1.NormalBalance_NORMAL_BALANCE_UNSPECIFIED
}

// Ensure ReferenceDataClient implements ReferenceDataService at compile time.
var _ ReferenceDataService = (*ReferenceDataClient)(nil)

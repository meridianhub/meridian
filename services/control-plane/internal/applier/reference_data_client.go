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
	"google.golang.org/grpc/metadata"
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
func (c *ReferenceDataClient) ActivateInstrument(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &referencedatav1.ActivateInstrumentRequest{}
	req.Code, _ = params["instrument_code"].(string)
	if v, ok := toInt32(params["version"]); ok {
		req.Version = v
	}

	callCtx := prepareCallContext(ctx)
	resp, err := c.instruments.ActivateInstrument(callCtx, req)
	if err != nil {
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
// Creates a draft account type and immediately activates it (idempotent).
func (c *ReferenceDataClient) RegisterAccountType(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	draftReq := &referencedatav1.CreateDraftRequest{}
	draftReq.Code, _ = params["code"].(string)
	draftReq.DisplayName, _ = params["display_name"].(string)
	draftReq.Description, _ = params["description"].(string)
	draftReq.InstrumentCode, _ = params["instrument_code"].(string)
	draftReq.DefaultSagaPrefix, _ = params["default_saga_prefix"].(string)
	draftReq.ValidationCel, _ = params["validation_cel"].(string)
	draftReq.EligibilityCel, _ = params["eligibility_cel"].(string)

	if bcStr, ok := params["behavior_class"].(string); ok {
		draftReq.BehaviorClass = parseBehaviorClass(bcStr)
	}
	if nbStr, ok := params["normal_balance"].(string); ok {
		draftReq.NormalBalance = parseNormalBalance(nbStr)
	}

	// Resolved conversion method (UUID + version from ValuationMethodService)
	if id, ok := params["default_conversion_method_id"].(string); ok {
		draftReq.DefaultConversionMethodId = id
	}
	if v, ok := toInt32(params["default_conversion_method_version"]); ok {
		draftReq.DefaultConversionMethodVersion = v
	}

	callCtx := prepareCallContext(ctx)
	draftResp, err := c.accountTypes.CreateDraft(callCtx, draftReq)
	if err != nil {
		return nil, fmt.Errorf("create account type draft: %w", err)
	}

	def := draftResp.GetDefinition()

	// Activate the draft
	activateReq := &referencedatav1.ActivateAccountTypeRequest{
		Id: def.GetId(),
	}
	activateResp, err := c.accountTypes.ActivateAccountType(callCtx, activateReq)
	if err != nil {
		return nil, fmt.Errorf("activate account type: %w", err)
	}

	activeDef := activateResp.GetDefinition()
	return map[string]any{
		"code":    activeDef.GetCode(),
		"version": activeDef.GetVersion(),
		"status":  activeDef.GetStatus().String(),
	}, nil
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
func (c *ReferenceDataClient) RegisterSagaDefinition(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	draftReq := &sagav1.CreateSagaDraftRequest{}
	draftReq.Name, _ = params["saga_name"].(string)
	draftReq.DisplayName, _ = params["display_name"].(string)
	draftReq.Description, _ = params["description"].(string)
	draftReq.Script, _ = params["script"].(string)

	callCtx := prepareCallContext(ctx)
	draftResp, err := c.sagas.CreateSagaDraft(callCtx, draftReq)
	if err != nil {
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

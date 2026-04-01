package applier

import (
	"context"
	"errors"
	"fmt"
	"time"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrUnknownDataCategory is returned when an unrecognized data category string is provided.
var ErrUnknownDataCategory = errors.New("unknown data category")

// MarketInformationClient wraps the market-information gRPC client to implement
// MarketInformationService for use as a saga handler dependency.
//
// The client translates between the flat map[string]any parameter convention used
// by saga handlers and the typed proto messages required by the gRPC service.
type MarketInformationClient struct {
	client marketinformationv1.MarketInformationServiceClient
}

// NewMarketInformationClient creates a new MarketInformationClient from a gRPC connection.
func NewMarketInformationClient(conn *grpc.ClientConn) *MarketInformationClient {
	return &MarketInformationClient{
		client: marketinformationv1.NewMarketInformationServiceClient(conn),
	}
}

// RegisterDataSource implements MarketInformationService.
// Converts Starlark params to a RegisterDataSourceRequest and calls the gRPC service.
// Uses proactive idempotency: checks if data source already exists before creating.
func (c *MarketInformationClient) RegisterDataSource(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &marketinformationv1.RegisterDataSourceRequest{}
	req.Code, _ = params["code"].(string)
	req.Name, _ = params["name"].(string)
	req.Description, _ = params["description"].(string)
	if tl, ok := toInt32(params["trust_level"]); ok {
		req.TrustLevel = tl
	}

	callCtx := prepareCallContext(ctx)

	// Proactive check: if a data source with this code already exists, return success immediately.
	existing, lookupErr := c.findDataSourceByCode(callCtx, req.Code)
	if lookupErr == nil && existing != nil {
		return map[string]any{
			"source_id": existing.GetId(),
			"code":      existing.GetCode(),
			"status":    "REGISTERED",
		}, nil
	}

	// Proceed with registration.
	resp, err := c.client.RegisterDataSource(callCtx, req)
	if err != nil {
		// Reactive fallback: treat AlreadyExists as success for manifest re-apply scenarios.
		if status.Code(err) == codes.AlreadyExists {
			fallback, fallbackErr := c.findDataSourceByCode(callCtx, req.Code)
			if fallbackErr == nil && fallback != nil {
				return map[string]any{
					"source_id": fallback.GetId(),
					"code":      fallback.GetCode(),
					"status":    "REGISTERED",
				}, nil
			}
			return map[string]any{
				"code":   req.Code,
				"status": "REGISTERED",
			}, nil
		}
		return nil, fmt.Errorf("register data source: %w", err)
	}

	src := resp.GetSource()
	return map[string]any{
		"source_id": src.GetId(),
		"code":      src.GetCode(),
		"status":    "REGISTERED",
	}, nil
}

// findDataSourceByCode pages through ListDataSources to locate a data source by code.
// Returns nil, nil if not found. Returns nil, error on RPC failure.
func (c *MarketInformationClient) findDataSourceByCode(ctx context.Context, code string) (*marketinformationv1.DataSource, error) {
	pageToken := ""
	for {
		resp, err := c.client.ListDataSources(ctx, &marketinformationv1.ListDataSourcesRequest{
			PageSize:  100,
			PageToken: pageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list data sources for lookup: %w", err)
		}
		for _, src := range resp.GetSources() {
			if src.GetCode() == code {
				return src, nil
			}
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return nil, nil
		}
	}
}

// RegisterDataSet implements MarketInformationService.
// Converts Starlark params to a RegisterDataSetRequest and calls the gRPC service.
// The created data set is in DRAFT status and must be activated separately.
// Uses proactive idempotency: checks if data set already exists before creating.
func (c *MarketInformationClient) RegisterDataSet(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &marketinformationv1.RegisterDataSetRequest{}
	req.Code, _ = params["code"].(string)
	req.Unit, _ = params["unit"].(string)
	req.DisplayName, _ = params["display_name"].(string)
	req.Description, _ = params["description"].(string)
	req.ResolutionKeyExpression, _ = params["resolution_key_expression"].(string)
	req.ValidationExpression, _ = params["validation_expression"].(string)
	req.ErrorMessageExpression, _ = params["error_message_expression"].(string)

	categoryStr, _ := params["category"].(string)
	category, err := parseDataCategory(categoryStr)
	if err != nil {
		return nil, fmt.Errorf("register data set: %w", err)
	}
	req.Category = category

	if effectiveFromStr, ok := params["effective_from"].(string); ok && effectiveFromStr != "" {
		t, parseErr := time.Parse(time.RFC3339, effectiveFromStr)
		if parseErr != nil {
			return nil, fmt.Errorf("register data set: invalid effective_from %q: %w", effectiveFromStr, parseErr)
		}
		req.EffectiveFrom = timestamppb.New(t)
	}

	callCtx := prepareCallContext(ctx)

	// Proactive check: if data set already exists (DRAFT or ACTIVE), return success immediately.
	existing, lookupErr := c.retrieveDataSet(callCtx, req.Code)
	if lookupErr == nil && existing != nil {
		return dataSetResult(existing), nil
	}

	// Proceed with registration.
	resp, err := c.client.RegisterDataSet(callCtx, req)
	if err != nil {
		// Reactive fallback: treat AlreadyExists as success for manifest re-apply scenarios.
		if status.Code(err) == codes.AlreadyExists {
			return c.handleDataSetAlreadyExists(callCtx, req.Code)
		}
		return nil, fmt.Errorf("register data set: %w", err)
	}

	ds := resp.GetDataset()
	return dataSetResult(ds), nil
}

// ActivateDataSet implements MarketInformationService.
// Converts Starlark params to an ActivateDataSetRequest and calls the gRPC service.
// Uses proactive idempotency: checks if already ACTIVE before attempting activation.
func (c *MarketInformationClient) ActivateDataSet(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &marketinformationv1.ActivateDataSetRequest{}
	req.Code, _ = params["code"].(string)
	if v, ok := toInt32(params["version"]); ok {
		req.Version = v
	}

	callCtx := prepareCallContext(ctx)

	// Proactive check: if already ACTIVE, return success immediately.
	existing, lookupErr := c.retrieveDataSet(callCtx, req.Code)
	if lookupErr == nil && existing.GetStatus() == marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE {
		return dataSetResult(existing), nil
	}

	// Proceed with activation.
	resp, err := c.client.ActivateDataSet(callCtx, req)
	if err != nil {
		// Reactive fallback: on FailedPrecondition, check if the data set is already ACTIVE.
		if status.Code(err) == codes.FailedPrecondition {
			retryLookup, retryErr := c.retrieveDataSet(callCtx, req.Code)
			if retryErr == nil && retryLookup.GetStatus() == marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE {
				return dataSetResult(retryLookup), nil
			}
		}
		return nil, fmt.Errorf("activate data set: %w", err)
	}

	ds := resp.GetDataset()
	return dataSetResult(ds), nil
}

// dataSetResult converts a DataSetDefinition to a saga result map.
func dataSetResult(ds *marketinformationv1.DataSetDefinition) map[string]any {
	return map[string]any{
		"dataset_id": ds.GetId(),
		"code":       ds.GetCode(),
		"version":    ds.GetVersion(),
		"status":     ds.GetStatus().String(),
	}
}

// handleDataSetAlreadyExists retrieves an existing data set by code so that
// downstream saga steps receive dataset_id. Returns an error if the lookup fails.
func (c *MarketInformationClient) handleDataSetAlreadyExists(ctx context.Context, code string) (any, error) {
	ds, err := c.retrieveDataSet(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("register data set: dataset already exists but lookup failed: %w", err)
	}
	return dataSetResult(ds), nil
}

// retrieveDataSet looks up a data set by code. Returns the proto definition or an error.
func (c *MarketInformationClient) retrieveDataSet(ctx context.Context, code string) (*marketinformationv1.DataSetDefinition, error) {
	resp, err := c.client.RetrieveDataSet(ctx, &marketinformationv1.RetrieveDataSetRequest{
		Code: code,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetDataset(), nil
}

// parseDataCategory converts a string category name to the proto enum value.
// Accepts both prefixed ("DATA_CATEGORY_ENERGY_PRICE") and stripped ("ENERGY_PRICE") forms,
// since the Starlark handler schema uses stripped names while proto uses prefixed names.
// Returns an error for non-empty strings that do not match a known category.
func parseDataCategory(s string) (marketinformationv1.DataCategory, error) {
	if s == "" {
		return marketinformationv1.DataCategory_DATA_CATEGORY_UNSPECIFIED, nil
	}
	// Try the value as-is first (handles both prefixed and stripped forms).
	if v, ok := marketinformationv1.DataCategory_value[s]; ok {
		return marketinformationv1.DataCategory(v), nil
	}
	// Try with the DATA_CATEGORY_ prefix added (handles stripped form like "ENERGY_PRICE").
	if v, ok := marketinformationv1.DataCategory_value["DATA_CATEGORY_"+s]; ok {
		return marketinformationv1.DataCategory(v), nil
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownDataCategory, s)
}

// Ensure MarketInformationClient implements MarketInformationService at compile time.
var _ MarketInformationService = (*MarketInformationClient)(nil)

package applier

import (
	"errors"
	"fmt"
	"time"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/grpc"
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
func (c *MarketInformationClient) RegisterDataSource(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &marketinformationv1.RegisterDataSourceRequest{}
	req.Code, _ = params["code"].(string)
	req.Name, _ = params["name"].(string)
	req.Description, _ = params["description"].(string)
	if tl, ok := toInt32(params["trust_level"]); ok {
		req.TrustLevel = tl
	}

	callCtx := prepareCallContext(ctx)
	resp, err := c.client.RegisterDataSource(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("register data source: %w", err)
	}

	src := resp.GetSource()
	return map[string]any{
		"source_id": src.GetId(),
		"code":      src.GetCode(),
		"status":    "REGISTERED",
	}, nil
}

// RegisterDataSet implements MarketInformationService.
// Converts Starlark params to a RegisterDataSetRequest and calls the gRPC service.
// The created data set is in DRAFT status and must be activated separately.
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
	resp, err := c.client.RegisterDataSet(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("register data set: %w", err)
	}

	ds := resp.GetDataset()
	return map[string]any{
		"dataset_id": ds.GetId(),
		"code":       ds.GetCode(),
		"version":    ds.GetVersion(),
		"status":     ds.GetStatus().String(),
	}, nil
}

// ActivateDataSet implements MarketInformationService.
// Converts Starlark params to an ActivateDataSetRequest and calls the gRPC service.
func (c *MarketInformationClient) ActivateDataSet(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req := &marketinformationv1.ActivateDataSetRequest{}
	req.Code, _ = params["code"].(string)
	if v, ok := toInt32(params["version"]); ok {
		req.Version = v
	}

	callCtx := prepareCallContext(ctx)
	resp, err := c.client.ActivateDataSet(callCtx, req)
	if err != nil {
		return nil, fmt.Errorf("activate data set: %w", err)
	}

	ds := resp.GetDataset()
	return map[string]any{
		"dataset_id": ds.GetId(),
		"code":       ds.GetCode(),
		"version":    ds.GetVersion(),
		"status":     ds.GetStatus().String(),
	}, nil
}

// parseDataCategory converts a string category name to the proto enum value.
// Returns an error for non-empty strings that do not match a known category.
func parseDataCategory(s string) (marketinformationv1.DataCategory, error) {
	if s == "" {
		return marketinformationv1.DataCategory_DATA_CATEGORY_UNSPECIFIED, nil
	}
	if v, ok := marketinformationv1.DataCategory_value[s]; ok {
		return marketinformationv1.DataCategory(v), nil
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownDataCategory, s)
}

// Ensure MarketInformationClient implements MarketInformationService at compile time.
var _ MarketInformationService = (*MarketInformationClient)(nil)

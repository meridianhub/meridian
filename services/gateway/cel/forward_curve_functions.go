// Package cel provides CEL extension functions for forward curve consumption
// in the gateway service. These functions allow CEL pricing rules to query
// forward curve observations from the Market Data Service through a tiered cache.
package cel

import (
	"context"
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/shopspring/decimal"

	gatewaycache "github.com/meridianhub/meridian/services/gateway/cache"
)

// ForwardCurveLib creates a CEL function library for forward curve lookups.
//
// Functions:
//   - forward_price(resolution_key string, timestamp) -> double:
//     Query single forward curve observation value.
//   - forward_metadata(resolution_key string, timestamp) -> map[string, string]:
//     Get observation metadata (unit, quality, dataset_code, source_id).
//   - avg_forward_price(resolution_key string, start timestamp, end timestamp) -> double:
//     Average price over a time range.
func ForwardCurveLib(ctx context.Context, cache *gatewaycache.ForwardCurveCache) cel.EnvOption {
	return cel.Lib(&forwardCurveLibrary{
		cache: cache,
		ctx:   ctx,
	})
}

type forwardCurveLibrary struct {
	cache *gatewaycache.ForwardCurveCache
	ctx   context.Context
}

func (*forwardCurveLibrary) LibraryName() string {
	return "meridian.ForwardCurve"
}

func (lib *forwardCurveLibrary) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("forward_price",
			cel.Overload("forward_price_string_timestamp",
				[]*cel.Type{cel.StringType, cel.TimestampType},
				cel.DoubleType,
				cel.BinaryBinding(lib.forwardPrice),
			),
		),
		cel.Function("forward_metadata",
			cel.Overload("forward_metadata_string_timestamp",
				[]*cel.Type{cel.StringType, cel.TimestampType},
				cel.MapType(cel.StringType, cel.StringType),
				cel.BinaryBinding(lib.forwardMetadata),
			),
		),
		cel.Function("avg_forward_price",
			cel.Overload("avg_forward_price_string_timestamp_timestamp",
				[]*cel.Type{cel.StringType, cel.TimestampType, cel.TimestampType},
				cel.DoubleType,
				cel.FunctionBinding(lib.avgForwardPrice),
			),
		),
	}
}

func (*forwardCurveLibrary) ProgramOptions() []cel.ProgramOption {
	return nil
}

// forwardPrice queries a single forward curve observation and returns its value.
func (lib *forwardCurveLibrary) forwardPrice(lhs ref.Val, rhs ref.Val) ref.Val {
	gatewaycache.RecordCELEvaluation("forward_price")

	resolutionKey, ok := lhs.Value().(string)
	if !ok {
		return types.NewErr("forward_price: expected string resolution_key, got %T", lhs.Value())
	}

	ts, ok := rhs.Value().(time.Time)
	if !ok {
		return types.NewErr("forward_price: expected timestamp, got %T", rhs.Value())
	}

	obs, err := lib.cache.Get(lib.ctx, resolutionKey, ts)
	if err != nil {
		return types.NewErr("forward_price: %v", err)
	}

	f, _ := obs.Value.Float64()
	return types.Double(f)
}

// forwardMetadata queries a forward curve observation and returns its metadata.
func (lib *forwardCurveLibrary) forwardMetadata(lhs ref.Val, rhs ref.Val) ref.Val {
	gatewaycache.RecordCELEvaluation("forward_metadata")

	resolutionKey, ok := lhs.Value().(string)
	if !ok {
		return types.NewErr("forward_metadata: expected string resolution_key, got %T", lhs.Value())
	}

	ts, ok := rhs.Value().(time.Time)
	if !ok {
		return types.NewErr("forward_metadata: expected timestamp, got %T", rhs.Value())
	}

	obs, err := lib.cache.Get(lib.ctx, resolutionKey, ts)
	if err != nil {
		return types.NewErr("forward_metadata: %v", err)
	}

	metadata := map[string]string{
		"unit":         obs.Unit,
		"quality":      obs.Quality,
		"dataset_code": obs.DataSetCode,
		"source_id":    obs.SourceID,
		"observed_at":  obs.ObservedAt.Format(time.RFC3339),
		"valid_from":   obs.ValidFrom.Format(time.RFC3339),
		"valid_to":     obs.ValidTo.Format(time.RFC3339),
	}

	// Merge any additional metadata from the observation
	for k, v := range obs.Metadata {
		metadata[k] = v
	}

	// Convert to CEL map type
	adapter := types.DefaultTypeAdapter
	return types.NewStringStringMap(adapter, metadata)
}

// avgForwardPrice computes the arithmetic mean of forward prices over a time range.
func (lib *forwardCurveLibrary) avgForwardPrice(args ...ref.Val) ref.Val {
	gatewaycache.RecordCELEvaluation("avg_forward_price")

	if len(args) != 3 {
		return types.NewErr("avg_forward_price: expected 3 arguments, got %d", len(args))
	}

	resolutionKey, ok := args[0].Value().(string)
	if !ok {
		return types.NewErr("avg_forward_price: expected string resolution_key, got %T", args[0].Value())
	}

	start, ok := args[1].Value().(time.Time)
	if !ok {
		return types.NewErr("avg_forward_price: expected start timestamp, got %T", args[1].Value())
	}

	end, ok := args[2].Value().(time.Time)
	if !ok {
		return types.NewErr("avg_forward_price: expected end timestamp, got %T", args[2].Value())
	}

	if !start.Before(end) {
		return types.NewErr("avg_forward_price: start must be before end")
	}

	observations, err := lib.cache.GetRange(lib.ctx, resolutionKey, start, end)
	if err != nil {
		return types.NewErr("avg_forward_price: %v", err)
	}

	if len(observations) == 0 {
		return types.NewErr("avg_forward_price: no observations found for %s between %s and %s",
			resolutionKey, start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	sum := decimal.Zero
	for _, obs := range observations {
		sum = sum.Add(obs.Value)
	}

	avg := sum.Div(decimal.NewFromInt(int64(len(observations))))
	f, _ := avg.Float64()
	return types.Double(f)
}

// NewForwardCurveEnv creates a CEL environment with forward curve functions
// and standard pricing variables.
func NewForwardCurveEnv(ctx context.Context, cache *gatewaycache.ForwardCurveCache) (*cel.Env, error) {
	env, err := cel.NewEnv(
		// Standard pricing variables
		cel.Variable("amount", cel.DoubleType),
		cel.Variable("quantity", cel.DoubleType),
		cel.Variable("unit", cel.StringType),
		cel.Variable("timestamp", cel.TimestampType),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),

		// Forward curve extension functions
		ForwardCurveLib(ctx, cache),
	)
	if err != nil {
		return nil, fmt.Errorf("create forward curve CEL environment: %w", err)
	}

	return env, nil
}

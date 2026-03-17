// Package starlark provides a sandboxed Starlark execution environment for
// forecasting strategies. It extends the saga runtime patterns with
// forecasting-specific context injection, builtin functions, and validation.
package starlark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	starlarklib "go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/sandbox"
)

// sandboxCfg is the unified sandbox configuration for forecasting scripts.
var sandboxCfg = sandbox.ForecasterConfig()

// Default execution constraints.
// These constants are retained for backward compatibility; canonical values live in sandbox.ForecasterConfig().
const (
	DefaultTimeout       = 10 * time.Second // Reduced from 30s; 2x saga's 5s to allow observation fetching overhead
	MaxScriptSize        = 64 * 1024        // 64KB, matching saga
	MaxStepsPerExecution = 1_000_000        // Matching saga
)

// Runner errors.
var (
	ErrMISClientRequired   = errors.New("market information service client is required")
	ErrRefDataRequired     = errors.New("reference data client is required")
	ErrScriptRequired      = errors.New("starlark script is required")
	ErrEntryPointMissing   = errors.New("script must define a compute_forecast(ctx) function")
	ErrInvalidReturnType   = errors.New("compute_forecast must return a list of forecast points")
	ErrTimestampOutOfRange = errors.New("forecast point timestamp is outside the horizon")
	ErrNonMonotonic        = errors.New("forecast point timestamps must be monotonically increasing")
	ErrGranularityMismatch = errors.New("forecast point timestamp is not aligned to granularity")
	ErrScriptTooLarge      = errors.New("script exceeds maximum size")
	ErrValidation          = errors.New("script validation error")
	ErrInvalidInput        = errors.New("invalid strategy input")
)

// MISClient abstracts the Market Information Service for observation queries.
type MISClient interface {
	// FetchObservations retrieves historical observations for a dataset code.
	// Returns observations ordered by valid_from DESC.
	FetchObservations(ctx context.Context, datasetCode string, before time.Time) ([]Observation, error)
}

// RefDataClient abstracts the reference data service for node lookups.
type RefDataClient interface {
	// GetNodeByResolutionKey retrieves a reference data node by its resolution key.
	GetNodeByResolutionKey(ctx context.Context, tenantID, resolutionKey string) (*ReferenceData, error)
}

// ForecastContext holds the data and parameters injected into the Starlark
// script as the ctx argument to compute_forecast(ctx).
type ForecastContext struct {
	// Observations maps dataset code to a slice of observation records.
	Observations map[string][]Observation

	// ReferenceData holds the resolved reference data node (if resolution_key set).
	ReferenceData *ReferenceData

	// Horizon is the forecast window duration.
	Horizon time.Duration

	// Granularity is the spacing between forecast points.
	Granularity time.Duration

	// Now is the execution timestamp (start of the forecast window).
	Now time.Time
}

// Observation represents a single market data observation available to scripts.
type Observation struct {
	Timestamp time.Time
	Value     decimal.Decimal
	Quality   string
}

// ReferenceData holds reference data node information for scripts.
type ReferenceData struct {
	NodeType      string
	ResolutionKey string
	Attributes    map[string]any
}

// ForecastPoint is a single point in the forecast output.
type ForecastPoint struct {
	Timestamp time.Time
	Value     decimal.Decimal
	Metadata  map[string]string
}

// StrategyInput holds the parameters needed to execute a forecasting strategy.
type StrategyInput struct {
	// Script is the Starlark source code.
	Script string

	// InputDatasetCodes are the MDS dataset codes to fetch observations from.
	InputDatasetCodes []string

	// OutputDatasetCode is the target dataset for the forecast.
	OutputDatasetCode string

	// ResolutionKey is the optional reference data resolution key.
	ResolutionKey string

	// TenantID is the tenant scope for reference data lookups.
	TenantID string

	// HorizonHours defines how far into the future to forecast.
	HorizonHours int

	// GranularityHours defines the spacing between forecast points.
	GranularityHours int

	// Now overrides the current time (useful for testing). If zero, uses time.Now().
	Now time.Time
}

// ForecastRunner executes forecasting strategies written in Starlark.
type ForecastRunner struct {
	misClient MISClient
	refData   RefDataClient
	timeout   time.Duration
	logger    *slog.Logger
}

// ForecastRunnerConfig holds configuration for creating a ForecastRunner.
type ForecastRunnerConfig struct {
	MISClient MISClient
	RefData   RefDataClient
	Timeout   time.Duration
	Logger    *slog.Logger
}

// NewForecastRunner creates a new ForecastRunner.
func NewForecastRunner(cfg ForecastRunnerConfig) (*ForecastRunner, error) {
	if cfg.MISClient == nil {
		return nil, ErrMISClientRequired
	}
	if cfg.RefData == nil {
		return nil, ErrRefDataRequired
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &ForecastRunner{
		misClient: cfg.MISClient,
		refData:   cfg.RefData,
		timeout:   timeout,
		logger:    logger,
	}, nil
}

// ExecuteStrategy runs a forecasting strategy and returns the forecast points.
func (r *ForecastRunner) ExecuteStrategy(ctx context.Context, input StrategyInput) ([]ForecastPoint, error) {
	if input.Script == "" {
		return nil, ErrScriptRequired
	}
	if err := sandbox.ValidateScript(input.Script, sandboxCfg); err != nil {
		return nil, fmt.Errorf("%w: size %d exceeds maximum %d bytes", ErrScriptTooLarge, len(input.Script), sandboxCfg.MaxScriptSize)
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	if input.HorizonHours <= 0 {
		return nil, fmt.Errorf("%w: horizon_hours must be > 0", ErrInvalidInput)
	}
	if input.GranularityHours <= 0 {
		return nil, fmt.Errorf("%w: granularity_hours must be > 0", ErrInvalidInput)
	}

	horizon := time.Duration(input.HorizonHours) * time.Hour
	granularity := time.Duration(input.GranularityHours) * time.Hour

	r.logger.Info("executing forecast strategy",
		"input_datasets", input.InputDatasetCodes,
		"output_dataset", input.OutputDatasetCode,
		"horizon_hours", input.HorizonHours,
		"granularity_hours", input.GranularityHours,
	)

	// Step 1: Fetch historical observations from MDS
	observations, err := r.fetchObservations(ctx, input.InputDatasetCodes, now)
	if err != nil {
		return nil, fmt.Errorf("fetch observations: %w", err)
	}

	// Step 2: Fetch reference data node if resolution key specified
	var refData *ReferenceData
	if (input.ResolutionKey == "") != (input.TenantID == "") {
		return nil, fmt.Errorf("%w: resolution_key and tenant_id must both be set or both be empty", ErrInvalidInput)
	}
	if input.ResolutionKey != "" && input.TenantID != "" {
		refData, err = r.refData.GetNodeByResolutionKey(ctx, input.TenantID, input.ResolutionKey)
		if err != nil {
			return nil, fmt.Errorf("fetch reference data: %w", err)
		}
	}

	// Step 3: Build ForecastContext
	forecastCtx := &ForecastContext{
		Observations:  observations,
		ReferenceData: refData,
		Horizon:       horizon,
		Granularity:   granularity,
		Now:           now,
	}

	// Step 4-5: Execute Starlark script
	points, err := r.executeScript(ctx, input.Script, forecastCtx)
	if err != nil {
		return nil, err
	}

	// Step 6: Validate returned forecast points
	if err := validateForecastPoints(points, now, horizon, granularity); err != nil {
		return nil, err
	}

	r.logger.Info("forecast strategy execution completed",
		"point_count", len(points),
	)

	return points, nil
}

// fetchObservations retrieves historical observations from MDS for all input datasets.
func (r *ForecastRunner) fetchObservations(ctx context.Context, datasetCodes []string, now time.Time) (map[string][]Observation, error) {
	result := make(map[string][]Observation, len(datasetCodes))

	for _, code := range datasetCodes {
		obs, err := r.misClient.FetchObservations(ctx, code, now)
		if err != nil {
			return nil, fmt.Errorf("fetch observations for %s: %w", code, err)
		}
		result[code] = obs
	}

	return result, nil
}

// executeScript runs the Starlark script in a sandboxed environment.
func (r *ForecastRunner) executeScript(ctx context.Context, script string, forecastCtx *ForecastContext) ([]ForecastPoint, error) {
	// Apply timeout
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// Build Starlark context value
	ctxValue := forecastContextToStarlark(forecastCtx)

	// Build predeclared environment with forecasting builtins
	predeclared := newForecastBuiltins(r.logger)
	predeclared["Decimal"] = saga.DecimalBuiltin()

	// Create thread with cancellation support
	thread := &starlarklib.Thread{
		Name: "forecast",
		Print: func(_ *starlarklib.Thread, msg string) {
			r.logger.Info("forecast script print", "message", msg)
		},
	}
	thread.SetLocal("ctx", ctx)
	sandbox.HardenThread(thread, sandboxCfg)

	// Execute script in a goroutine for timeout support
	done := make(chan struct{})
	var execErr error
	var globals starlarklib.StringDict

	go func() {
		defer close(done)
		var err error
		globals, err = starlarklib.ExecFileOptions(&syntax.FileOptions{}, thread, "forecast.star", script, predeclared)
		if err != nil {
			execErr = err
		}
	}()

	select {
	case <-done:
		if execErr != nil {
			return nil, wrapStarlarkError(execErr)
		}
	case <-ctx.Done():
		thread.Cancel("execution cancelled")
		<-done
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: exceeded %v", saga.ErrTimeout, r.timeout)
		}
		return nil, fmt.Errorf("%w: %w", saga.ErrCancelled, ctx.Err())
	}

	// Look up compute_forecast function
	computeFn, ok := globals["compute_forecast"]
	if !ok {
		return nil, ErrEntryPointMissing
	}

	fn, ok := computeFn.(*starlarklib.Function)
	if !ok {
		return nil, ErrEntryPointMissing
	}

	// Call compute_forecast(ctx)
	callDone := make(chan struct{})
	var callErr error
	var resultVal starlarklib.Value

	go func() {
		defer close(callDone)
		var err error
		resultVal, err = starlarklib.Call(thread, fn, starlarklib.Tuple{ctxValue}, nil)
		if err != nil {
			callErr = err
		}
	}()

	select {
	case <-callDone:
		if callErr != nil {
			return nil, wrapStarlarkError(callErr)
		}
	case <-ctx.Done():
		thread.Cancel("execution cancelled")
		<-callDone
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: exceeded %v", saga.ErrTimeout, r.timeout)
		}
		return nil, fmt.Errorf("%w: %w", saga.ErrCancelled, ctx.Err())
	}

	// Convert Starlark result to []ForecastPoint
	return starlarkToForecastPoints(resultVal)
}

// validateForecastPoints checks that forecast points are within horizon, monotonic,
// and aligned to granularity.
func validateForecastPoints(points []ForecastPoint, now time.Time, horizon, granularity time.Duration) error {
	horizonEnd := now.Add(horizon)

	for i, p := range points {
		// Check timestamp is within [now, now+horizon]
		if p.Timestamp.Before(now) || p.Timestamp.After(horizonEnd) {
			return fmt.Errorf("%w: point %d at %v is outside [%v, %v]",
				ErrTimestampOutOfRange, i, p.Timestamp, now, horizonEnd)
		}

		// Check monotonically increasing
		if i > 0 && !p.Timestamp.After(points[i-1].Timestamp) {
			return fmt.Errorf("%w: point %d at %v is not after point %d at %v",
				ErrNonMonotonic, i, p.Timestamp, i-1, points[i-1].Timestamp)
		}

		// Check granularity alignment
		offset := p.Timestamp.Sub(now)
		if granularity > 0 && offset%granularity != 0 {
			return fmt.Errorf("%w: point %d at %v (offset %v) is not aligned to %v granularity",
				ErrGranularityMismatch, i, p.Timestamp, offset, granularity)
		}
	}

	return nil
}

// starlarkToForecastPoints converts the Starlark return value to []ForecastPoint.
func starlarkToForecastPoints(val starlarklib.Value) ([]ForecastPoint, error) {
	list, ok := val.(*starlarklib.List)
	if !ok {
		return nil, fmt.Errorf("%w: got %s, want list", ErrInvalidReturnType, val.Type())
	}

	points := make([]ForecastPoint, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		item := list.Index(i)
		dict, ok := item.(*starlarklib.Dict)
		if !ok {
			return nil, fmt.Errorf("%w: element %d is %s, want dict", ErrInvalidReturnType, i, item.Type())
		}

		point, err := dictToForecastPoint(dict)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		points = append(points, point)
	}

	return points, nil
}

// dictToForecastPoint converts a Starlark dict to a ForecastPoint.
func dictToForecastPoint(dict *starlarklib.Dict) (ForecastPoint, error) {
	var point ForecastPoint

	ts, err := extractPointTimestamp(dict)
	if err != nil {
		return point, err
	}
	point.Timestamp = ts

	val, err := extractPointValue(dict)
	if err != nil {
		return point, err
	}
	point.Value = val

	meta, err := extractPointMetadata(dict)
	if err != nil {
		return point, err
	}
	point.Metadata = meta

	return point, nil
}

// extractPointTimestamp extracts and parses the timestamp from a forecast point dict.
func extractPointTimestamp(dict *starlarklib.Dict) (time.Time, error) {
	tsVal, found, err := dict.Get(starlarklib.String("timestamp"))
	if err != nil {
		return time.Time{}, fmt.Errorf("get timestamp: %w", err)
	}
	if !found {
		return time.Time{}, fmt.Errorf("%w: missing 'timestamp' key", ErrInvalidReturnType)
	}

	switch v := tsVal.(type) {
	case starlarklib.String:
		ts, err := time.Parse(time.RFC3339, string(v))
		if err != nil {
			return time.Time{}, fmt.Errorf("parse timestamp %q: %w", string(v), err)
		}
		return ts, nil
	case starlarklib.Int:
		unixSec, ok := v.Int64()
		if !ok {
			return time.Time{}, fmt.Errorf("%w: timestamp integer too large", ErrInvalidReturnType)
		}
		return time.Unix(unixSec, 0).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("%w: timestamp must be string (RFC3339) or int (unix seconds)", ErrInvalidReturnType)
	}
}

// extractPointValue extracts and converts the value from a forecast point dict.
func extractPointValue(dict *starlarklib.Dict) (decimal.Decimal, error) {
	valVal, found, err := dict.Get(starlarklib.String("value"))
	if err != nil {
		return decimal.Zero, fmt.Errorf("get value: %w", err)
	}
	if !found {
		return decimal.Zero, fmt.Errorf("%w: missing 'value' key", ErrInvalidReturnType)
	}

	switch v := valVal.(type) {
	case *saga.DecimalValue:
		return v.GetDecimal(), nil
	case starlarklib.String:
		d, err := decimal.NewFromString(string(v))
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse value %q: %w", string(v), err)
		}
		return d, nil
	case starlarklib.Float:
		return decimal.NewFromFloat(float64(v)), nil
	case starlarklib.Int:
		i64, ok := v.Int64()
		if !ok {
			return decimal.Zero, fmt.Errorf("%w: integer value too large for decimal conversion", ErrInvalidReturnType)
		}
		return decimal.NewFromInt(i64), nil
	default:
		return decimal.Zero, fmt.Errorf("%w: value must be Decimal, string, float, or int; got %s", ErrInvalidReturnType, valVal.Type())
	}
}

// extractPointMetadata extracts the optional metadata dict from a forecast point dict.
// Returns an empty map (not nil) when metadata is absent or None.
func extractPointMetadata(dict *starlarklib.Dict) (map[string]string, error) {
	metaVal, found, err := dict.Get(starlarklib.String("metadata"))
	if err != nil {
		return nil, fmt.Errorf("get metadata: %w", err)
	}
	if !found || metaVal == starlarklib.None {
		return make(map[string]string), nil
	}

	metaDict, ok := metaVal.(*starlarklib.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: metadata must be dict, got %s", ErrInvalidReturnType, metaVal.Type())
	}

	result := make(map[string]string)
	for _, item := range metaDict.Items() {
		k, ok := item[0].(starlarklib.String)
		if !ok {
			continue
		}
		// Use type assertion to get the raw string value, avoiding Starlark repr quotes.
		if sv, ok := item[1].(starlarklib.String); ok {
			result[string(k)] = string(sv)
		} else {
			result[string(k)] = item[1].String()
		}
	}
	return result, nil
}

// forecastContextToStarlark converts a ForecastContext to a frozen Starlark dict.
func forecastContextToStarlark(fc *ForecastContext) *starlarklib.Dict {
	ctx := starlarklib.NewDict(5)

	// Convert observations: map[string][]Observation -> dict[string, list[dict]]
	obsDict := starlarklib.NewDict(len(fc.Observations))
	for code, observations := range fc.Observations {
		obsList := make([]starlarklib.Value, 0, len(observations))
		for _, obs := range observations {
			d := starlarklib.NewDict(3)
			_ = d.SetKey(starlarklib.String("timestamp"), starlarklib.String(obs.Timestamp.Format(time.RFC3339)))
			_ = d.SetKey(starlarklib.String("value"), starlarklib.String(obs.Value.String()))
			_ = d.SetKey(starlarklib.String("quality"), starlarklib.String(obs.Quality))
			obsList = append(obsList, d)
		}
		_ = obsDict.SetKey(starlarklib.String(code), starlarklib.NewList(obsList))
	}
	_ = ctx.SetKey(starlarklib.String("observations"), obsDict)

	// Convert reference data
	if fc.ReferenceData != nil {
		refDict := starlarklib.NewDict(3)
		_ = refDict.SetKey(starlarklib.String("node_type"), starlarklib.String(fc.ReferenceData.NodeType))
		_ = refDict.SetKey(starlarklib.String("resolution_key"), starlarklib.String(fc.ReferenceData.ResolutionKey))

		attrs := starlarklib.NewDict(len(fc.ReferenceData.Attributes))
		for k, v := range fc.ReferenceData.Attributes {
			_ = attrs.SetKey(starlarklib.String(k), goToStarlark(v))
		}
		_ = refDict.SetKey(starlarklib.String("attributes"), attrs)
		_ = ctx.SetKey(starlarklib.String("reference_data"), refDict)
	} else {
		_ = ctx.SetKey(starlarklib.String("reference_data"), starlarklib.None)
	}

	// Horizon in seconds
	_ = ctx.SetKey(starlarklib.String("horizon_seconds"), starlarklib.MakeInt64(int64(fc.Horizon.Seconds())))

	// Granularity in seconds
	_ = ctx.SetKey(starlarklib.String("granularity_seconds"), starlarklib.MakeInt64(int64(fc.Granularity.Seconds())))

	// Now as RFC3339
	_ = ctx.SetKey(starlarklib.String("now"), starlarklib.String(fc.Now.Format(time.RFC3339)))

	// Freeze to prevent modification by scripts
	ctx.Freeze()

	return ctx
}

// goToStarlark converts a Go value to a Starlark value.
// Mirrors the saga package's unexported function.
func goToStarlark(v interface{}) starlarklib.Value {
	if v == nil {
		return starlarklib.None
	}

	switch val := v.(type) {
	case string:
		return starlarklib.String(val)
	case int:
		return starlarklib.MakeInt(val)
	case int64:
		return starlarklib.MakeInt64(val)
	case float64:
		return starlarklib.Float(val)
	case bool:
		return starlarklib.Bool(val)
	case []interface{}:
		list := make([]starlarklib.Value, len(val))
		for i, elem := range val {
			list[i] = goToStarlark(elem)
		}
		return starlarklib.NewList(list)
	case map[string]string:
		dict := starlarklib.NewDict(len(val))
		for k, v := range val {
			_ = dict.SetKey(starlarklib.String(k), starlarklib.String(v))
		}
		return dict
	case map[string]interface{}:
		dict := starlarklib.NewDict(len(val))
		for k, v := range val {
			_ = dict.SetKey(starlarklib.String(k), goToStarlark(v))
		}
		return dict
	default:
		return starlarklib.String(fmt.Sprintf("%v", v))
	}
}

// wrapStarlarkError wraps Starlark errors with appropriate package errors.
func wrapStarlarkError(err error) error {
	if err == nil {
		return nil
	}

	var evalErr *starlarklib.EvalError
	if errors.As(err, &evalErr) {
		return errors.Join(saga.ErrExecution, err)
	}

	errStr := err.Error()
	if strings.Contains(errStr, "syntax") ||
		strings.Contains(errStr, "parse") ||
		strings.Contains(errStr, "got ") {
		return errors.Join(ErrValidation, err)
	}

	return errors.Join(saga.ErrExecution, err)
}

// SortForecastPoints sorts forecast points by timestamp.
func SortForecastPoints(points []ForecastPoint) {
	sort.Slice(points, func(i, j int) bool {
		return points[i].Timestamp.Before(points[j].Timestamp)
	})
}

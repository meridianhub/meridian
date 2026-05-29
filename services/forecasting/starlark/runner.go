// Package starlark provides a sandboxed Starlark execution environment for
// forecasting strategies. It extends the saga runtime patterns with
// forecasting-specific context injection, builtin functions, and validation.
package starlark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	if err := validateStrategyInput(input); err != nil {
		return nil, err
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	horizon := time.Duration(input.HorizonHours) * time.Hour
	granularity := time.Duration(input.GranularityHours) * time.Hour

	r.logger.Info("executing forecast strategy",
		"input_datasets", input.InputDatasetCodes,
		"output_dataset", input.OutputDatasetCode,
		"horizon_hours", input.HorizonHours,
		"granularity_hours", input.GranularityHours,
	)

	forecastCtx, err := r.buildForecastContext(ctx, input, now, horizon, granularity)
	if err != nil {
		return nil, err
	}

	points, err := r.executeScript(ctx, input.Script, forecastCtx)
	if err != nil {
		return nil, err
	}

	if err := validateForecastPoints(points, now, horizon, granularity); err != nil {
		return nil, err
	}

	r.logger.Info("forecast strategy execution completed", "point_count", len(points))

	return points, nil
}

// validateStrategyInput checks required fields and constraints on the strategy input.
func validateStrategyInput(input StrategyInput) error {
	if input.Script == "" {
		return ErrScriptRequired
	}
	if err := sandbox.ValidateScript(input.Script, sandboxCfg); err != nil {
		return fmt.Errorf("%w: size %d exceeds maximum %d bytes", ErrScriptTooLarge, len(input.Script), sandboxCfg.MaxScriptSize)
	}
	if input.HorizonHours <= 0 {
		return fmt.Errorf("%w: horizon_hours must be > 0", ErrInvalidInput)
	}
	if input.GranularityHours <= 0 {
		return fmt.Errorf("%w: granularity_hours must be > 0", ErrInvalidInput)
	}
	return nil
}

// buildForecastContext fetches observations and reference data, returning the assembled ForecastContext.
func (r *ForecastRunner) buildForecastContext(
	ctx context.Context,
	input StrategyInput,
	now time.Time,
	horizon, granularity time.Duration,
) (*ForecastContext, error) {
	observations, err := r.fetchObservations(ctx, input.InputDatasetCodes, now)
	if err != nil {
		return nil, fmt.Errorf("fetch observations: %w", err)
	}

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

	return &ForecastContext{
		Observations:  observations,
		ReferenceData: refData,
		Horizon:       horizon,
		Granularity:   granularity,
		Now:           now,
	}, nil
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
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	ctxValue := forecastContextToStarlark(forecastCtx)

	predeclared := newForecastBuiltins(r.logger)
	predeclared["Decimal"] = saga.DecimalBuiltin()

	thread := r.newSandboxedThread(ctx)

	// Phase 1: Execute top-level script to define globals
	globals, err := r.execWithTimeout(ctx, thread, func() (starlarklib.StringDict, error) {
		return starlarklib.ExecFileOptions(&syntax.FileOptions{}, thread, "forecast.star", script, predeclared)
	})
	if err != nil {
		return nil, err
	}

	// Look up compute_forecast function
	fn, err := resolveEntryPoint(globals)
	if err != nil {
		return nil, err
	}

	// Phase 2: Call compute_forecast(ctx) and convert results
	return r.callAndConvert(ctx, thread, fn, ctxValue)
}

// newSandboxedThread creates a hardened Starlark thread with cancellation support.
func (r *ForecastRunner) newSandboxedThread(ctx context.Context) *starlarklib.Thread {
	thread := &starlarklib.Thread{
		Name: "forecast",
		Print: func(_ *starlarklib.Thread, msg string) {
			r.logger.Info("forecast script print", "message", msg)
		},
	}
	thread.SetLocal("ctx", ctx)
	sandbox.HardenThread(thread, sandboxCfg)
	return thread
}

// execWithTimeout runs a Starlark exec in a goroutine with context cancellation support.
func (r *ForecastRunner) execWithTimeout(
	ctx context.Context,
	thread *starlarklib.Thread,
	fn func() (starlarklib.StringDict, error),
) (starlarklib.StringDict, error) {
	done := make(chan struct{})
	var result starlarklib.StringDict
	var execErr error

	go func() {
		defer close(done)
		result, execErr = fn()
	}()

	select {
	case <-done:
		if execErr != nil {
			return nil, wrapStarlarkError(execErr)
		}
		return result, nil
	case <-ctx.Done():
		thread.Cancel("execution cancelled")
		<-done
		return nil, r.contextError(ctx)
	}
}

// callAndConvert calls compute_forecast(ctx) with timeout and converts the result to ForecastPoints.
func (r *ForecastRunner) callAndConvert(
	ctx context.Context,
	thread *starlarklib.Thread,
	fn *starlarklib.Function,
	ctxValue starlarklib.Value,
) ([]ForecastPoint, error) {
	done := make(chan struct{})
	var callErr error
	var resultVal starlarklib.Value

	go func() {
		defer close(done)
		resultVal, callErr = starlarklib.Call(thread, fn, starlarklib.Tuple{ctxValue}, nil)
	}()

	select {
	case <-done:
		if callErr != nil {
			return nil, wrapStarlarkError(callErr)
		}
		return starlarkToForecastPoints(resultVal)
	case <-ctx.Done():
		thread.Cancel("execution cancelled")
		<-done
		return nil, r.contextError(ctx)
	}
}

// contextError maps a context error to the appropriate saga error.
func (r *ForecastRunner) contextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: exceeded %v", saga.ErrTimeout, r.timeout)
	}
	return fmt.Errorf("%w: %w", saga.ErrCancelled, ctx.Err())
}

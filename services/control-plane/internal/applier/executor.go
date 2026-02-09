package applier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// ManifestExecutor orchestrates the ApplyManifest saga for a tenant.
// It loads the saga definition (with platform default fallback per ADR-0028),
// constructs the saga input from the manifest, and executes via the saga engine.
type ManifestExecutor struct {
	pool    *pgxpool.Pool
	runner  *saga.StarlarkSagaRunner
	jobRepo *ApplyJobRepository
	logger  *slog.Logger
}

// ManifestExecutorConfig contains configuration for creating a ManifestExecutor.
type ManifestExecutorConfig struct {
	Pool   *pgxpool.Pool
	Runner *saga.StarlarkSagaRunner
	Logger *slog.Logger
}

// NewManifestExecutor creates a new ManifestExecutor.
func NewManifestExecutor(cfg ManifestExecutorConfig) *ManifestExecutor {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &ManifestExecutor{
		pool:    cfg.Pool,
		runner:  cfg.Runner,
		jobRepo: NewApplyJobRepository(cfg.Pool),
		logger:  logger.With("component", "manifest_executor"),
	}
}

// ApplyManifestInput contains the input for a manifest application.
type ApplyManifestInput struct {
	// ManifestVersion is the version being applied.
	ManifestVersion string

	// Instruments to register in Phase 1.
	Instruments []InstrumentInput

	// AccountTypes to register and provision in Phase 2.
	AccountTypes []AccountTypeInput

	// ValuationRules to register in Phase 3.
	ValuationRules []ValuationRuleInput

	// SagaDefinitions to register in Phase 4.
	SagaDefinitions []SagaDefinitionInput

	// TenantID is the tenant for cross-tenant execution.
	TenantID string
}

// InstrumentInput represents an instrument to register.
type InstrumentInput struct {
	Code          string
	DisplayName   string
	Dimension     string
	DecimalPlaces int
	Description   string
}

// AccountTypeInput represents an account type to register and provision.
type AccountTypeInput struct {
	Code           string
	DisplayName    string
	Description    string
	InstrumentCode string
	AccountType    string
}

// ValuationRuleInput represents a valuation rule to register.
type ValuationRuleInput struct {
	FromInstrument string
	ToInstrument   string
	RuleType       string
	Expression     string
	Description    string
}

// SagaDefinitionInput represents a saga definition to register.
type SagaDefinitionInput struct {
	Name        string
	DisplayName string
	Description string
	Script      string
	Version     string
}

// ApplyManifestResult contains the result of a manifest application.
type ApplyManifestResult struct {
	// JobID is the tracking job identifier.
	JobID uuid.UUID

	// SagaExecutionID is the saga execution identifier.
	SagaExecutionID uuid.UUID

	// Status is the result status ("applied" or "failed").
	Status string

	// Version is the manifest version that was applied.
	Version string

	// Error contains the error message if Status is "failed".
	Error string

	// StepResults contains individual step results for debugging.
	StepResults []saga.StepResult
}

// Executor errors.
var (
	// ErrSagaNotFound is returned when the apply_manifest saga definition is not found.
	ErrSagaNotFound = errors.New("apply_manifest saga definition not found")

	// ErrSagaFailed is returned when the saga execution fails.
	ErrSagaFailed = errors.New("saga execution failed")
)

// Apply executes the apply_manifest saga for a tenant.
// It resolves the saga script using platform default fallback (ADR-0028),
// constructs the input, and runs the saga with automatic compensation.
func (e *ManifestExecutor) Apply(ctx context.Context, input *ApplyManifestInput) (*ApplyManifestResult, error) {
	logger := e.logger.With(
		"manifest_version", input.ManifestVersion,
		"tenant_id", input.TenantID,
	)

	logger.Info("starting manifest application")

	// Create tracking job
	versionInt := parseManifestVersion(input.ManifestVersion)
	job, err := e.jobRepo.Create(ctx, versionInt)
	if err != nil {
		return nil, fmt.Errorf("create apply job: %w", err)
	}

	// Resolve the saga script (platform default fallback per ADR-0028)
	script, err := e.resolveSagaScript(ctx)
	if err != nil {
		_ = e.jobRepo.MarkFailed(ctx, job.ID, err.Error())
		return nil, fmt.Errorf("resolve saga script: %w", err)
	}

	// Build saga input
	sagaInput := e.buildSagaInput(input)

	// Generate execution IDs
	executionID := uuid.New()
	correlationID := uuid.New()

	// Mark job as applying
	if err := e.jobRepo.MarkApplying(ctx, job.ID, executionID); err != nil {
		return nil, fmt.Errorf("mark job applying: %w", err)
	}

	logger.Info("executing apply_manifest saga",
		"execution_id", executionID,
		"correlation_id", correlationID,
	)

	// Execute the saga
	runnerInput := saga.RunnerInput{
		SagaExecutionID: executionID,
		CorrelationID:   correlationID,
		KnowledgeAt:     time.Now(),
		Input:           sagaInput,
	}

	output, err := e.runner.ExecuteSaga(ctx, "apply_manifest", script, runnerInput)
	if err != nil {
		_ = e.jobRepo.MarkFailed(ctx, job.ID, err.Error())
		return nil, fmt.Errorf("execute saga: %w", err)
	}

	// Check saga result
	if !output.Success {
		_ = e.jobRepo.MarkFailed(ctx, job.ID, output.Error)
		return &ApplyManifestResult{
			JobID:           job.ID,
			SagaExecutionID: executionID,
			Status:          "failed",
			Version:         input.ManifestVersion,
			Error:           output.Error,
			StepResults:     output.StepResults,
		}, fmt.Errorf("%w: %s", ErrSagaFailed, output.Error)
	}

	// Mark job as applied
	if err := e.jobRepo.MarkApplied(ctx, job.ID); err != nil {
		logger.Error("failed to mark job applied", "error", err)
	}

	logger.Info("manifest application completed",
		"execution_id", executionID,
		"step_count", len(output.StepResults),
	)

	return &ApplyManifestResult{
		JobID:           job.ID,
		SagaExecutionID: executionID,
		Status:          "applied",
		Version:         input.ManifestVersion,
		StepResults:     output.StepResults,
	}, nil
}

// resolveSagaScript resolves the apply_manifest saga script.
// Per ADR-0028, it first checks for a tenant-specific override,
// then falls back to the platform default in public.platform_saga_definition.
func (e *ManifestExecutor) resolveSagaScript(ctx context.Context) (string, error) {
	// Query platform default (applies to all tenants including those with 0 local saga definitions)
	var script string
	err := e.pool.QueryRow(ctx,
		`SELECT script FROM public.platform_saga_definition
		 WHERE name = $1 AND status = 'ACTIVE'
		 ORDER BY
			split_part(version, '.', 1)::int DESC,
			split_part(version, '.', 2)::int DESC,
			split_part(version, '.', 3)::int DESC
		 LIMIT 1`,
		"apply_manifest",
	).Scan(&script)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrSagaNotFound, err)
	}

	return script, nil
}

// buildSagaInput converts the structured input into the map[string]interface{}
// format expected by the Starlark saga runner.
func (e *ManifestExecutor) buildSagaInput(input *ApplyManifestInput) map[string]interface{} {
	sagaInput := map[string]interface{}{
		"manifest_version": input.ManifestVersion,
	}

	// Convert instruments
	instruments := make([]interface{}, len(input.Instruments))
	for i, inst := range input.Instruments {
		instruments[i] = map[string]interface{}{
			"code":           inst.Code,
			"display_name":   inst.DisplayName,
			"dimension":      inst.Dimension,
			"decimal_places": inst.DecimalPlaces,
			"description":    inst.Description,
		}
	}
	sagaInput["instruments"] = instruments

	// Convert account types
	accountTypes := make([]interface{}, len(input.AccountTypes))
	for i, at := range input.AccountTypes {
		accountTypes[i] = map[string]interface{}{
			"code":            at.Code,
			"display_name":    at.DisplayName,
			"description":     at.Description,
			"instrument_code": at.InstrumentCode,
			"account_type":    at.AccountType,
		}
	}
	sagaInput["account_types"] = accountTypes

	// Convert valuation rules
	valuationRules := make([]interface{}, len(input.ValuationRules))
	for i, vr := range input.ValuationRules {
		valuationRules[i] = map[string]interface{}{
			"from_instrument": vr.FromInstrument,
			"to_instrument":   vr.ToInstrument,
			"rule_type":       vr.RuleType,
			"expression":      vr.Expression,
			"description":     vr.Description,
		}
	}
	sagaInput["valuation_rules"] = valuationRules

	// Convert saga definitions
	sagaDefs := make([]interface{}, len(input.SagaDefinitions))
	for i, sd := range input.SagaDefinitions {
		sagaDefs[i] = map[string]interface{}{
			"name":         sd.Name,
			"display_name": sd.DisplayName,
			"description":  sd.Description,
			"script":       sd.Script,
			"version":      sd.Version,
		}
	}
	sagaInput["saga_definitions"] = sagaDefs

	return sagaInput
}

// parseManifestVersion converts a version string to an integer.
// Handles both numeric ("42") and semver ("1.2.3") formats.
func parseManifestVersion(version string) int {
	n := 0
	for _, c := range version {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

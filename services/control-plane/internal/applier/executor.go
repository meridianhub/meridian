package applier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// PlannedResourceAction pairs a resource identifier with the diff action
// computed by the manifest differ. It is used to communicate which resources
// need to be acted upon and what kind of change is required.
type PlannedResourceAction struct {
	ResourceType differ.ResourceType
	ResourceCode string
	Action       differ.ActionType
}

// ManifestExecutor orchestrates the ApplyManifest saga for a tenant.
// It loads the saga definition (with platform default fallback per ADR-0028),
// constructs the saga input from the manifest, and executes via the saga engine.
type ManifestExecutor struct {
	pool        *pgxpool.Pool
	runner      *saga.StarlarkSagaRunner
	jobRepo     *ApplyJobRepository
	sagaDefRepo *SagaDefinitionRepository
	logger      *slog.Logger
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
		pool:        cfg.Pool,
		runner:      cfg.Runner,
		jobRepo:     NewApplyJobRepository(cfg.Pool),
		sagaDefRepo: NewSagaDefinitionRepository(cfg.Pool),
		logger:      logger.With("component", "manifest_executor"),
	}
}

// ManifestExecutorDepsConfig contains all service dependencies needed to build
// a fully wired ManifestExecutor. It assembles the handler registry, schema modules,
// and saga runner from the provided service clients.
type ManifestExecutorDepsConfig struct {
	Pool   *pgxpool.Pool
	Deps   *HandlerDependencies
	Logger *slog.Logger
}

// NewManifestExecutorFromDeps creates a ManifestExecutor with a fully wired saga runner.
// It registers all manifest handlers from deps, derives the handler schema from proto
// metadata, builds typed Starlark service modules, and assembles the StarlarkSagaRunner.
//
// This is the preferred factory for production use. The simpler NewManifestExecutor
// is available for callers that pre-build their own saga runner.
func NewManifestExecutorFromDeps(cfg ManifestExecutorDepsConfig) (*ManifestExecutor, error) {
	if cfg.Pool == nil {
		return nil, ErrPoolRequired
	}

	registry := saga.NewHandlerRegistry()
	if err := RegisterManifestHandlers(registry, cfg.Deps); err != nil {
		return nil, fmt.Errorf("register manifest handlers: %w", err)
	}

	serviceModules, err := schema.BuildServiceModules(registry)
	if err != nil {
		return nil, fmt.Errorf("build service modules: %w", err)
	}

	runtime, err := saga.NewRuntime(nil)
	if err != nil {
		return nil, fmt.Errorf("create saga runtime: %w", err)
	}

	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       registry,
		ServiceModules: serviceModules,
		Logger:         cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create saga runner: %w", err)
	}

	return NewManifestExecutor(ManifestExecutorConfig{
		Pool:   cfg.Pool,
		Runner: runner,
		Logger: cfg.Logger,
	}), nil
}

// ApplyManifestInput contains the input for a manifest application.
type ApplyManifestInput struct {
	// ManifestVersion is the version being applied.
	ManifestVersion string

	// Instruments to register in Phase 10.
	Instruments []InstrumentInput

	// AccountTypes to register in Phase 20.
	AccountTypes []AccountTypeInput

	// MarketDataSources to register in Phase 30.
	MarketDataSources []MarketDataSourceInput

	// MarketDataSets to register and activate in Phase 35.
	MarketDataSets []MarketDataSetInput

	// ValuationRules to register in Phase 40.
	ValuationRules []ValuationRuleInput

	// Organizations to register in Phase 55.
	Organizations []OrganizationInput

	// InternalAccounts to initiate in Phase 60.
	InternalAccounts []InternalAccountInput

	// SagaDefinitions to register in Phase 70.
	SagaDefinitions []SagaDefinitionInput

	// ProviderConnections to upsert in Phase 90 (Operational Gateway).
	ProviderConnections []ProviderConnectionInput

	// InstructionRoutes to upsert in Phase 90 (Operational Gateway).
	InstructionRoutes []InstructionRouteInput

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
	Action        string // Diff action: CREATE, UPDATE, DEPRECATE (empty for legacy path)
}

// AccountTypeInput represents an account type to register and provision.
type AccountTypeInput struct {
	Code                    string
	DisplayName             string
	Description             string
	BehaviorClass           string
	NormalBalance           string
	InstrumentCode          string
	AccountType             string
	DefaultSagaPrefix       string
	DefaultConversionMethod string
	ValidationCEL           string
	EligibilityCEL          string
	AttributeSchema         string
	ValuationMethods        []ValuationMethodInput
	Action                  string
}

// ValuationMethodInput represents a valuation method template for an account type.
type ValuationMethodInput struct {
	InputInstrument string
	MethodName      string
}

// ValuationRuleInput represents a valuation rule to register.
type ValuationRuleInput struct {
	FromInstrument string
	ToInstrument   string
	RuleType       string
	Expression     string
	Description    string
	Action         string
}

// SagaDefinitionInput represents a saga definition to register.
type SagaDefinitionInput struct {
	Name        string
	DisplayName string
	Description string
	Script      string
	Version     string
	Action      string
}

// ProviderConnectionInput represents a provider connection to upsert.
type ProviderConnectionInput struct {
	ConnectionID    string
	ProviderName    string
	ProviderType    string
	Protocol        string
	BaseURL         string
	AuthType        string
	AuthConfig      map[string]any
	RetryPolicy     map[string]any
	RateLimitConfig map[string]any
	Action          string
}

// InstructionRouteInput represents an instruction route to upsert.
type InstructionRouteInput struct {
	InstructionType      string
	ConnectionID         string
	FallbackConnectionID string
	OutboundMapping      string
	InboundMapping       string
	HTTPMethod           string
	PathTemplate         string
	Action               string
}

// MarketDataSourceInput represents a market data source to register.
type MarketDataSourceInput struct {
	Code        string
	Name        string
	Description string
	TrustLevel  int
	Action      string
}

// MarketDataSetInput represents a market data set to register and activate.
type MarketDataSetInput struct {
	Code                    string
	Category                string
	Unit                    string
	SourceCode              string
	DisplayName             string
	Description             string
	ValidationExpression    string
	ResolutionKeyExpression string
	Action                  string
}

// OrganizationInput represents an organization to register.
type OrganizationInput struct {
	Code                  string
	Name                  string
	LegalName             string
	DisplayName           string
	ExternalReference     string
	ExternalReferenceType string
	PartyType             string
	Attributes            map[string]string
	Action                string
}

// InternalAccountInput represents an internal account to initiate.
type InternalAccountInput struct {
	Code              string
	AccountType       string
	InstrumentCode    string
	OwnerOrganization string
	Description       string
	Action            string
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

	// ErrNilInput is returned when the apply manifest input is nil.
	ErrNilInput = errors.New("apply manifest: input is nil")

	// ErrMissingTenantID is returned when tenant_id is empty.
	ErrMissingTenantID = errors.New("apply manifest: tenant_id is required")

	// ErrPoolRequired is returned when the database pool is nil.
	ErrPoolRequired = errors.New("manifest executor: pool is required")

	// ErrExecutorNotConfigured is returned when a non-dry-run apply is attempted without an executor.
	ErrExecutorNotConfigured = errors.New("executor not configured: cannot execute non-dry-run apply")

	// ErrOperationalGatewayNotConfigured is returned when a handler requires the operational
	// gateway service but it was not provided in HandlerDependencies.
	ErrOperationalGatewayNotConfigured = errors.New("operational_gateway service not configured")
)

// Apply executes the apply_manifest saga for a tenant.
// It resolves the saga script using platform default fallback (ADR-0028),
// constructs the input, and runs the saga with automatic compensation.
func (e *ManifestExecutor) Apply(ctx context.Context, input *ApplyManifestInput) (*ApplyManifestResult, error) {
	if input == nil {
		return nil, ErrNilInput
	}
	if input.TenantID == "" {
		return nil, ErrMissingTenantID
	}

	logger := e.logger.With(
		"manifest_version", input.ManifestVersion,
		"tenant_id", input.TenantID,
	)

	logger.Info("starting manifest application")

	// Create tracking job and resolve saga script.
	job, script, err := e.prepareApplyJob(ctx, input)
	if err != nil {
		return nil, err
	}

	// Execute the saga and process the result.
	return e.executeSagaAndFinalize(ctx, input, job, script, logger)
}

// prepareApplyJob creates the tracking job, resolves the saga script, and pins
// the resolved definition into the saga_definitions table so a future resume
// path can re-execute the same script even if the platform default is later
// updated.
//
// Pinning is best-effort: if the saga_definitions table is missing (e.g. in
// older environments yet to apply the migration) or the pin fails for an
// infrastructure reason, we log and continue with the resolved script - the
// current saga still runs correctly, only durable-resume parity is lost.
func (e *ManifestExecutor) prepareApplyJob(ctx context.Context, input *ApplyManifestInput) (*ApplyJob, string, error) {
	versionInt := parseManifestVersion(input.ManifestVersion)
	job, err := e.jobRepo.Create(ctx, versionInt)
	if err != nil {
		return nil, "", fmt.Errorf("create apply job: %w", err)
	}

	script, sagaVersion, err := e.resolveSagaScript(ctx)
	if err != nil {
		_ = e.jobRepo.MarkFailed(ctx, job.ID, err.Error())
		return nil, "", fmt.Errorf("resolve saga script: %w", err)
	}

	e.pinSagaDefinition(ctx, "apply_manifest", sagaVersion, script)

	return job, script, nil
}

// pinSagaDefinition writes the resolved saga definition into saga_definitions
// via FindOrCreate. Errors are logged but not returned: a pinning failure must
// not block the apply itself.
func (e *ManifestExecutor) pinSagaDefinition(ctx context.Context, name, version, script string) {
	if e.sagaDefRepo == nil {
		return
	}
	def, err := e.sagaDefRepo.FindOrCreate(ctx, name, version, script, nil)
	if err != nil {
		e.logger.Warn("failed to pin saga definition",
			"saga_name", name,
			"saga_version", version,
			"error", err,
		)
		return
	}
	e.logger.Debug("pinned saga definition",
		"saga_name", name,
		"saga_version", version,
		"saga_definition_id", def.ID,
	)
}

// executeSagaAndFinalize runs the saga, handles failure/success, and updates the job status.
func (e *ManifestExecutor) executeSagaAndFinalize(ctx context.Context, input *ApplyManifestInput, job *ApplyJob, script string, logger *slog.Logger) (*ApplyManifestResult, error) {
	sagaInput := e.buildSagaInput(input)
	executionID := uuid.New()
	correlationID := uuid.New()

	if err := e.jobRepo.MarkApplying(ctx, job.ID, executionID); err != nil {
		return nil, fmt.Errorf("mark job applying: %w", err)
	}

	logger.Info("executing apply_manifest saga",
		"execution_id", executionID,
		"correlation_id", correlationID,
	)

	runnerInput := saga.RunnerInput{
		SagaExecutionID: executionID,
		CorrelationID:   correlationID,
		PartyScope: &saga.PartyScope{
			PartyType: "SYSTEM",
			TenantID:  input.TenantID,
		},
		KnowledgeAt: time.Now(),
		Input:       sagaInput,
	}

	output, err := e.runner.ExecuteSaga(ctx, "apply_manifest", script, runnerInput)
	if err != nil {
		_ = e.jobRepo.MarkFailed(ctx, job.ID, err.Error())
		return nil, fmt.Errorf("execute saga: %w", err)
	}

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

	if err := e.jobRepo.MarkApplied(ctx, job.ID); err != nil {
		logger.Error("failed to mark job applied", "error", err)
		return nil, fmt.Errorf("saga succeeded but job tracking failed: %w", err)
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

// resolveSagaScript resolves the apply_manifest saga script and its semver version
// from the platform default table.
// Per ADR-0028, the control plane uses the platform default directly from
// public.platform_saga_definition. Tenant-specific saga overrides are resolved
// by the Reference Data service's saga registry (GetActive), not here.
func (e *ManifestExecutor) resolveSagaScript(ctx context.Context) (script, version string, err error) {
	// Query platform default (applies to all tenants including those with 0 local saga definitions)
	err = e.pool.QueryRow(ctx,
		`SELECT script, version FROM public.platform_saga_definition
		 WHERE name = $1 AND status = 'ACTIVE'
		 ORDER BY
			COALESCE(NULLIF(split_part(version, '.', 1), '')::int, 0) DESC,
			COALESCE(NULLIF(split_part(version, '.', 2), '')::int, 0) DESC,
			COALESCE(NULLIF(split_part(version, '.', 3), '')::int, 0) DESC
		 LIMIT 1`,
		"apply_manifest",
	).Scan(&script, &version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrSagaNotFound
		}
		return "", "", fmt.Errorf("query platform saga definition: %w", err)
	}

	return script, version, nil
}

// buildSagaInput converts the structured input into the map[string]interface{}
// format expected by the Starlark saga runner.
func (e *ManifestExecutor) buildSagaInput(input *ApplyManifestInput) map[string]interface{} {
	sagaInput := map[string]interface{}{
		"manifest_version":     input.ManifestVersion,
		"instruments":          convertInstruments(input.Instruments),
		"account_types":        convertAccountTypes(input.AccountTypes),
		"market_data_sources":  convertMarketDataSources(input.MarketDataSources),
		"market_data_sets":     convertMarketDataSets(input.MarketDataSets),
		"valuation_rules":      convertValuationRules(input.ValuationRules),
		"organizations":        convertOrganizations(input.Organizations),
		"internal_accounts":    convertInternalAccounts(input.InternalAccounts),
		"saga_definitions":     convertSagaDefinitions(input.SagaDefinitions),
		"provider_connections": convertProviderConnections(input.ProviderConnections),
		"instruction_routes":   convertInstructionRoutes(input.InstructionRoutes),
	}
	return sagaInput
}

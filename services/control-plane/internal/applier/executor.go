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

// prepareApplyJob creates the tracking job and resolves the saga script.
func (e *ManifestExecutor) prepareApplyJob(ctx context.Context, input *ApplyManifestInput) (*ApplyJob, string, error) {
	versionInt := parseManifestVersion(input.ManifestVersion)
	job, err := e.jobRepo.Create(ctx, versionInt)
	if err != nil {
		return nil, "", fmt.Errorf("create apply job: %w", err)
	}

	script, err := e.resolveSagaScript(ctx)
	if err != nil {
		_ = e.jobRepo.MarkFailed(ctx, job.ID, err.Error())
		return nil, "", fmt.Errorf("resolve saga script: %w", err)
	}

	return job, script, nil
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

// resolveSagaScript resolves the apply_manifest saga script from the platform default table.
// Per ADR-0028, the control plane uses the platform default directly from
// public.platform_saga_definition. Tenant-specific saga overrides are resolved
// by the Reference Data service's saga registry (GetActive), not here.
func (e *ManifestExecutor) resolveSagaScript(ctx context.Context) (string, error) {
	// Query platform default (applies to all tenants including those with 0 local saga definitions)
	var script string
	err := e.pool.QueryRow(ctx,
		`SELECT script FROM public.platform_saga_definition
		 WHERE name = $1 AND status = 'ACTIVE'
		 ORDER BY
			COALESCE(NULLIF(split_part(version, '.', 1), '')::int, 0) DESC,
			COALESCE(NULLIF(split_part(version, '.', 2), '')::int, 0) DESC,
			COALESCE(NULLIF(split_part(version, '.', 3), '')::int, 0) DESC
		 LIMIT 1`,
		"apply_manifest",
	).Scan(&script)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrSagaNotFound
		}
		return "", fmt.Errorf("query platform saga definition: %w", err)
	}

	return script, nil
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

func convertInstruments(instruments []InstrumentInput) []interface{} {
	result := make([]interface{}, len(instruments))
	for i, inst := range instruments {
		result[i] = map[string]interface{}{
			"code":           inst.Code,
			"display_name":   inst.DisplayName,
			"dimension":      inst.Dimension,
			"decimal_places": inst.DecimalPlaces,
			"description":    inst.Description,
			"action":         inst.Action,
		}
	}
	return result
}

func convertAccountTypes(accountTypes []AccountTypeInput) []interface{} {
	result := make([]interface{}, len(accountTypes))
	for i, at := range accountTypes {
		vmethods := make([]interface{}, len(at.ValuationMethods))
		for j, vm := range at.ValuationMethods {
			vmethods[j] = map[string]interface{}{
				"input_instrument": vm.InputInstrument,
				"method_name":      vm.MethodName,
			}
		}
		result[i] = map[string]interface{}{
			"code":                      at.Code,
			"display_name":              at.DisplayName,
			"description":               at.Description,
			"behavior_class":            at.BehaviorClass,
			"normal_balance":            at.NormalBalance,
			"instrument_code":           at.InstrumentCode,
			"account_type":              at.AccountType,
			"default_saga_prefix":       at.DefaultSagaPrefix,
			"default_conversion_method": at.DefaultConversionMethod,
			"validation_cel":            at.ValidationCEL,
			"eligibility_cel":           at.EligibilityCEL,
			"attribute_schema":          at.AttributeSchema,
			"valuation_methods":         vmethods,
			"action":                    at.Action,
		}
	}
	return result
}

func convertMarketDataSources(sources []MarketDataSourceInput) []interface{} {
	result := make([]interface{}, len(sources))
	for i, src := range sources {
		result[i] = map[string]interface{}{
			"code":        src.Code,
			"name":        src.Name,
			"description": src.Description,
			"trust_level": src.TrustLevel,
			"action":      src.Action,
		}
	}
	return result
}

func convertMarketDataSets(dataSets []MarketDataSetInput) []interface{} {
	result := make([]interface{}, len(dataSets))
	for i, ds := range dataSets {
		result[i] = map[string]interface{}{
			"code":                      ds.Code,
			"category":                  ds.Category,
			"unit":                      ds.Unit,
			"source_code":               ds.SourceCode,
			"display_name":              ds.DisplayName,
			"description":               ds.Description,
			"validation_expression":     ds.ValidationExpression,
			"resolution_key_expression": ds.ResolutionKeyExpression,
			"action":                    ds.Action,
		}
	}
	return result
}

func convertValuationRules(rules []ValuationRuleInput) []interface{} {
	result := make([]interface{}, len(rules))
	for i, vr := range rules {
		result[i] = map[string]interface{}{
			"from_instrument": vr.FromInstrument,
			"to_instrument":   vr.ToInstrument,
			"rule_type":       vr.RuleType,
			"expression":      vr.Expression,
			"description":     vr.Description,
			"action":          vr.Action,
		}
	}
	return result
}

func convertOrganizations(organizations []OrganizationInput) []interface{} {
	result := make([]interface{}, len(organizations))
	for i, org := range organizations {
		attrs := make(map[string]interface{}, len(org.Attributes))
		for k, v := range org.Attributes {
			attrs[k] = v
		}
		result[i] = map[string]interface{}{
			"code":                    org.Code,
			"name":                    org.Name,
			"legal_name":              org.LegalName,
			"display_name":            org.DisplayName,
			"external_reference":      org.ExternalReference,
			"external_reference_type": org.ExternalReferenceType,
			"party_type":              org.PartyType,
			"attributes":              attrs,
			"action":                  org.Action,
		}
	}
	return result
}

func convertInternalAccounts(accounts []InternalAccountInput) []interface{} {
	result := make([]interface{}, len(accounts))
	for i, ia := range accounts {
		result[i] = map[string]interface{}{
			"code":               ia.Code,
			"account_type":       ia.AccountType,
			"instrument_code":    ia.InstrumentCode,
			"owner_organization": ia.OwnerOrganization,
			"description":        ia.Description,
			"action":             ia.Action,
		}
	}
	return result
}

func convertSagaDefinitions(defs []SagaDefinitionInput) []interface{} {
	result := make([]interface{}, len(defs))
	for i, sd := range defs {
		result[i] = map[string]interface{}{
			"name":         sd.Name,
			"display_name": sd.DisplayName,
			"description":  sd.Description,
			"script":       sd.Script,
			"version":      sd.Version,
			"action":       sd.Action,
		}
	}
	return result
}

func convertProviderConnections(conns []ProviderConnectionInput) []interface{} {
	result := make([]interface{}, len(conns))
	for i, pc := range conns {
		result[i] = map[string]interface{}{
			"connection_id":     pc.ConnectionID,
			"provider_name":     pc.ProviderName,
			"provider_type":     pc.ProviderType,
			"protocol":          pc.Protocol,
			"base_url":          pc.BaseURL,
			"auth_type":         pc.AuthType,
			"auth_config":       pc.AuthConfig,
			"retry_policy":      pc.RetryPolicy,
			"rate_limit_config": pc.RateLimitConfig,
			"action":            pc.Action,
		}
	}
	return result
}

func convertInstructionRoutes(routes []InstructionRouteInput) []interface{} {
	result := make([]interface{}, len(routes))
	for i, r := range routes {
		result[i] = map[string]interface{}{
			"instruction_type":       r.InstructionType,
			"connection_id":          r.ConnectionID,
			"fallback_connection_id": r.FallbackConnectionID,
			"outbound_mapping":       r.OutboundMapping,
			"inbound_mapping":        r.InboundMapping,
			"http_method":            r.HTTPMethod,
			"path_template":          r.PathTemplate,
			"action":                 r.Action,
		}
	}
	return result
}

// parseManifestVersion extracts the leading numeric portion of a version string.
// For numeric strings ("42"), returns the number directly.
// For semver-like strings ("1.2.3"), returns only the major version (1).
// Returns 1 as default for empty or non-numeric strings.
// The full version string is preserved separately in ApplyManifestResult.Version.
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

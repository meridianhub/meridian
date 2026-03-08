// Package applier provides the ApplyManifest gRPC handler that orchestrates
// manifest validation, diffing, planning, and execution.
package applier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sentinel errors for handler configuration validation.
var (
	ErrValidatorRequired = errors.New("validator is required")
	ErrDifferRequired    = errors.New("differ is required")
	ErrPlannerRequired   = errors.New("planner is required")
)

// ApplyManifestHandler implements the ApplyManifestService gRPC interface.
// It orchestrates: validate -> diff -> plan -> execute (or dry-run) -> record history.
type ApplyManifestHandler struct {
	controlplanev1.UnimplementedApplyManifestServiceServer

	validator      *validator.ManifestValidator
	differ         *differ.ManifestDiffer
	planner        *planner.ManifestPlanner
	executor       *ManifestExecutor
	historyService *manifest.HistoryService
	versionStore   differ.ManifestVersionStore
	postApplyHooks []PostApplyHook
	logger         *slog.Logger
}

// PostApplyHook is called after a manifest is successfully applied.
// The tenantID identifies the tenant whose manifest was applied.
// Implementations must be safe for concurrent use and should not block.
type PostApplyHook func(ctx context.Context, tenantID string)

// ApplyManifestHandlerConfig contains dependencies for creating an ApplyManifestHandler.
type ApplyManifestHandlerConfig struct {
	Validator      *validator.ManifestValidator
	Differ         *differ.ManifestDiffer
	Planner        *planner.ManifestPlanner
	Executor       *ManifestExecutor
	HistoryService *manifest.HistoryService
	VersionStore   differ.ManifestVersionStore
	Logger         *slog.Logger

	// PostApplyHooks are called after a manifest is successfully applied.
	// Used for cache invalidation (e.g., saga binding cache in the API gateway).
	PostApplyHooks []PostApplyHook
}

// NewApplyManifestHandler creates a new ApplyManifestHandler with the given dependencies.
func NewApplyManifestHandler(cfg ApplyManifestHandlerConfig) (*ApplyManifestHandler, error) {
	if cfg.Validator == nil {
		return nil, ErrValidatorRequired
	}
	if cfg.Differ == nil {
		return nil, ErrDifferRequired
	}
	if cfg.Planner == nil {
		return nil, ErrPlannerRequired
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &ApplyManifestHandler{
		validator:      cfg.Validator,
		differ:         cfg.Differ,
		planner:        cfg.Planner,
		executor:       cfg.Executor,
		historyService: cfg.HistoryService,
		versionStore:   cfg.VersionStore,
		postApplyHooks: cfg.PostApplyHooks,
		logger:         cfg.Logger.With("component", "apply_manifest_handler"),
	}, nil
}

// ApplyManifest validates and applies a manifest, returning the execution result.
func (h *ApplyManifestHandler) ApplyManifest(
	ctx context.Context,
	req *controlplanev1.ApplyManifestRequest,
) (*controlplanev1.ApplyManifestResponse, error) {
	if req.GetManifest() == nil {
		return nil, status.Error(codes.InvalidArgument, "manifest is required")
	}
	if req.GetAppliedBy() == "" {
		return nil, status.Error(codes.InvalidArgument, "applied_by is required")
	}

	logger := h.logger.With(
		"applied_by", req.GetAppliedBy(),
		"dry_run", req.GetDryRun(),
		"manifest_version", req.GetManifest().GetVersion(),
	)

	response := &controlplanev1.ApplyManifestResponse{}

	// Step 1: Validate the manifest
	logger.Info("step 1: validating manifest")
	validationResult := h.validate(ctx, req.GetManifest())
	response.StepResults = append(response.StepResults, validationResult.stepResult)

	if !validationResult.valid {
		logger.Warn("manifest validation failed", "error_count", len(validationResult.errors))
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED
		response.ValidationErrors = validationResult.errors
		return response, nil
	}

	// Step 2: Diff against current manifest
	logger.Info("step 2: diffing against current manifest")
	diffResult := h.diff(ctx, req.GetManifest())
	response.StepResults = append(response.StepResults, diffResult.stepResult)
	response.DiffSummary = diffResult.summary

	if diffResult.err != nil {
		logger.Error("diff failed", "error", diffResult.err)
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
		return response, nil //nolint:nilerr // error conveyed via response status, not gRPC error
	}

	// Check for blocked deletions
	if diffResult.plan.HasBlockedDeletions() && !req.GetForce() {
		logger.Warn("apply blocked by safety checks",
			"blocked_deletions", len(diffResult.plan.BlockedDeletions))
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_BLOCKED

		blockStep := &controlplanev1.StepResult{
			StepName: "safety_check",
			Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
			Message:  "Deletions blocked by safety checks (use force=true to override)",
			Details:  make(map[string]string),
		}
		for i, bd := range diffResult.plan.BlockedDeletions {
			blockStep.Details[fmt.Sprintf("blocked_%d", i)] = fmt.Sprintf(
				"%s %s: %s", bd.ResourceType, bd.ResourceCode, bd.Reason)
		}
		response.StepResults = append(response.StepResults, blockStep)
		return response, nil
	}

	// Step 3: Plan execution
	logger.Info("step 3: planning execution")
	tenantID, _ := tenant.FromContext(ctx)
	execPlan, planResult := h.plan(diffResult.plan, string(tenantID), req.GetManifest().GetVersion(), req.GetDryRun())
	response.StepResults = append(response.StepResults, planResult)

	if execPlan == nil {
		logger.Error("planning failed")
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
		return response, nil
	}

	// Step 4: Execute (or dry-run)
	if req.GetDryRun() {
		logger.Info("step 4: dry run - skipping execution",
			"planned_calls", len(execPlan.Calls))
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN
		response.StepResults = append(response.StepResults, &controlplanev1.StepResult{
			StepName: "execute",
			Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SKIPPED,
			Message:  fmt.Sprintf("Dry run: %d calls planned, execution skipped", len(execPlan.Calls)),
			Details: map[string]string{
				"plan_summary": execPlan.Summary(),
			},
		})
		return response, nil
	}

	logger.Info("step 4: executing manifest apply")
	execResult := h.execute(ctx, req, execPlan)
	response.StepResults = append(response.StepResults, execResult.stepResult)
	response.JobId = execResult.jobID

	if execResult.err != nil {
		logger.Error("execution failed", "error", execResult.err)
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED

		// Record failed apply in history
		h.recordHistory(ctx, req.GetManifest(), req.GetAppliedBy(), execResult.jobID, manifest.ApplyStatusFailed)
		return response, nil //nolint:nilerr // error conveyed via response status, not gRPC error
	}

	// Step 5: Record history
	logger.Info("step 5: recording manifest history")
	snapshot := h.recordHistory(ctx, req.GetManifest(), req.GetAppliedBy(), execResult.jobID, manifest.ApplyStatusApplied)
	if snapshot != nil {
		response.Snapshot = snapshot
	}

	// Save to version store for future diffs
	if h.versionStore != nil {
		if err := h.versionStore.Save(ctx, req.GetManifest(), req.GetAppliedBy()); err != nil {
			logger.Error("failed to save manifest version to differ store", "error", err)
		}
	}

	response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED
	logger.Info("manifest applied successfully", "job_id", execResult.jobID)

	// Invoke post-apply hooks (e.g., cache invalidation)
	for _, hook := range h.postApplyHooks {
		hook(ctx, string(tenantID))
	}

	return response, nil
}

// validationOutput holds the results of manifest validation.
type validationOutput struct {
	valid      bool
	errors     []*controlplanev1.ValidationError
	stepResult *controlplanev1.StepResult
}

// validate runs the manifest validator and returns structured results.
func (h *ApplyManifestHandler) validate(
	ctx context.Context,
	mf *controlplanev1.Manifest,
) validationOutput {
	// Get the previous manifest for immutability checks (best-effort)
	var previousManifest *controlplanev1.Manifest
	if h.versionStore != nil {
		prev, err := h.versionStore.GetLatestApplied(ctx)
		if err == nil && prev != nil {
			previousManifest = prev.Manifest
		}
	}

	result := h.validator.Validate(mf, previousManifest)

	step := &controlplanev1.StepResult{
		StepName: "validate",
		Details:  make(map[string]string),
	}

	if result.Valid {
		step.Status = controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS
		step.Message = fmt.Sprintf("Manifest valid (%d warnings)", len(result.Warnings))
		step.Details["warning_count"] = fmt.Sprintf("%d", len(result.Warnings))
	} else {
		step.Status = controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED
		step.Message = fmt.Sprintf("Manifest invalid: %d errors, %d warnings",
			len(result.Errors), len(result.Warnings))
		step.Details["error_count"] = fmt.Sprintf("%d", len(result.Errors))
		step.Details["warning_count"] = fmt.Sprintf("%d", len(result.Warnings))
	}

	// Convert validation errors to proto
	protoErrors := make([]*controlplanev1.ValidationError, 0, len(result.Errors))
	for _, ve := range result.Errors {
		protoErrors = append(protoErrors, &controlplanev1.ValidationError{
			Severity:   string(ve.Severity),
			Path:       ve.Path,
			Code:       ve.Code,
			Message:    ve.Message,
			Suggestion: ve.Suggestion,
		})
	}

	return validationOutput{
		valid:      result.Valid,
		errors:     protoErrors,
		stepResult: step,
	}
}

// diffOutput holds the results of manifest diffing.
type diffOutput struct {
	plan       *differ.DiffPlan
	summary    string
	stepResult *controlplanev1.StepResult
	err        error
}

// diff compares the new manifest against the last-applied manifest.
func (h *ApplyManifestHandler) diff(
	ctx context.Context,
	mf *controlplanev1.Manifest,
) diffOutput {
	// Get the last-applied manifest (nil means first apply)
	var lastApplied *controlplanev1.Manifest
	if h.versionStore != nil {
		prev, err := h.versionStore.GetLatestApplied(ctx)
		if err != nil {
			return diffOutput{
				err: err,
				stepResult: &controlplanev1.StepResult{
					StepName: "diff",
					Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
					Message:  fmt.Sprintf("Diff failed (version lookup): %s", err.Error()),
				},
			}
		}
		if prev != nil {
			lastApplied = prev.Manifest
		}
	}

	diffPlan, err := h.differ.Diff(ctx, lastApplied, mf)
	if err != nil {
		return diffOutput{
			err: err,
			stepResult: &controlplanev1.StepResult{
				StepName: "diff",
				Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
				Message:  fmt.Sprintf("Diff failed: %s", err.Error()),
			},
		}
	}

	summary := diffPlan.Summary()
	step := &controlplanev1.StepResult{
		StepName: "diff",
		Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS,
		Message:  summary,
		Details: map[string]string{
			"has_breaking_changes": fmt.Sprintf("%t", diffPlan.HasBreakingChanges),
			"action_count":         fmt.Sprintf("%d", len(diffPlan.Actions)),
		},
	}

	return diffOutput{
		plan:       diffPlan,
		summary:    summary,
		stepResult: step,
	}
}

// plan transforms a diff plan into an execution plan.
func (h *ApplyManifestHandler) plan(
	diffPlan *differ.DiffPlan,
	tenantID string,
	manifestVersion string,
	dryRun bool,
) (*planner.ExecutionPlan, *controlplanev1.StepResult) {
	execPlan, err := h.planner.Plan(diffPlan, tenantID, manifestVersion, dryRun)
	if err != nil {
		return nil, &controlplanev1.StepResult{
			StepName: "plan",
			Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
			Message:  fmt.Sprintf("Planning failed: %s", err.Error()),
		}
	}

	return execPlan, &controlplanev1.StepResult{
		StepName: "plan",
		Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS,
		Message:  execPlan.Summary(),
		Details: map[string]string{
			"total_calls": fmt.Sprintf("%d", len(execPlan.Calls)),
			"phases":      fmt.Sprintf("%d", len(execPlan.Phases())),
		},
	}
}

// executeOutput holds the results of manifest execution.
type executeOutput struct {
	jobID      string
	stepResult *controlplanev1.StepResult
	err        error
}

// execute runs the manifest apply via the executor.
func (h *ApplyManifestHandler) execute(
	ctx context.Context,
	req *controlplanev1.ApplyManifestRequest,
	execPlan *planner.ExecutionPlan,
) executeOutput {
	if h.executor == nil {
		// No executor configured - reject non-dry-run applies to prevent
		// acknowledging applies without actually executing them.
		return executeOutput{
			err: ErrExecutorNotConfigured,
			stepResult: &controlplanev1.StepResult{
				StepName: "execute",
				Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
				Message:  "Executor not configured: this deployment only supports validation and dry-run",
			},
		}
	}

	// Build executor input from the manifest
	input := buildExecutorInput(req.GetManifest())
	input.TenantID = execPlan.TenantID

	result, err := h.executor.Apply(ctx, input)
	if err != nil {
		jobID := ""
		if result != nil {
			jobID = result.JobID.String()
		}
		return executeOutput{
			jobID: jobID,
			err:   err,
			stepResult: &controlplanev1.StepResult{
				StepName: "execute",
				Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
				Message:  fmt.Sprintf("Execution failed: %s", err.Error()),
			},
		}
	}

	return executeOutput{
		jobID: result.JobID.String(),
		stepResult: &controlplanev1.StepResult{
			StepName: "execute",
			Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS,
			Message:  fmt.Sprintf("Applied successfully (job: %s, steps: %d)", result.JobID, len(result.StepResults)),
			Details: map[string]string{
				"saga_execution_id": result.SagaExecutionID.String(),
			},
		},
	}
}

// recordHistory stores the manifest version in the history service.
func (h *ApplyManifestHandler) recordHistory(
	ctx context.Context,
	mf *controlplanev1.Manifest,
	appliedBy string,
	jobID string,
	applyStatus manifest.ApplyStatus,
) *controlplanev1.ManifestVersion {
	if h.historyService == nil {
		return nil
	}

	var jobUUID *uuid.UUID
	if jobID != "" {
		parsed, err := uuid.Parse(jobID)
		if err == nil {
			jobUUID = &parsed
		}
	}

	entity, err := h.historyService.StoreManifestVersion(ctx, mf, appliedBy, jobUUID, applyStatus)
	if err != nil {
		h.logger.Error("failed to record manifest history", "error", err)
		return nil
	}

	proto, err := manifest.EntityToProto(entity)
	if err != nil {
		h.logger.Error("failed to convert history entity to proto", "error", err)
		return nil
	}

	return proto
}

// buildExecutorInput converts a Manifest proto into the ApplyManifestInput
// consumed by the saga-based ManifestExecutor.
func buildExecutorInput(mf *controlplanev1.Manifest) *ApplyManifestInput {
	input := &ApplyManifestInput{
		ManifestVersion: mf.GetVersion(),
	}

	for _, inst := range mf.GetInstruments() {
		input.Instruments = append(input.Instruments, InstrumentInput{
			Code:          inst.GetCode(),
			DisplayName:   inst.GetName(),
			Dimension:     inst.GetDimensions().GetUnit(),
			DecimalPlaces: int(inst.GetDimensions().GetPrecision()),
		})
	}

	for _, acct := range mf.GetAccountTypes() {
		input.AccountTypes = append(input.AccountTypes, AccountTypeInput{
			Code:          acct.GetCode(),
			DisplayName:   acct.GetName(),
			NormalBalance: acct.GetNormalBalance().String(),
		})
	}

	for _, vr := range mf.GetValuationRules() {
		input.ValuationRules = append(input.ValuationRules, ValuationRuleInput{
			FromInstrument: vr.GetFromInstrument(),
			ToInstrument:   vr.GetToInstrument(),
			RuleType:       vr.GetMethod().String(),
		})
	}

	for _, saga := range mf.GetSagas() {
		input.SagaDefinitions = append(input.SagaDefinitions, SagaDefinitionInput{
			Name:   saga.GetName(),
			Script: saga.GetScript(),
		})
	}

	if gw := mf.GetOperationalGateway(); gw != nil {
		for _, conn := range gw.GetProviderConnections() {
			pc := ProviderConnectionInput{
				ConnectionID: conn.GetConnectionId(),
				ProviderName: conn.GetProviderName(),
				ProviderType: conn.GetProviderType(),
				Protocol:     conn.GetProtocol().String(),
				BaseURL:      conn.GetBaseUrl(),
			}
			pc.AuthType, pc.AuthConfig = extractAuthConfig(conn.GetAuth())
			if rp := conn.GetRetryPolicy(); rp != nil {
				pc.RetryPolicy = map[string]any{
					"max_attempts":            rp.GetMaxAttempts(),
					"initial_backoff_seconds": rp.GetInitialBackoffSeconds(),
					"max_backoff_seconds":     rp.GetMaxBackoffSeconds(),
					"backoff_multiplier":      rp.GetBackoffMultiplier(),
				}
			}
			if rl := conn.GetRateLimit(); rl != nil {
				pc.RateLimitConfig = map[string]any{
					"requests_per_second": rl.GetRequestsPerSecond(),
					"burst_size":          rl.GetBurstSize(),
				}
			}
			input.ProviderConnections = append(input.ProviderConnections, pc)
		}

		for _, route := range gw.GetInstructionRoutes() {
			input.InstructionRoutes = append(input.InstructionRoutes, InstructionRouteInput{
				InstructionType:      route.GetInstructionType(),
				ConnectionID:         route.GetConnectionId(),
				FallbackConnectionID: route.GetFallbackConnectionId(),
				OutboundMapping:      route.GetOutboundMappingId(),
				InboundMapping:       route.GetInboundMappingId(),
				HTTPMethod:           route.GetHttpMethod(),
				PathTemplate:         route.GetPathTemplate(),
			})
		}
	}

	return input
}

// extractAuthConfig converts a manifest AuthConfigManifest oneof to (authType, configMap).
func extractAuthConfig(auth *controlplanev1.AuthConfigManifest) (string, map[string]any) {
	if auth == nil {
		return "", nil
	}
	switch v := auth.GetAuthConfig().(type) {
	case *controlplanev1.AuthConfigManifest_ApiKey:
		return "api_key", map[string]any{
			"header_name": v.ApiKey.GetHeaderName(),
			"secret_ref":  v.ApiKey.GetApiKeySecretRef(),
		}
	case *controlplanev1.AuthConfigManifest_Basic:
		return "basic", map[string]any{
			"username":     v.Basic.GetUsername(),
			"password_ref": v.Basic.GetPasswordSecretRef(),
		}
	case *controlplanev1.AuthConfigManifest_Oauth2:
		return "oauth2", map[string]any{
			"token_url":         v.Oauth2.GetTokenUrl(),
			"client_id":         v.Oauth2.GetClientId(),
			"client_secret_ref": v.Oauth2.GetClientSecretRef(),
			"scopes":            v.Oauth2.GetScopes(),
		}
	case *controlplanev1.AuthConfigManifest_Hmac:
		return "hmac", map[string]any{
			"algorithm":        v.Hmac.GetAlgorithm(),
			"secret_ref":       v.Hmac.GetSecretRef(),
			"signature_header": v.Hmac.GetSignatureHeader(),
		}
	case *controlplanev1.AuthConfigManifest_Mtls:
		return "mtls", map[string]any{
			"client_cert_ref": v.Mtls.GetClientCertSecretRef(),
			"client_key_ref":  v.Mtls.GetClientKeySecretRef(),
			"ca_cert_ref":     v.Mtls.GetCaCertSecretRef(),
		}
	default:
		return "", nil
	}
}

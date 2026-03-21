// Package applier provides the ApplyManifest gRPC handler that orchestrates
// manifest validation, diffing, planning, and execution.
//
//meridian:large-file - known oversized file; split tracked in backlog
package applier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	if req.GetAppliedBy() == "" && !req.GetDryRun() {
		return nil, status.Error(codes.InvalidArgument, "applied_by is required")
	}

	logger := h.logger.With(
		"applied_by", req.GetAppliedBy(),
		"dry_run", req.GetDryRun(),
		"manifest_version", req.GetManifest().GetVersion(),
	)

	response := &controlplanev1.ApplyManifestResponse{}

	// Determine whether to skip immutability/safety checks.
	// Only respected during dry-run; ignored for real applies.
	skipImmutability := req.GetSkipImmutabilityChecks() && req.GetDryRun()

	// Step 1: Validate the manifest
	logger.Info("step 1: validating manifest")
	validationResult := h.validate(ctx, req.GetManifest(), skipImmutability)
	response.StepResults = append(response.StepResults, validationResult.stepResult)

	if !validationResult.valid {
		logger.Warn("manifest validation failed", "error_count", len(validationResult.errors))
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED
		response.ValidationErrors = validationResult.errors
		return response, nil
	}

	// Step 2: Diff against current manifest
	logger.Info("step 2: diffing against current manifest")
	diffResult := h.diff(ctx, req.GetManifest(), skipImmutability)
	response.StepResults = append(response.StepResults, diffResult.stepResult)
	response.DiffSummary = diffResult.summary

	if diffResult.err != nil {
		logger.Error("diff failed", "error", diffResult.err)
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
		return response, nil //nolint:nilerr // error conveyed via response status, not gRPC error
	}

	// Check for blocked deletions
	if blocked := h.checkBlockedDeletions(diffResult.plan, req.GetForce(), logger); blocked != nil {
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_BLOCKED
		response.StepResults = append(response.StepResults, blocked)
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

		// Determine if this is a partial failure (some phases succeeded)
		applyStatus, responseStatus := classifyFailure(execResult.phaseStatus)
		response.Status = responseStatus
		response.PhaseStatus = phaseStatusMapToResponseProto(execResult.phaseStatus)

		// Record history with phase status
		_, _ = h.recordHistoryWithPhaseStatus(ctx, req.GetManifest(), req.GetAppliedBy(), execResult.jobID, applyStatus, nil, 0, execResult.phaseStatus)
		return response, nil //nolint:nilerr // error conveyed via response status, not gRPC error
	}

	// Step 5: Record history (with optimistic locking check)
	logger.Info("step 5: recording manifest history")
	expectedSeq := req.GetExpectedSequenceNumber()
	snapshot, seqErr := h.recordHistory(ctx, req.GetManifest(), req.GetAppliedBy(), execResult.jobID, manifest.ApplyStatusApplied, validationResult.graph, expectedSeq)
	if seqErr != nil {
		// Sequence conflict detected atomically during store
		logger.Warn("sequence number conflict during history recording", "error", seqErr)
		return nil, status.Errorf(codes.Aborted, "%v", seqErr)
	}
	if snapshot != nil {
		response.Snapshot = snapshot
		response.SequenceNumber = snapshot.SequenceNumber
	}

	// Save to version store for future diffs
	if h.versionStore != nil {
		if err := h.versionStore.Save(ctx, req.GetManifest(), req.GetAppliedBy()); err != nil {
			logger.Error("failed to save manifest version to differ store", "error", err)
		}
	}

	response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED
	response.PhaseStatus = phaseStatusMapToResponseProto(execResult.phaseStatus)
	logger.Info("manifest applied successfully", "job_id", execResult.jobID)

	// Invoke post-apply hooks (e.g., cache invalidation)
	h.runPostApplyHooks(ctx, string(tenantID), logger)

	return response, nil
}

// runPostApplyHooks invokes each post-apply hook with panic recovery
// to prevent a misbehaving hook from crashing the gRPC handler.
func (h *ApplyManifestHandler) runPostApplyHooks(ctx context.Context, tenantID string, logger *slog.Logger) {
	for i, hook := range h.postApplyHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("post-apply hook panicked", "hook_index", i, "panic", r)
				}
			}()
			hook(ctx, tenantID)
		}()
	}
}

// checkBlockedDeletions returns a StepResult if the plan contains blocked deletions
// and force is not set. Returns nil if there are no blocked deletions or force overrides them.
func (h *ApplyManifestHandler) checkBlockedDeletions(plan *differ.DiffPlan, force bool, logger *slog.Logger) *controlplanev1.StepResult {
	if !plan.HasBlockedDeletions() || force {
		return nil
	}
	logger.Warn("apply blocked by safety checks",
		"blocked_deletions", len(plan.BlockedDeletions))

	step := &controlplanev1.StepResult{
		StepName: "safety_check",
		Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
		Message:  "Deletions blocked by safety checks (use force=true to override)",
		Details:  make(map[string]string),
	}
	for i, bd := range plan.BlockedDeletions {
		step.Details[fmt.Sprintf("blocked_%d", i)] = fmt.Sprintf(
			"%s %s: %s", bd.ResourceType, bd.ResourceCode, bd.Reason)
	}
	return step
}

// validationOutput holds the results of manifest validation.
type validationOutput struct {
	valid      bool
	errors     []*controlplanev1.ValidationError
	stepResult *controlplanev1.StepResult
	graph      *validator.RelationshipGraph
}

// validate runs the manifest validator and returns structured results.
func (h *ApplyManifestHandler) validate(
	ctx context.Context,
	mf *controlplanev1.Manifest,
	skipImmutability bool,
) validationOutput {
	// Get the previous manifest for immutability checks (best-effort).
	// When skipImmutability is true we model a new-tenant create, so there
	// is no previous manifest to compare against.
	var previousManifest *controlplanev1.Manifest
	if h.versionStore != nil && !skipImmutability {
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
			Severity:     string(ve.Severity),
			Path:         ve.Path,
			Code:         ve.Code,
			Message:      ve.Message,
			Suggestion:   ve.Suggestion,
			ResourceType: ve.ResourceType,
			ResourceId:   ve.ResourceID,
		})
	}

	return validationOutput{
		valid:      result.Valid,
		errors:     protoErrors,
		stepResult: step,
		graph:      result.Graph,
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
	skipImmutability bool,
) diffOutput {
	// Get the last-applied manifest (nil means first apply).
	// When skipImmutability is true we model a new-tenant create, so there
	// is no baseline to diff against — everything is treated as CREATE.
	var lastApplied *controlplanev1.Manifest
	if h.versionStore != nil && !skipImmutability {
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
	jobID       string
	stepResult  *controlplanev1.StepResult
	err         error
	phaseStatus manifest.PhaseStatusMap
}

// execute runs the manifest apply via the executor.
func (h *ApplyManifestHandler) execute(
	ctx context.Context,
	req *controlplanev1.ApplyManifestRequest,
	execPlan *planner.ExecutionPlan,
) executeOutput {
	if h.executor == nil {
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

	// Derive phase status from execution plan phases
	phaseStatus := buildInitialPhaseStatus(execPlan)

	result, err := h.executor.Apply(ctx, input)

	// Update phase status based on execution outcome
	updatePhaseStatus(phaseStatus, execPlan, result, err)

	if err != nil {
		jobID := ""
		if result != nil {
			jobID = result.JobID.String()
		}
		return executeOutput{
			jobID:       jobID,
			err:         err,
			phaseStatus: phaseStatus,
			stepResult: &controlplanev1.StepResult{
				StepName: "execute",
				Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_FAILED,
				Message:  fmt.Sprintf("Execution failed: %s", err.Error()),
			},
		}
	}

	return executeOutput{
		jobID:       result.JobID.String(),
		phaseStatus: phaseStatus,
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

// buildInitialPhaseStatus creates a PhaseStatusMap with PENDING entries for each
// phase present in the execution plan.
func buildInitialPhaseStatus(plan *planner.ExecutionPlan) manifest.PhaseStatusMap {
	phases := plan.Phases()
	m := make(manifest.PhaseStatusMap, len(phases))
	for _, p := range phases {
		key := fmt.Sprintf("phase_%d", p)
		m[key] = manifest.PhaseStatusEntry{
			Status: manifest.PhaseStatusPending,
		}
	}
	return m
}

// updatePhaseStatus updates phase entries based on the execution result.
// On success, all phases are marked COMPLETED.
// On failure, phases are marked based on step results: completed phases get COMPLETED,
// the phase containing the failing step gets FAILED, and remaining phases get SKIPPED.
func updatePhaseStatus(
	phaseStatus manifest.PhaseStatusMap,
	plan *planner.ExecutionPlan,
	result *ApplyManifestResult,
	execErr error,
) {
	now := time.Now().UTC()
	phases := plan.Phases()

	if execErr == nil && result != nil {
		// All phases completed successfully
		for _, p := range phases {
			key := fmt.Sprintf("phase_%d", p)
			entry := phaseStatus[key]
			entry.Status = manifest.PhaseStatusCompleted
			entry.StartedAt = &now
			entry.CompletedAt = &now
			phaseStatus[key] = entry
		}
		return
	}

	// On failure: mark phases based on step results.
	// Steps are executed in phase order. We mark phases as COMPLETED until we
	// find a failed step, then the containing phase is FAILED and the rest are SKIPPED.
	failedPhase := findFailedPhase(plan, result)

	for _, p := range phases {
		key := fmt.Sprintf("phase_%d", p)
		entry := phaseStatus[key]
		entry.StartedAt = &now

		if failedPhase > 0 && p < failedPhase {
			entry.Status = manifest.PhaseStatusCompleted
			entry.CompletedAt = &now
		} else if failedPhase > 0 && p == failedPhase {
			entry.Status = manifest.PhaseStatusFailed
			entry.CompletedAt = &now
			if result != nil {
				entry.Error = result.Error
			} else if execErr != nil {
				entry.Error = execErr.Error()
			}
		} else if failedPhase > 0 {
			entry.Status = manifest.PhaseStatusSkipped
			entry.StartedAt = nil
		} else {
			// No phase-level info available; mark all as failed
			entry.Status = manifest.PhaseStatusFailed
			entry.CompletedAt = &now
			if execErr != nil {
				entry.Error = execErr.Error()
			}
		}
		phaseStatus[key] = entry
	}
}

// findFailedPhase returns the phase number of the first failed step, or 0 if
// it cannot be determined from step results.
//
// Step results are ordered by execution sequence, matching the call order in
// the execution plan. We use positional correlation: step result at index i
// corresponds to planned call at index i. This avoids the naming mismatch
// between saga handler names (e.g. "reference_data.register_instrument") and
// gRPC method paths in the execution plan.
func findFailedPhase(plan *planner.ExecutionPlan, result *ApplyManifestResult) planner.Phase {
	if result == nil || len(result.StepResults) == 0 {
		return 0
	}

	for i, step := range result.StepResults {
		if !step.Success {
			if i < len(plan.Calls) {
				return plan.Calls[i].Phase
			}
			return 0
		}
	}

	return 0
}

// recordHistory stores the manifest version in the history service.
// expectedSeq is passed through for atomic optimistic locking; 0 skips the check.
// Returns (nil, nil) if historyService is not configured.
// Returns a non-nil error only for sequence conflicts (ErrSequenceConflict).
func (h *ApplyManifestHandler) recordHistory(
	ctx context.Context,
	mf *controlplanev1.Manifest,
	appliedBy string,
	jobID string,
	applyStatus manifest.ApplyStatus,
	graph *validator.RelationshipGraph,
	expectedSeq int64,
) (*controlplanev1.ManifestVersion, error) {
	if h.historyService == nil {
		return nil, nil //nolint:nilnil // history recording is optional
	}

	var jobUUID *uuid.UUID
	if jobID != "" {
		parsed, err := uuid.Parse(jobID)
		if err == nil {
			jobUUID = &parsed
		}
	}

	entity, err := h.historyService.StoreManifestVersion(ctx, mf, appliedBy, jobUUID, applyStatus, graph, expectedSeq)
	if err != nil {
		if errors.Is(err, manifest.ErrSequenceConflict) {
			return nil, err
		}
		h.logger.Error("failed to record manifest history", "error", err)
		return nil, nil //nolint:nilnil // non-conflict errors are logged but non-fatal
	}

	proto, err := manifest.EntityToProto(entity)
	if err != nil {
		h.logger.Error("failed to convert history entity to proto", "error", err)
		return nil, nil //nolint:nilnil // conversion errors are logged but non-fatal
	}

	return proto, nil
}

// classifyFailure examines phase statuses to determine if this is a partial
// or complete failure. Returns both the internal ApplyStatus and proto status.
func classifyFailure(ps manifest.PhaseStatusMap) (manifest.ApplyStatus, controlplanev1.ApplyManifestStatus) {
	if ps == nil {
		return manifest.ApplyStatusFailed, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
	}
	hasCompleted := false
	hasFailed := false
	for _, entry := range ps {
		switch entry.Status { //nolint:exhaustive // only COMPLETED/FAILED affect classification
		case manifest.PhaseStatusCompleted:
			hasCompleted = true
		case manifest.PhaseStatusFailed:
			hasFailed = true
		}
	}
	if hasCompleted && hasFailed {
		return manifest.ApplyStatusPartial, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_PARTIAL
	}
	return manifest.ApplyStatusFailed, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
}

// phaseStatusMapToResponseProto converts a PhaseStatusMap to proto map for the response.
func phaseStatusMapToResponseProto(ps manifest.PhaseStatusMap) map[string]*controlplanev1.PhaseStatusDetail {
	if ps == nil {
		return nil
	}
	result := make(map[string]*controlplanev1.PhaseStatusDetail, len(ps))
	for key, entry := range ps {
		detail := &controlplanev1.PhaseStatusDetail{
			Status: string(entry.Status),
			Error:  entry.Error,
		}
		if entry.StartedAt != nil {
			detail.StartedAt = timestamppb.New(*entry.StartedAt)
		}
		if entry.CompletedAt != nil {
			detail.CompletedAt = timestamppb.New(*entry.CompletedAt)
		}
		result[key] = detail
	}
	return result
}

// recordHistoryWithPhaseStatus stores the manifest version in history with phase status.
func (h *ApplyManifestHandler) recordHistoryWithPhaseStatus(
	ctx context.Context,
	mf *controlplanev1.Manifest,
	appliedBy string,
	jobID string,
	applyStatus manifest.ApplyStatus,
	graph *validator.RelationshipGraph,
	expectedSeq int64,
	phaseStatus manifest.PhaseStatusMap,
) (*controlplanev1.ManifestVersion, error) {
	if h.historyService == nil {
		return nil, nil //nolint:nilnil // history recording is optional
	}

	var jobUUID *uuid.UUID
	if jobID != "" {
		parsed, err := uuid.Parse(jobID)
		if err == nil {
			jobUUID = &parsed
		}
	}

	entity, err := h.historyService.StoreManifestVersionWithPhaseStatus(ctx, mf, appliedBy, jobUUID, applyStatus, graph, expectedSeq, phaseStatus)
	if err != nil {
		if errors.Is(err, manifest.ErrSequenceConflict) {
			return nil, err
		}
		h.logger.Error("failed to record manifest history with phase status", "error", err)
		return nil, nil //nolint:nilnil // non-conflict errors are logged but non-fatal
	}

	proto, err := manifest.EntityToProto(entity)
	if err != nil {
		h.logger.Error("failed to convert history entity to proto", "error", err)
		return nil, nil //nolint:nilnil // conversion errors are logged but non-fatal
	}

	return proto, nil
}

// buildExecutorInput converts a Manifest proto into the ApplyManifestInput
// consumed by the saga-based ManifestExecutor.
func buildExecutorInput(mf *controlplanev1.Manifest) *ApplyManifestInput {
	input := &ApplyManifestInput{
		ManifestVersion: mf.GetVersion(),
	}

	for _, inst := range mf.GetInstruments() {
		dim := instrumentTypeToDimension(inst.GetType(), inst.GetDimensions().GetUnit())
		if dim == "" {
			// Fallback: the Starlark script's .get("dimension", "CURRENCY") only
			// kicks in when the key is absent, not when it's empty. Use CURRENCY
			// as a safe default so the saga can proceed. A future manifest proto
			// change should add an explicit dimension field.
			dim = "CURRENCY"
		}
		input.Instruments = append(input.Instruments, InstrumentInput{
			Code:          inst.GetCode(),
			DisplayName:   inst.GetName(),
			Dimension:     dim,
			DecimalPlaces: int(inst.GetDimensions().GetPrecision()),
		})
	}

	for _, acct := range mf.GetAccountTypes() {
		nb := stripEnumPrefix(acct.GetNormalBalance().String(), "NORMAL_BALANCE_")
		if nb == "UNSPECIFIED" {
			nb = "DEBIT"
		}
		// Use the first allowed instrument as the account type's instrument code.
		var instrumentCode string
		if instruments := acct.GetAllowedInstruments(); len(instruments) > 0 {
			instrumentCode = instruments[0]
		}
		input.AccountTypes = append(input.AccountTypes, AccountTypeInput{
			Code:           acct.GetCode(),
			DisplayName:    acct.GetName(),
			NormalBalance:  nb,
			BehaviorClass:  "HOLDING",
			InstrumentCode: instrumentCode,
			AccountType:    acct.GetCode(), // used by saga auto-derivation for internal accounts
		})
	}

	for _, vr := range mf.GetValuationRules() {
		input.ValuationRules = append(input.ValuationRules, ValuationRuleInput{
			FromInstrument: vr.GetFromInstrument(),
			ToInstrument:   vr.GetToInstrument(),
			RuleType:       vr.GetMethod().String(),
		})
	}

	extractMarketData(mf, input)
	extractPartyAndAccounts(mf, input)

	for _, saga := range mf.GetSagas() {
		input.SagaDefinitions = append(input.SagaDefinitions, SagaDefinitionInput{
			Name:   saga.GetName(),
			Script: saga.GetScript(),
		})
	}

	extractOperationalGateway(mf, input)

	return input
}

// extractMarketData converts market data sources and data sets from the manifest proto.
func extractMarketData(mf *controlplanev1.Manifest, input *ApplyManifestInput) {
	md := mf.GetMarketData()
	if md == nil {
		return
	}
	for _, src := range md.GetSources() {
		input.MarketDataSources = append(input.MarketDataSources, MarketDataSourceInput{
			Code:        src.GetCode(),
			Name:        src.GetName(),
			Description: src.GetDescription(),
			TrustLevel:  int(src.GetTrustLevel()),
		})
	}
	for _, ds := range md.GetDatasets() {
		input.MarketDataSets = append(input.MarketDataSets, MarketDataSetInput{
			Code:                    ds.GetCode(),
			Category:                stripEnumPrefix(ds.GetCategory().String(), "DATA_CATEGORY_"),
			Unit:                    ds.GetUnit(),
			SourceCode:              ds.GetSourceCode(),
			DisplayName:             ds.GetDisplayName(),
			Description:             ds.GetDescription(),
			ValidationExpression:    ds.GetValidationExpression(),
			ResolutionKeyExpression: ds.GetResolutionKeyExpression(),
		})
	}
}

// extractPartyAndAccounts converts organizations and internal accounts from the manifest proto.
func extractPartyAndAccounts(mf *controlplanev1.Manifest, input *ApplyManifestInput) {
	for _, org := range mf.GetOrganizations() {
		// Resolve legal_name with fallback chain: legal_name -> name -> code
		legalName := org.GetLegalName()
		if legalName == "" {
			legalName = org.GetName()
		}
		if legalName == "" {
			legalName = org.GetCode()
		}

		// Resolve display_name with fallback chain: display_name -> legal_name
		displayName := org.GetDisplayName()
		if displayName == "" {
			displayName = legalName
		}

		// Resolve external_reference with fallback: external_reference -> code
		extRef := org.GetExternalReference()
		if extRef == "" {
			extRef = org.GetCode()
		}

		input.Organizations = append(input.Organizations, OrganizationInput{
			Code:                  org.GetCode(),
			Name:                  org.GetName(),
			LegalName:             legalName,
			DisplayName:           displayName,
			ExternalReference:     extRef,
			ExternalReferenceType: org.GetExternalReferenceType(),
			PartyType:             org.GetPartyType(),
			Attributes:            org.GetAttributes(),
		})
	}
	for _, ia := range mf.GetInternalAccounts() {
		input.InternalAccounts = append(input.InternalAccounts, InternalAccountInput{
			Code:              ia.GetCode(),
			AccountType:       ia.GetAccountType(),
			InstrumentCode:    ia.GetInstrument(),
			OwnerOrganization: ia.GetOwnerOrganization(),
			Description:       ia.GetDescription(),
		})
	}
}

// extractOperationalGateway converts operational gateway config from the manifest proto.
func extractOperationalGateway(mf *controlplanev1.Manifest, input *ApplyManifestInput) {
	gw := mf.GetOperationalGateway()
	if gw == nil {
		return
	}
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

// instrumentTypeToDimension derives the Dimension enum name from the manifest
// InstrumentType and unit. FIAT→CURRENCY, VOUCHER→COUNT. For COMMODITY and
// other types, checks if the uppercased unit is a valid Dimension enum name;
// otherwise returns empty string so the Starlark script uses its default.
func instrumentTypeToDimension(instType controlplanev1.InstrumentType, unit string) string {
	switch instType {
	case controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT:
		return "CURRENCY"
	case controlplanev1.InstrumentType_INSTRUMENT_TYPE_VOUCHER:
		return "COUNT"
	case controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
		controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED:
		// Check if the uppercased unit matches a known Dimension enum name.
		upper := strings.ToUpper(unit)
		if _, ok := referencedatav1.Dimension_value["DIMENSION_"+upper]; ok {
			return upper
		}
		// Not a known dimension — return empty so the Starlark default applies.
		return ""
	}
	return ""
}

// stripEnumPrefix removes a common prefix from a proto enum string representation.
// For example, stripEnumPrefix("NORMAL_BALANCE_DEBIT", "NORMAL_BALANCE_") returns "DEBIT".
// Returns the original string if the prefix is not found.
func stripEnumPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
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

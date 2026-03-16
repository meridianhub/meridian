package applier

import (
	"context"
	"fmt"
	"log/slog"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ApplyResource applies a granular mutation to a single resource within the manifest.
// It reads the current manifest from the version store, patches the specified
// resource into it, validates the full dependency graph, computes a diff, and
// executes only the affected phases. A new manifest version is created on success.
func (h *ApplyManifestHandler) ApplyResource(
	ctx context.Context,
	req *controlplanev1.ApplyResourceRequest,
) (*controlplanev1.ApplyResourceResponse, error) {
	if err := validateApplyResourceRequest(req); err != nil {
		return nil, err
	}

	logger := h.logger.With(
		"rpc", "ApplyResource",
		"resource_type", req.GetResourceType().String(),
		"resource_id", resourceID(req),
		"applied_by", req.GetAppliedBy(),
		"dry_run", req.GetDryRun(),
	)

	// Load current manifest and produce patched version.
	patchedManifest, currentManifest, err := h.loadAndPatch(ctx, req, logger)
	if err != nil {
		return nil, err
	}

	// Run the validate -> diff -> plan -> execute pipeline on the patched manifest.
	return h.applyPatchedManifest(ctx, req, patchedManifest, currentManifest, logger)
}

// validateApplyResourceRequest checks that the request has all required fields.
func validateApplyResourceRequest(req *controlplanev1.ApplyResourceRequest) error {
	if req.GetResource() == nil {
		return status.Error(codes.InvalidArgument, "resource payload is required")
	}
	if req.GetAppliedBy() == "" && !req.GetDryRun() {
		return status.Error(codes.InvalidArgument, "applied_by is required for non-dry-run applies")
	}
	return nil
}

// loadAndPatch reads the current manifest from the version store and patches
// the resource from the request into it. Returns the patched manifest and the
// current (unmodified) manifest.
func (h *ApplyManifestHandler) loadAndPatch(
	ctx context.Context,
	req *controlplanev1.ApplyResourceRequest,
	logger *slog.Logger,
) (*controlplanev1.Manifest, *controlplanev1.Manifest, error) {
	logger.Info("loading current manifest")
	if h.versionStore == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "version store not configured")
	}

	prev, err := h.versionStore.GetLatestApplied(ctx)
	if err != nil {
		logger.Error("failed to load current manifest", "error", err)
		return nil, nil, status.Errorf(codes.Internal, "failed to load current manifest: %v", err)
	}
	if prev == nil || prev.Manifest == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, ErrNoCurrentManifest.Error())
	}

	logger.Info("patching resource into manifest")
	patched, patchErr := patchResource(prev.Manifest, req)
	if patchErr != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "resource patch failed: %v", patchErr)
	}

	return patched, prev.Manifest, nil
}

// applyPatchedManifest runs the validate -> diff -> plan -> execute pipeline
// on a patched manifest, diffing against the current manifest. It handles
// both dry-run and real execution modes.
func (h *ApplyManifestHandler) applyPatchedManifest(
	ctx context.Context,
	req *controlplanev1.ApplyResourceRequest,
	patchedManifest *controlplanev1.Manifest,
	currentManifest *controlplanev1.Manifest,
	logger *slog.Logger,
) (*controlplanev1.ApplyResourceResponse, error) {
	response := &controlplanev1.ApplyResourceResponse{}

	// Step 1: Validate
	logger.Info("validating patched manifest")
	validationResult := h.validate(ctx, patchedManifest, false)
	response.StepResults = append(response.StepResults, validationResult.stepResult)

	if !validationResult.valid {
		logger.Warn("patched manifest validation failed", "error_count", len(validationResult.errors))
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED
		response.ValidationErrors = validationResult.errors
		return response, nil
	}

	// Step 2: Diff
	logger.Info("diffing patched manifest against current")
	diffResult := h.diffAgainst(ctx, currentManifest, patchedManifest)
	response.StepResults = append(response.StepResults, diffResult.stepResult)
	response.DiffSummary = diffResult.summary

	if diffResult.err != nil {
		logger.Error("diff failed", "error", diffResult.err)
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
		return response, nil //nolint:nilerr // error conveyed via response status, not gRPC error
	}

	if blocked := h.checkBlockedDeletions(diffResult.plan, false, logger); blocked != nil {
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_BLOCKED
		response.StepResults = append(response.StepResults, blocked)
		return response, nil
	}

	// Step 3: Plan
	logger.Info("planning execution")
	tenantID, _ := tenant.FromContext(ctx)
	execPlan, planResult := h.plan(diffResult.plan, string(tenantID), patchedManifest.GetVersion(), req.GetDryRun())
	response.StepResults = append(response.StepResults, planResult)

	if execPlan == nil {
		logger.Error("planning failed")
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
		return response, nil
	}

	// Step 4: Execute or dry-run
	if req.GetDryRun() {
		return h.applyResourceDryRun(response, execPlan, logger), nil
	}

	return h.applyResourceExecute(ctx, req, response, patchedManifest, execPlan, validationResult, logger)
}

// applyResourceDryRun adds the dry-run step result and returns the response.
func (h *ApplyManifestHandler) applyResourceDryRun(
	response *controlplanev1.ApplyResourceResponse,
	execPlan *planner.ExecutionPlan,
	logger *slog.Logger,
) *controlplanev1.ApplyResourceResponse {
	logger.Info("dry run - skipping execution", "planned_calls", len(execPlan.Calls))
	response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN
	response.StepResults = append(response.StepResults, &controlplanev1.StepResult{
		StepName: "execute",
		Status:   controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SKIPPED,
		Message:  fmt.Sprintf("Dry run: %d calls planned, execution skipped", len(execPlan.Calls)),
		Details: map[string]string{
			"plan_summary": execPlan.Summary(),
		},
	})
	return response
}

// applyResourceExecute runs the executor, records history, and saves the version.
func (h *ApplyManifestHandler) applyResourceExecute(
	ctx context.Context,
	req *controlplanev1.ApplyResourceRequest,
	response *controlplanev1.ApplyResourceResponse,
	patchedManifest *controlplanev1.Manifest,
	execPlan *planner.ExecutionPlan,
	validationResult validationOutput,
	logger *slog.Logger,
) (*controlplanev1.ApplyResourceResponse, error) {
	logger.Info("executing resource apply")
	execResult := h.execute(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  patchedManifest,
		AppliedBy: req.GetAppliedBy(),
	}, execPlan)
	response.StepResults = append(response.StepResults, execResult.stepResult)

	if execResult.err != nil {
		logger.Error("execution failed", "error", execResult.err)
		response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED
		return response, nil //nolint:nilerr // error conveyed via response status, not gRPC error
	}

	// Record history (creates new manifest version)
	logger.Info("recording manifest history")
	snapshot, seqErr := h.recordHistory(ctx, patchedManifest, req.GetAppliedBy(), execResult.jobID, manifest.ApplyStatusApplied, validationResult.graph, req.GetExpectedSequenceNumber())
	if seqErr != nil {
		logger.Warn("sequence number conflict during history recording", "error", seqErr)
		return nil, status.Errorf(codes.Aborted, "%v", seqErr)
	}
	if snapshot != nil {
		response.Snapshot = snapshot
		response.SequenceNumber = snapshot.SequenceNumber
	}

	// Save to version store for future diffs
	if err := h.versionStore.Save(ctx, patchedManifest, req.GetAppliedBy()); err != nil {
		logger.Error("failed to save manifest version to differ store", "error", err)
	}

	response.Status = controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED
	logger.Info("resource applied successfully",
		"resource_type", req.GetResourceType().String(),
		"resource_id", resourceID(req))

	tenantID, _ := tenant.FromContext(ctx)
	h.runPostApplyHooks(ctx, string(tenantID), logger)

	return response, nil
}

// diffAgainst compares a new manifest against a specific base manifest.
// Unlike the diff() method which loads the base from the version store,
// this takes the base explicitly — used by ApplyResource where we already
// have the current manifest loaded.
func (h *ApplyManifestHandler) diffAgainst(
	ctx context.Context,
	base *controlplanev1.Manifest,
	newManifest *controlplanev1.Manifest,
) diffOutput {
	diffPlan, err := h.differ.Diff(ctx, base, newManifest)
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

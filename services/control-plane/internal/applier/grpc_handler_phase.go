package applier

import (
	"fmt"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

package tools

import (
	"context"
	"encoding/json"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// parseAndConvertManifest parses a manifest input (YAML/JSON string or JSON object) into a proto Manifest.
// Returns a validation error response (non-nil) on failure, or the manifest on success.
func parseAndConvertManifest(input interface{}) (*controlplanev1.Manifest, interface{}) {
	manifestJSON, err := parseManifestInput(input)
	if err != nil {
		return nil, map[string]interface{}{
			"valid":  false,
			"errors": []interface{}{map[string]interface{}{"type": mcperrors.TypeManifestValidation, "message": err.Error()}},
		}
	}
	manifest, err := manifestJSONToProto(manifestJSON)
	if err != nil {
		return nil, map[string]interface{}{
			"valid":  false,
			"errors": []interface{}{map[string]interface{}{"type": mcperrors.TypeManifestValidation, "message": err.Error()}},
		}
	}
	return manifest, nil
}

// buildManifestValidateTool returns the meridian_manifest_validate tool.
func buildManifestValidateTool(client ManifestApplier) Tool {
	return Tool{
		Name:     "meridian_manifest_validate",
		Category: CategorySimulate,
		Description: "Validate a manifest YAML/JSON without applying it. " +
			"Runs structural validation and returns any errors with paths and suggestions. " +
			"Use mode='create' (default) for new economy validation or mode='amend' with tenant_id to validate against an existing tenant's manifest.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"manifest": map[string]interface{}{
					"oneOf": []interface{}{
						map[string]interface{}{"type": "object"},
						map[string]interface{}{"type": "string"},
					},
					"description": "The manifest to validate, as a YAML/JSON string or a JSON object.",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"description": "Validation mode: 'create' (default) performs schema-only validation for new economies; 'amend' validates against the existing tenant's manifest.",
					"enum":        []interface{}{"create", "amend"},
				},
				"tenant_id": map[string]interface{}{
					"type":        "string",
					"description": "Required for amend mode. The tenant whose manifest to compare against.",
				},
			},
			"required": []interface{}{"manifest"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleManifestValidate(ctx, client, params)
		},
	}
}

// manifestValidateParams holds parsed parameters for meridian_manifest_validate.
type manifestValidateParams struct {
	Manifest interface{} `json:"manifest"` // string (YAML/JSON) or object
	Mode     string      `json:"mode"`     // "create" or "amend"
	TenantID string      `json:"tenant_id"`
}

// handleManifestValidate implements the meridian_manifest_validate handler logic.
func handleManifestValidate(ctx context.Context, client ManifestApplier, params json.RawMessage) (interface{}, error) {
	var p manifestValidateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	// Validate mode parameter.
	var skipImmutabilityChecks bool
	switch p.Mode {
	case "", "create":
		// Create mode: schema-only validation, skip tenant state comparison.
		// SkipImmutabilityChecks bypasses immutability enforcement so a new manifest
		// can be validated without comparing against any existing tenant state.
		skipImmutabilityChecks = true
	case "amend":
		if p.TenantID == "" {
			return map[string]interface{}{
				"error":   "tenant_id is required when mode is 'amend'",
				"message": "Provide a tenant_id to validate against the tenant's existing manifest.",
			}, nil
		}
		// Inject tenant context so the control plane validates against the correct tenant's state.
		ctx = tenant.WithTenant(ctx, tenant.TenantID(p.TenantID))
	default:
		return map[string]interface{}{
			"error":   "invalid mode: " + p.Mode,
			"message": "mode must be 'create' or 'amend'",
		}, nil
	}

	manifest, errResp := parseAndConvertManifest(p.Manifest)
	if errResp != nil {
		return errResp, nil
	}

	resp, err := client.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:               manifest,
		DryRun:                 true,
		SkipImmutabilityChecks: skipImmutabilityChecks,
		AppliedBy:              "mcp-server-validate",
	})
	if err != nil {
		formatted := mcperrors.FormatGRPCError(err)
		return map[string]interface{}{
			"valid":  false,
			"errors": formatValidationErrors(formatted.Errors),
		}, nil
	}

	if resp.Status == controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED {
		return map[string]interface{}{
			"valid":  false,
			"errors": formatProtoValidationErrors(resp.ValidationErrors),
		}, nil
	}

	return map[string]interface{}{
		"valid":   true,
		"message": "Manifest is valid",
	}, nil
}

// buildManifestPlanTool returns the meridian_manifest_plan tool.
func buildManifestPlanTool(client ManifestApplier, sess PlanStore) Tool {
	return Tool{
		Name:     "meridian_manifest_plan",
		Category: CategoryWrite,
		Description: "Dry-run a manifest apply and store the result for later application. " +
			"Returns a diff summary and a plan_hash that must be provided to meridian_manifest_apply. " +
			"Use this to preview changes before committing them.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"manifest": map[string]interface{}{
					"oneOf": []interface{}{
						map[string]interface{}{"type": "object"},
						map[string]interface{}{"type": "string"},
					},
					"description": "The manifest to plan, as a YAML/JSON string or a JSON object.",
				},
			},
			"required": []interface{}{"manifest"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleManifestPlan(ctx, client, sess, params)
		},
	}
}

// manifestPlanParams holds parsed parameters for meridian_manifest_plan.
type manifestPlanParams struct {
	Manifest interface{} `json:"manifest"` // string (YAML/JSON) or object
}

// handleManifestPlan implements the meridian_manifest_plan handler logic.
func handleManifestPlan(ctx context.Context, client ManifestApplier, sess PlanStore, params json.RawMessage) (interface{}, error) {
	var p manifestPlanParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	manifest, errResp := parseAndConvertManifest(p.Manifest)
	if errResp != nil {
		return errResp, nil
	}

	resp, err := client.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  manifest,
		DryRun:    true,
		AppliedBy: "mcp-server-plan",
	})
	if err != nil {
		formatted := mcperrors.FormatGRPCError(err)
		return map[string]interface{}{
			"valid":  false,
			"errors": formatValidationErrors(formatted.Errors),
		}, nil
	}

	if resp.Status == controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED {
		return map[string]interface{}{
			"valid":  false,
			"errors": formatProtoValidationErrors(resp.ValidationErrors),
		}, nil
	}

	// Store canonical manifest bytes so semantically equivalent JSON hashes identically.
	canonicalBytes, err := canonicalManifestBytes(manifest)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}
	planHash := sess.StorePlan(canonicalBytes)

	result := map[string]interface{}{
		"valid":     true,
		"plan_hash": planHash,
		"status":    resp.Status.String(),
	}
	if resp.DiffSummary != "" {
		result["diff_summary"] = resp.DiffSummary
	}
	if len(resp.StepResults) > 0 {
		result["steps"] = formatStepResults(resp.StepResults)
	}
	return result, nil
}

// buildManifestApplyTool returns the meridian_manifest_apply tool.
func buildManifestApplyTool(client ManifestApplier, sess PlanStore) Tool {
	return Tool{
		Name:     "meridian_manifest_apply",
		Category: CategoryWrite,
		Description: "Apply a manifest that has been previously planned. " +
			"Requires a valid plan_hash from meridian_manifest_plan. " +
			"This enforces the plan-before-apply workflow for safety.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"manifest": map[string]interface{}{
					"oneOf": []interface{}{
						map[string]interface{}{"type": "object"},
						map[string]interface{}{"type": "string"},
					},
					"description": "The manifest to apply (must match the planned manifest), as a YAML/JSON string or a JSON object.",
				},
				"plan_hash": map[string]interface{}{
					"type":        "string",
					"description": "The plan hash returned by meridian_manifest_plan.",
				},
				"applied_by": map[string]interface{}{
					"type":        "string",
					"description": "Identifier of who is applying this manifest (e.g., user email).",
				},
			},
			"required": []interface{}{"manifest", "plan_hash", "applied_by"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleManifestApply(ctx, client, sess, params)
		},
	}
}

// manifestApplyParams holds parsed parameters for meridian_manifest_apply.
type manifestApplyParams struct {
	Manifest  interface{} `json:"manifest"` // string (YAML/JSON) or object
	PlanHash  string      `json:"plan_hash"`
	AppliedBy string      `json:"applied_by"`
}

// handleManifestApply implements the meridian_manifest_apply handler logic.
func handleManifestApply(ctx context.Context, client ManifestApplier, sess PlanStore, params json.RawMessage) (interface{}, error) {
	var p manifestApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	// Enforce plan-before-apply: the plan hash must exist in the session cache.
	if !sess.ValidatePlan(p.PlanHash) {
		return map[string]interface{}{
			"error":   "no valid plan found for this manifest",
			"message": "You must run meridian_manifest_plan first and provide the returned plan_hash.",
		}, nil
	}

	manifest, errResp := parseAndConvertManifest(p.Manifest)
	if errResp != nil {
		return errResp, nil
	}

	// Verify manifest content matches the plan by comparing canonical proto bytes.
	// This is whitespace/key-order agnostic since we canonicalize via deterministic
	// proto marshaling before hashing.
	canonicalBytes, err := canonicalManifestBytes(manifest)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}
	contentHash := sha256Hex(canonicalBytes)
	if contentHash != p.PlanHash {
		return map[string]interface{}{
			"error":   "manifest content does not match the planned manifest",
			"message": "The manifest provided to apply differs from the one used during plan. Re-run meridian_manifest_plan with the updated manifest.",
		}, nil
	}

	resp, err := client.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  manifest,
		DryRun:    false,
		AppliedBy: p.AppliedBy,
	})
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	result := map[string]interface{}{
		"status": resp.Status.String(),
	}
	if resp.JobId != "" {
		result["job_id"] = resp.JobId
	}
	if resp.DiffSummary != "" {
		result["diff_summary"] = resp.DiffSummary
	}
	if len(resp.StepResults) > 0 {
		result["steps"] = formatStepResults(resp.StepResults)
	}
	if resp.Snapshot != nil {
		result["snapshot"] = formatManifestVersion(resp.Snapshot)
	}
	if resp.Status == controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED {
		result["validation_errors"] = formatProtoValidationErrors(resp.ValidationErrors)
	}
	return result, nil
}

// formatProtoValidationErrors formats proto ValidationError messages for tool responses.
func formatProtoValidationErrors(errs []*controlplanev1.ValidationError) []interface{} {
	result := make([]interface{}, 0, len(errs))
	for _, e := range errs {
		entry := map[string]interface{}{
			"type":    mcperrors.TypeManifestValidation,
			"message": e.Message,
		}
		if e.Path != "" {
			entry["path"] = e.Path
		}
		if e.Code != "" {
			entry["code"] = e.Code
		}
		if e.Severity != "" {
			entry["severity"] = e.Severity
		}
		if e.Suggestion != "" {
			entry["suggestion"] = e.Suggestion
		}
		result = append(result, entry)
	}
	return result
}

// formatValidationErrors converts mcperrors.ErrorDetail into tool-response-compatible format.
func formatValidationErrors(details []mcperrors.ErrorDetail) []interface{} {
	return formatErrorDetails(details)
}

// formatStepResults formats a slice of StepResult for LLM consumption.
func formatStepResults(steps []*controlplanev1.StepResult) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(steps))
	for _, s := range steps {
		entry := map[string]interface{}{
			"step_name": s.StepName,
			"status":    s.Status.String(),
		}
		if s.Message != "" {
			entry["message"] = s.Message
		}
		if len(s.Details) > 0 {
			entry["details"] = s.Details
		}
		result = append(result, entry)
	}
	return result
}

// formatManifestVersion formats a ManifestVersion for LLM consumption.
func formatManifestVersion(v *controlplanev1.ManifestVersion) map[string]interface{} {
	if v == nil {
		return nil
	}
	entry := map[string]interface{}{
		"id":           v.Id,
		"version":      v.Version,
		"apply_status": v.ApplyStatus.String(),
		"applied_by":   v.AppliedBy,
	}
	if v.AppliedAt != nil {
		entry["applied_at"] = v.AppliedAt.AsTime().Format(timeFmt)
	}
	if v.CreatedAt != nil {
		entry["created_at"] = v.CreatedAt.AsTime().Format(timeFmt)
	}
	if v.ApplyJobId != nil {
		entry["apply_job_id"] = *v.ApplyJobId
	}
	if v.DiffSummary != nil {
		entry["diff_summary"] = *v.DiffSummary
	}
	return entry
}

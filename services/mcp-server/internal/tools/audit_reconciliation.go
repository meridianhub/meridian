// Package tools provides reconciliation and saga-execution audit tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

// validSagaStatuses maps the accepted status string values to proto enum values.
var validSagaStatuses = map[string]sagav1.SagaStatus{
	"ACTIVE":     sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
	"DRAFT":      sagav1.SagaStatus_SAGA_STATUS_DRAFT,
	"DEPRECATED": sagav1.SagaStatus_SAGA_STATUS_DEPRECATED,
}

// buildSagaExecutionsTool returns the meridian_saga_executions tool.
func buildSagaExecutionsTool(client SagaExecutionQuerier) Tool {
	return Tool{
		Name:     "meridian_saga_executions",
		Category: CategoryRead,
		Description: "Query saga definitions and their execution status. " +
			"Returns saga definitions filtered by status. " +
			"Use this to inspect available workflows, their Starlark scripts, and lifecycle states.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Filter by saga status. One of: ACTIVE, DRAFT, DEPRECATED.",
					"enum":        []interface{}{"ACTIVE", "DRAFT", "DEPRECATED"},
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (default 25, max 100).",
					"minimum":     1,
					"maximum":     100,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleSagaExecutions(ctx, client, params)
		},
	}
}

// handleSagaExecutions implements the meridian_saga_executions handler logic.
func handleSagaExecutions(ctx context.Context, client SagaExecutionQuerier, params json.RawMessage) (interface{}, error) {
	var p struct {
		Status   string `json:"status"`
		PageSize int32  `json:"page_size"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	req := &sagav1.ListSagasRequest{}
	if p.Status != "" {
		statusVal, ok := validSagaStatuses[strings.ToUpper(p.Status)]
		if !ok {
			return map[string]interface{}{
				"error": fmt.Sprintf("invalid status %q: must be one of ACTIVE, DRAFT, DEPRECATED", p.Status),
			}, nil
		}
		req.StatusFilter = statusVal
	}
	if p.PageSize > 0 {
		req.PageSize = p.PageSize
	}

	resp, err := client.ListSagas(ctx, req)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if len(resp.Sagas) == 0 {
		return map[string]interface{}{
			"message": "no saga definitions found matching the query",
			"sagas":   []interface{}{},
		}, nil
	}

	sagas := make([]map[string]interface{}, 0, len(resp.Sagas))
	for _, saga := range resp.Sagas {
		sagas = append(sagas, formatSagaDefinition(saga))
	}

	return map[string]interface{}{
		"count": len(sagas),
		"sagas": sagas,
	}, nil
}

// formatSagaDefinition formats a single SagaDefinition for LLM consumption.
func formatSagaDefinition(saga *sagav1.SagaDefinition) map[string]interface{} {
	entry := map[string]interface{}{
		"id":           saga.Id,
		"name":         saga.Name,
		"display_name": saga.DisplayName,
		"description":  saga.Description,
		"version":      saga.Version,
		"status":       saga.Status.String(),
		"is_system":    saga.IsSystem,
	}
	if saga.CreatedAt != nil {
		entry["created_at"] = saga.CreatedAt.AsTime().Format(time.RFC3339)
	}
	if saga.ActivatedAt != nil {
		entry["activated_at"] = saga.ActivatedAt.AsTime().Format(time.RFC3339)
	}
	if saga.DeprecatedAt != nil {
		entry["deprecated_at"] = saga.DeprecatedAt.AsTime().Format(time.RFC3339)
	}
	if saga.PreconditionsExpression != "" {
		entry["preconditions_expression"] = saga.PreconditionsExpression
	}
	return entry
}

// buildReconciliationStatusTool returns the meridian_reconciliation_status tool.
func buildReconciliationStatusTool(client ReconciliationQuerier) Tool {
	return Tool{
		Name:     "meridian_reconciliation_status",
		Category: CategoryRead,
		Description: "Query reconciliation cycle status and variance mismatches. " +
			"Lists settlement runs with optional account and status filters. " +
			"When a run_id is provided, also fetches detailed variance results. " +
			"Use this to inspect reconciliation health and investigate discrepancies.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"account_id": map[string]interface{}{
					"type":        "string",
					"description": "Filter settlement runs by account ID (optional).",
				},
				"run_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of a specific settlement run to fetch variances for (optional).",
					"pattern":     `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				},
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Filter by run status. One of: PENDING, RUNNING, COMPLETED, FAILED, CANCELLED, PAUSED.",
					"enum":        []interface{}{"PENDING", "RUNNING", "COMPLETED", "FAILED", "CANCELLED", "PAUSED"},
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of runs to return (default 25, max 100).",
					"minimum":     1,
					"maximum":     100,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleReconciliationStatus(ctx, client, params)
		},
	}
}

// reconciliationStatusParams holds parsed parameters for meridian_reconciliation_status.
type reconciliationStatusParams struct {
	AccountID string `json:"account_id"`
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	PageSize  int32  `json:"page_size"`
}

// handleReconciliationStatus implements the meridian_reconciliation_status handler logic.
func handleReconciliationStatus(ctx context.Context, client ReconciliationQuerier, params json.RawMessage) (interface{}, error) {
	var p reconciliationStatusParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	listReq, errResult := buildReconciliationRequest(p)
	if errResult != nil {
		return errResult, nil
	}

	runsResp, err := client.ListAccountReconciliations(ctx, listReq)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	runs := make([]map[string]interface{}, 0, len(runsResp.Runs))
	for _, run := range runsResp.Runs {
		runs = append(runs, formatSettlementRun(run))
	}

	result := map[string]interface{}{
		"count": len(runs),
		"runs":  runs,
	}

	if p.RunID != "" {
		appendVariances(ctx, client, p.RunID, result)
	}

	if len(runs) == 0 && p.RunID == "" {
		result["message"] = "no reconciliation runs found matching the query"
	}

	return result, nil
}

// buildReconciliationRequest constructs the ListAccountReconciliationsRequest from parsed params.
// Returns the request and nil on success, or nil and an error result map on validation failure.
func buildReconciliationRequest(p reconciliationStatusParams) (*reconciliationv1.ListAccountReconciliationsRequest, interface{}) {
	req := &reconciliationv1.ListAccountReconciliationsRequest{}
	if p.AccountID != "" {
		req.AccountId = p.AccountID
	}
	if p.PageSize > 0 {
		req.PageSize = p.PageSize
	}
	if p.Status != "" {
		statusVal := reconciliationRunStatus(p.Status)
		if statusVal == reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED {
			return nil, map[string]interface{}{
				"error": fmt.Sprintf("invalid status %q: must be one of PENDING, RUNNING, COMPLETED, FAILED, CANCELLED, PAUSED", p.Status),
			}
		}
		req.Status = statusVal
	}
	return req, nil
}

// appendVariances fetches variance details for a run and adds them to result.
func appendVariances(ctx context.Context, client ReconciliationQuerier, runID string, result map[string]interface{}) {
	varResp, err := client.ListReconciliationResults(ctx, &reconciliationv1.ListReconciliationResultsRequest{
		RunId: runID,
	})
	if err != nil {
		result["variances_error"] = mcperrors.FormatGRPCError(err)
		return
	}
	variances := make([]map[string]interface{}, 0, len(varResp.Variances))
	for _, v := range varResp.Variances {
		variances = append(variances, formatVariance(v))
	}
	result["variances"] = variances
	result["variance_count"] = len(variances)
}

// reconciliationRunStatus maps status string to proto enum value.
func reconciliationRunStatus(s string) reconciliationv1.RunStatus {
	switch strings.ToUpper(s) {
	case "PENDING":
		return reconciliationv1.RunStatus_RUN_STATUS_PENDING
	case "RUNNING":
		return reconciliationv1.RunStatus_RUN_STATUS_RUNNING
	case "COMPLETED":
		return reconciliationv1.RunStatus_RUN_STATUS_COMPLETED
	case "FAILED":
		return reconciliationv1.RunStatus_RUN_STATUS_FAILED
	case "CANCELLED":
		return reconciliationv1.RunStatus_RUN_STATUS_CANCELLED
	case "PAUSED":
		return reconciliationv1.RunStatus_RUN_STATUS_PAUSED
	default:
		return reconciliationv1.RunStatus_RUN_STATUS_UNSPECIFIED
	}
}

// formatSettlementRun formats a SettlementRunSummary for LLM consumption.
func formatSettlementRun(run *reconciliationv1.SettlementRunSummary) map[string]interface{} {
	if run == nil {
		return nil
	}
	entry := map[string]interface{}{
		"run_id":          run.RunId,
		"account_id":      run.AccountId,
		"scope":           run.Scope.String(),
		"settlement_type": run.SettlementType.String(),
		"status":          run.Status.String(),
		"initiated_by":    run.InitiatedBy,
		"variance_count":  run.VarianceCount,
		"total_variance":  run.TotalVariance,
		"snapshot_count":  run.SnapshotCount,
	}
	if run.PeriodStart != nil {
		entry["period_start"] = run.PeriodStart.AsTime().Format(time.RFC3339)
	}
	if run.PeriodEnd != nil {
		entry["period_end"] = run.PeriodEnd.AsTime().Format(time.RFC3339)
	}
	if run.CompletedAt != nil {
		entry["completed_at"] = run.CompletedAt.AsTime().Format(time.RFC3339)
	}
	if run.FailureReason != "" {
		entry["failure_reason"] = run.FailureReason
	}
	if run.CreatedAt != nil {
		entry["created_at"] = run.CreatedAt.AsTime().Format(time.RFC3339)
	}
	return entry
}

// formatVariance formats a VarianceDetail for LLM consumption.
func formatVariance(v *reconciliationv1.VarianceDetail) map[string]interface{} {
	if v == nil {
		return nil
	}
	entry := map[string]interface{}{
		"variance_id":     v.VarianceId,
		"run_id":          v.RunId,
		"account_id":      v.AccountId,
		"instrument_code": v.InstrumentCode,
		"expected_amount": v.ExpectedAmount,
		"actual_amount":   v.ActualAmount,
		"variance_amount": v.VarianceAmount,
		"reason":          v.Reason.String(),
		"status":          v.Status.String(),
	}
	if v.ResolutionNote != "" {
		entry["resolution_note"] = v.ResolutionNote
	}
	if v.ResolvedBy != "" {
		entry["resolved_by"] = v.ResolvedBy
	}
	if v.ResolvedAt != nil {
		entry["resolved_at"] = v.ResolvedAt.AsTime().Format(time.RFC3339)
	}
	if v.CreatedAt != nil {
		entry["created_at"] = v.CreatedAt.AsTime().Format(time.RFC3339)
	}
	return entry
}

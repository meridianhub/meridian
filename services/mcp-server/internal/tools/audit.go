// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

// SagaAdminQuerier is the minimal interface for fetching causation trees.
type SagaAdminQuerier interface {
	GetCausationTree(ctx context.Context, req *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error)
}

// PositionQuerier is the minimal interface for fetching position logs.
type PositionQuerier interface {
	ListFinancialPositionLogs(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error)
}

// PostingQuerier is the minimal interface for fetching ledger postings.
type PostingQuerier interface {
	ListLedgerPostings(ctx context.Context, req *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error)
}

// SagaExecutionQuerier is the minimal interface for fetching saga definitions/executions.
type SagaExecutionQuerier interface {
	ListSagas(ctx context.Context, req *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error)
}

// ReconciliationQuerier is the minimal interface for fetching reconciliation status.
type ReconciliationQuerier interface {
	ListAccountReconciliations(ctx context.Context, req *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error)
	ListReconciliationResults(ctx context.Context, req *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error)
}

// AuditClients groups all gRPC client dependencies for audit tools.
type AuditClients struct {
	SagaAdmin           SagaAdminQuerier
	PositionKeeping     PositionQuerier
	FinancialAccounting PostingQuerier
	SagaRegistry        SagaExecutionQuerier
	Reconciliation      ReconciliationQuerier
}

// RegisterAuditTools registers all read-only audit tools onto the SDK server.
// Tools whose required client is nil are silently skipped.
func RegisterAuditTools(srv *mcp.Server, clients AuditClients) {
	candidates := []Tool{}
	if clients.SagaAdmin != nil {
		candidates = append(candidates, buildCausationTreeTool(clients.SagaAdmin))
	}
	if clients.PositionKeeping != nil {
		candidates = append(candidates, buildPositionsQueryTool(clients.PositionKeeping))
	}
	if clients.FinancialAccounting != nil {
		candidates = append(candidates, buildPostingsQueryTool(clients.FinancialAccounting))
	}
	if clients.SagaRegistry != nil {
		candidates = append(candidates, buildSagaExecutionsTool(clients.SagaRegistry))
	}
	if clients.Reconciliation != nil {
		candidates = append(candidates, buildReconciliationStatusTool(clients.Reconciliation))
	}
	for _, t := range candidates {
		addTool(srv, t)
	}
}

// buildCausationTreeTool returns the meridian_causation_tree tool.
func buildCausationTreeTool(client SagaAdminQuerier) Tool {
	return Tool{
		Name:     "meridian_causation_tree",
		Category: CategoryRead,
		Description: "Fetch the full parent→child saga causation tree for a given root saga ID. " +
			"Returns the complete execution hierarchy including step status and failure details. " +
			"Use this to debug complex nested saga failures.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"saga_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of the root saga instance to fetch the causation tree for.",
					"pattern":     `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				},
			},
			"required": []interface{}{"saga_id"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			var p struct {
				SagaID string `json:"saga_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return mcperrors.FormatGRPCError(err), nil
			}

			resp, err := client.GetCausationTree(ctx, &sagav1.GetCausationTreeRequest{
				SagaId: p.SagaID,
			})
			if err != nil {
				return mcperrors.FormatGRPCError(err), nil
			}

			if resp.Tree == nil {
				return map[string]interface{}{
					"message": fmt.Sprintf("no causation tree found for saga %s", p.SagaID),
					"depth":   0,
				}, nil
			}

			return map[string]interface{}{
				"saga_id": p.SagaID,
				"depth":   resp.Depth,
				"tree":    formatCausationNode(resp.Tree),
			}, nil
		},
	}
}

// formatCausationNode formats a CausationTreeNode for LLM consumption.
func formatCausationNode(node *sagav1.CausationTreeNode) map[string]interface{} {
	if node == nil {
		return nil
	}

	steps := make([]map[string]interface{}, 0, len(node.Steps))
	for _, s := range node.Steps {
		step := map[string]interface{}{
			"index":  s.Index,
			"name":   s.Name,
			"status": s.Status,
		}
		if s.ExecutedAt != nil {
			step["executed_at"] = s.ExecutedAt.AsTime().Format(time.RFC3339)
		}
		if s.Error != "" {
			step["error"] = s.Error
		}
		if len(s.ChildSagas) > 0 {
			children := make([]map[string]interface{}, 0, len(s.ChildSagas))
			for _, child := range s.ChildSagas {
				children = append(children, formatCausationNode(child))
			}
			step["child_sagas"] = children
		}
		steps = append(steps, step)
	}

	result := map[string]interface{}{
		"saga_id":   node.SagaId,
		"saga_name": node.SagaName,
		"status":    node.Status,
		"steps":     steps,
	}

	if node.EffectiveAt != nil {
		result["effective_at"] = node.EffectiveAt.AsTime().Format(time.RFC3339)
	}
	if node.KnowledgeAt != nil {
		result["knowledge_at"] = node.KnowledgeAt.AsTime().Format(time.RFC3339)
	}
	if node.FailedStep != nil {
		result["failed_step"] = map[string]interface{}{
			"index":          node.FailedStep.Index,
			"error":          node.FailedStep.Error,
			"error_category": node.FailedStep.ErrorCategory,
		}
	}

	return result
}

// buildPositionsQueryTool returns the meridian_positions_query tool.
func buildPositionsQueryTool(client PositionQuerier) Tool {
	return Tool{
		Name:     "meridian_positions_query",
		Category: CategoryRead,
		Description: "Query financial position logs with optional account filtering. " +
			"Returns position log summaries for one or all accounts. " +
			"Use this to inspect account positions, transaction histories, and status tracking.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"account_id": map[string]interface{}{
					"type":        "string",
					"description": "Filter by account identifier (optional).",
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
			return handlePositionsQuery(ctx, client, params)
		},
	}
}

// handlePositionsQuery implements the meridian_positions_query handler logic.
func handlePositionsQuery(ctx context.Context, client PositionQuerier, params json.RawMessage) (interface{}, error) {
	var p struct {
		AccountID string `json:"account_id"`
		PageSize  int32  `json:"page_size"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	req := &positionkeepingv1.ListFinancialPositionLogsRequest{}
	if p.AccountID != "" {
		req.AccountId = p.AccountID
	}
	if p.PageSize > 0 {
		req.Pagination = &commonv1.Pagination{PageSize: p.PageSize}
	}

	resp, err := client.ListFinancialPositionLogs(ctx, req)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if len(resp.Logs) == 0 {
		return map[string]interface{}{
			"message": "no position logs found matching the query",
			"logs":    []interface{}{},
		}, nil
	}

	logs := make([]map[string]interface{}, 0, len(resp.Logs))
	for _, log := range resp.Logs {
		entry := map[string]interface{}{
			"log_id":     log.LogId,
			"account_id": log.AccountId,
			"version":    log.Version,
		}
		if log.StatusTracking != nil {
			entry["status"] = log.StatusTracking.CurrentStatus.String()
		}
		if log.CreatedAt != nil {
			entry["created_at"] = log.CreatedAt.AsTime().Format(time.RFC3339)
		}
		if log.UpdatedAt != nil {
			entry["updated_at"] = log.UpdatedAt.AsTime().Format(time.RFC3339)
		}
		entry["transaction_count"] = len(log.TransactionLogEntries)
		logs = append(logs, entry)
	}

	return map[string]interface{}{
		"count": len(logs),
		"logs":  logs,
	}, nil
}

// buildPostingsQueryTool returns the meridian_postings_query tool.
func buildPostingsQueryTool(client PostingQuerier) Tool {
	return Tool{
		Name:     "meridian_postings_query",
		Category: CategoryRead,
		Description: "Query ledger postings with optional date range and account filtering. " +
			"Returns double-entry bookkeeping records with debit/credit direction and amounts. " +
			"Use this to inspect financial postings for audit and reconciliation purposes.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"account_id": map[string]interface{}{
					"type":        "string",
					"description": "Filter postings by account identifier (optional).",
				},
				"booking_log_id": map[string]interface{}{
					"type":        "string",
					"description": "Filter postings by financial booking log ID (optional).",
				},
				"date_from": map[string]interface{}{
					"type":        "string",
					"format":      "date-time",
					"description": "Filter postings with value_date on or after this ISO 8601 timestamp (optional).",
				},
				"date_to": map[string]interface{}{
					"type":        "string",
					"format":      "date-time",
					"description": "Filter postings with value_date on or before this ISO 8601 timestamp (optional).",
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
			return handlePostingsQuery(ctx, client, params)
		},
	}
}

// postingsQueryParams holds parsed parameters for meridian_postings_query.
type postingsQueryParams struct {
	AccountID    string `json:"account_id"`
	BookingLogID string `json:"booking_log_id"`
	DateFrom     string `json:"date_from"`
	DateTo       string `json:"date_to"`
	PageSize     int32  `json:"page_size"`
}

// handlePostingsQuery implements the meridian_postings_query handler logic.
func handlePostingsQuery(ctx context.Context, client PostingQuerier, params json.RawMessage) (interface{}, error) {
	var p postingsQueryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	req, validationErr := buildPostingsRequest(p)
	if validationErr != "" {
		return map[string]interface{}{"error": validationErr}, nil
	}

	resp, err := client.ListLedgerPostings(ctx, req)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if len(resp.LedgerPostings) == 0 {
		return map[string]interface{}{
			"message":  "no postings found matching the query",
			"postings": []interface{}{},
		}, nil
	}

	postings := make([]map[string]interface{}, 0, len(resp.LedgerPostings))
	for _, posting := range resp.LedgerPostings {
		postings = append(postings, formatPosting(posting))
	}

	return map[string]interface{}{
		"count":    len(postings),
		"postings": postings,
	}, nil
}

// buildPostingsRequest constructs the gRPC request from parsed params.
func buildPostingsRequest(p postingsQueryParams) (*financialaccountingv1.ListLedgerPostingsRequest, string) {
	req := &financialaccountingv1.ListLedgerPostingsRequest{}
	if p.AccountID != "" {
		req.AccountId = p.AccountID
	}
	if p.BookingLogID != "" {
		req.FinancialBookingLogId = p.BookingLogID
	}
	if p.PageSize > 0 {
		req.Pagination = &commonv1.Pagination{PageSize: p.PageSize}
	}
	var dateFrom, dateTo time.Time
	if p.DateFrom != "" {
		t, err := time.Parse(time.RFC3339, p.DateFrom)
		if err != nil {
			return nil, fmt.Sprintf("invalid date_from format (expected RFC3339): %v", err)
		}
		dateFrom = t
		req.ValueDateFrom = timestamppb.New(t)
	}
	if p.DateTo != "" {
		t, err := time.Parse(time.RFC3339, p.DateTo)
		if err != nil {
			return nil, fmt.Sprintf("invalid date_to format (expected RFC3339): %v", err)
		}
		dateTo = t
		req.ValueDateTo = timestamppb.New(t)
	}
	if !dateFrom.IsZero() && !dateTo.IsZero() && dateFrom.After(dateTo) {
		return nil, "date_from must be before or equal to date_to"
	}
	return req, ""
}

// formatPosting formats a single LedgerPosting for LLM consumption.
func formatPosting(posting *financialaccountingv1.LedgerPosting) map[string]interface{} {
	entry := map[string]interface{}{
		"id":             posting.Id,
		"booking_log_id": posting.FinancialBookingLogId,
		"account_id":     posting.AccountId,
		"direction":      posting.PostingDirection.String(),
		"status":         posting.Status.String(),
		"posting_result": posting.PostingResult,
	}
	if posting.PostingAmount != nil {
		entry["amount"] = map[string]interface{}{
			"amount":          posting.PostingAmount.Amount,
			"instrument_code": posting.PostingAmount.InstrumentCode,
			"version":         posting.PostingAmount.Version,
		}
	}
	if posting.ValueDate != nil {
		entry["value_date"] = posting.ValueDate.AsTime().Format(time.RFC3339)
	}
	if posting.CreatedAt != nil {
		entry["created_at"] = posting.CreatedAt.AsTime().Format(time.RFC3339)
	}
	return entry
}

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

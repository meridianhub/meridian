// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

// PlanStore abstracts the session plan cache to avoid an import cycle
// between tools and session packages. The session.Session type satisfies
// this interface.
type PlanStore interface {
	// StorePlan hashes the manifest bytes and stores the result. Returns the hash.
	StorePlan(manifest []byte) string
	// ValidatePlan returns true when a plan with the given hash exists and has not expired.
	ValidatePlan(hash string) bool
}

// ManifestApplier is the minimal interface for validating, planning, and applying manifests.
type ManifestApplier interface {
	ApplyManifest(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error)
}

// ManifestHistorian is the minimal interface for querying manifest version history.
type ManifestHistorian interface {
	ListManifestVersions(ctx context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error)
}

// EconomyDeps holds all service clients used by economy design tools.
type EconomyDeps struct {
	Applier   ManifestApplier
	Historian ManifestHistorian
}

// RegisterEconomyTools registers the manifest lifecycle tools into the registry.
// Tools whose required client is nil are silently skipped.
func RegisterEconomyTools(registry *Registry, sess PlanStore, deps EconomyDeps) {
	var candidates []Tool

	if deps.Applier != nil {
		candidates = append(candidates, buildManifestValidateTool(deps.Applier))
		if sess != nil {
			candidates = append(candidates, buildManifestPlanTool(deps.Applier, sess))
			candidates = append(candidates, buildManifestApplyTool(deps.Applier, sess))
		}
	}
	if deps.Historian != nil {
		candidates = append(candidates, buildManifestHistoryTool(deps.Historian))
	}
	if deps.Historian != nil {
		candidates = append(candidates, buildEconomyGraphTool(deps.Historian))
	}

	for _, t := range candidates {
		if err := registry.Register(t); err != nil {
			panic(fmt.Sprintf("failed to register economy tool %q: %v", t.Name, err))
		}
	}
}

// manifestJSONToProto converts a JSON manifest object into a controlplanev1.Manifest proto.
func manifestJSONToProto(manifestJSON json.RawMessage) (*controlplanev1.Manifest, error) {
	m := &controlplanev1.Manifest{}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(manifestJSON, m); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}
	return m, nil
}

// buildManifestValidateTool returns the meridian_manifest_validate tool.
func buildManifestValidateTool(client ManifestApplier) Tool {
	return Tool{
		Name:     "meridian_manifest_validate",
		Category: CategorySimulate,
		Description: "Validate a manifest YAML/JSON without applying it. " +
			"Runs structural validation and returns any errors with paths and suggestions. " +
			"Use this to check a manifest before planning or applying.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"manifest": map[string]interface{}{
					"type":        "object",
					"description": "The manifest JSON object to validate.",
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
	Manifest json.RawMessage `json:"manifest"`
}

// handleManifestValidate implements the meridian_manifest_validate handler logic.
func handleManifestValidate(ctx context.Context, client ManifestApplier, params json.RawMessage) (interface{}, error) {
	var p manifestValidateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	manifest, err := manifestJSONToProto(p.Manifest)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // err is surfaced in the tool response
			"valid":  false,
			"errors": []interface{}{map[string]interface{}{"type": mcperrors.TypeManifestValidation, "message": err.Error()}},
		}, nil
	}

	resp, err := client.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  manifest,
		DryRun:    true,
		AppliedBy: "mcp-server-validate",
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
					"type":        "object",
					"description": "The manifest JSON object to plan.",
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
	Manifest json.RawMessage `json:"manifest"`
}

// handleManifestPlan implements the meridian_manifest_plan handler logic.
func handleManifestPlan(ctx context.Context, client ManifestApplier, sess PlanStore, params json.RawMessage) (interface{}, error) {
	var p manifestPlanParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	manifest, err := manifestJSONToProto(p.Manifest)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // err is surfaced in the tool response
			"valid":  false,
			"errors": []interface{}{map[string]interface{}{"type": mcperrors.TypeManifestValidation, "message": err.Error()}},
		}, nil
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
					"type":        "object",
					"description": "The manifest JSON object to apply (must match the planned manifest).",
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
	Manifest  json.RawMessage `json:"manifest"`
	PlanHash  string          `json:"plan_hash"`
	AppliedBy string          `json:"applied_by"`
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

	manifest, err := manifestJSONToProto(p.Manifest)
	if err != nil {
		return map[string]interface{}{ //nolint:nilerr // err is surfaced in the tool response
			"valid":  false,
			"errors": []interface{}{map[string]interface{}{"type": mcperrors.TypeManifestValidation, "message": err.Error()}},
		}, nil
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

// buildManifestHistoryTool returns the meridian_manifest_history tool.
func buildManifestHistoryTool(client ManifestHistorian) Tool {
	return Tool{
		Name:     "meridian_manifest_history",
		Category: CategoryRead,
		Description: "Query manifest version history for the current tenant. " +
			"Returns a paginated list of manifest versions with apply status and timestamps. " +
			"Use this to review the change history of a tenant's economy configuration.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of versions to return (default 20, max 100).",
					"minimum":     1,
					"maximum":     100,
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "Number of versions to skip for pagination.",
					"minimum":     0,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleManifestHistory(ctx, client, params)
		},
	}
}

// manifestHistoryParams holds parsed parameters for meridian_manifest_history.
type manifestHistoryParams struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

// handleManifestHistory implements the meridian_manifest_history handler logic.
func handleManifestHistory(ctx context.Context, client ManifestHistorian, params json.RawMessage) (interface{}, error) {
	var p manifestHistoryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	req := &controlplanev1.ListManifestVersionsRequest{}
	if p.Limit > 0 {
		req.Limit = p.Limit
	}
	if p.Offset > 0 {
		req.Offset = p.Offset
	}

	resp, err := client.ListManifestVersions(ctx, req)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if len(resp.Versions) == 0 {
		return map[string]interface{}{
			"message":     "no manifest versions found",
			"versions":    []interface{}{},
			"total_count": resp.TotalCount,
		}, nil
	}

	versions := make([]map[string]interface{}, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		versions = append(versions, formatManifestVersion(v))
	}

	return map[string]interface{}{
		"count":       len(versions),
		"total_count": resp.TotalCount,
		"versions":    versions,
	}, nil
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
		entry["applied_at"] = v.AppliedAt.AsTime().Format(time.RFC3339)
	}
	if v.CreatedAt != nil {
		entry["created_at"] = v.CreatedAt.AsTime().Format(time.RFC3339)
	}
	if v.ApplyJobId != nil {
		entry["apply_job_id"] = *v.ApplyJobId
	}
	if v.DiffSummary != nil {
		entry["diff_summary"] = *v.DiffSummary
	}
	return entry
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

// canonicalManifestBytes returns deterministic proto-encoded bytes for a manifest.
// This ensures semantically equivalent manifests (differing only in JSON key
// order or whitespace) produce identical hashes.
func canonicalManifestBytes(m *controlplanev1.Manifest) ([]byte, error) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize manifest: %w", err)
	}
	return b, nil
}

// sha256Hex returns the hex-encoded SHA256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// buildEconomyGraphTool returns the meridian_economy_graph tool.
// It returns the full relationship graph (including handler call edges) stored during
// manifest validation. Falls back to structural-only extraction if no stored graph exists.
func buildEconomyGraphTool(historian ManifestHistorian) Tool {
	return Tool{
		Name:     "meridian_economy_graph",
		Category: CategoryRead,
		Description: "Query the relationship graph between manifest resources for impact analysis. " +
			"Returns nodes (sagas, handlers, instruments, account_types) and edges " +
			"(calls_handler, uses_instrument, reads_from, writes_to, denominated_in, converts, triggers_on). " +
			"Use node_id to get impact analysis showing what resources would be affected by removing a node.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"node_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional. Provide a node ID (e.g., 'instrument:GBP', 'handler:position_keeping.initiate_log') to get impact analysis for removing that node.",
				},
				"node_type": map[string]interface{}{
					"type":        "string",
					"description": "Optional. Filter nodes by type.",
					"enum":        []interface{}{"saga", "handler", "instrument", "account_type"},
				},
				"relationship": map[string]interface{}{
					"type":        "string",
					"description": "Optional. Filter edges by relationship type.",
					"enum":        []interface{}{"calls_handler", "uses_instrument", "reads_from", "writes_to", "denominated_in", "converts", "triggers_on"},
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleEconomyGraph(ctx, historian, params)
		},
	}
}

// economyGraphParams holds parsed parameters for meridian_economy_graph.
type economyGraphParams struct {
	NodeID       string `json:"node_id"`
	NodeType     string `json:"node_type"`
	Relationship string `json:"relationship"`
}

// graphNode is the serialization format for graph nodes in tool responses.
type graphNode struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Name     string            `json:"name"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// graphEdge is the serialization format for graph edges in tool responses.
type graphEdge struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	Relationship string `json:"relationship"`
	IsDynamic    bool   `json:"is_dynamic,omitempty"`
	Location     string `json:"location,omitempty"`
}

// storedGraph is the deserialization target for the JSONB relationship graph stored with manifest versions.
type storedGraph struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

// handleEconomyGraph retrieves the relationship graph from the stored manifest version.
// Prefers the full graph (including handler edges) stored during validation.
// Falls back to structural-only extraction if no stored graph is available.
func handleEconomyGraph(ctx context.Context, historian ManifestHistorian, params json.RawMessage) (interface{}, error) {
	var p economyGraphParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	// Get current manifest version via historian
	histResp, err := historian.ListManifestVersions(ctx, &controlplanev1.ListManifestVersionsRequest{Limit: 1})
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}
	if len(histResp.Versions) == 0 || histResp.Versions[0].Manifest == nil {
		return map[string]interface{}{
			"status":  "no_manifest",
			"message": "no manifest has been applied for this tenant",
		}, nil
	}

	version := histResp.Versions[0]
	allNodes, allEdges := loadGraph(version)

	filteredNodes := filterNodes(allNodes, p.NodeType)
	filteredEdges := filterEdges(allEdges, p.Relationship)

	result := map[string]interface{}{
		"node_count": len(filteredNodes),
		"edge_count": len(filteredEdges),
		"nodes":      filteredNodes,
		"edges":      filteredEdges,
	}

	if p.NodeID != "" {
		result["impact"] = computeImpact(p.NodeID, allEdges)
	}

	return result, nil
}

// loadGraph returns the full relationship graph from the stored version if available,
// falling back to structural-only extraction from the manifest.
func loadGraph(version *controlplanev1.ManifestVersion) ([]graphNode, []graphEdge) {
	if version.RelationshipGraph != nil {
		var sg storedGraph
		if err := json.Unmarshal([]byte(*version.RelationshipGraph), &sg); err == nil && len(sg.Nodes) > 0 {
			return sg.Nodes, sg.Edges
		}
	}
	return extractManifestGraph(version.Manifest)
}

// filterNodes returns only nodes matching the given type, or all nodes if nodeType is empty.
func filterNodes(nodes []graphNode, nodeType string) []graphNode {
	if nodeType == "" {
		return nodes
	}
	filtered := make([]graphNode, 0, len(nodes))
	for _, n := range nodes {
		if n.Type == nodeType {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// filterEdges returns only edges matching the given relationship, or all edges if rel is empty.
func filterEdges(edges []graphEdge, rel string) []graphEdge {
	if rel == "" {
		return edges
	}
	filtered := make([]graphEdge, 0, len(edges))
	for _, e := range edges {
		if e.Relationship == rel {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// computeImpact calculates which nodes are affected by removing the given node.
func computeImpact(nodeID string, edges []graphEdge) map[string]interface{} {
	affected := make(map[string]bool)
	edgeCount := 0
	for _, e := range edges {
		if e.Source == nodeID || e.Target == nodeID {
			edgeCount++
			if e.Source == nodeID {
				affected[e.Target] = true
			} else {
				affected[e.Source] = true
			}
		}
	}
	affectedList := make([]string, 0, len(affected))
	for n := range affected {
		affectedList = append(affectedList, n)
	}
	sort.Strings(affectedList)
	return map[string]interface{}{
		"node_id":        nodeID,
		"affected_nodes": affectedList,
		"affected_edges": edgeCount,
		"summary":        fmt.Sprintf("removing %s affects %d nodes via %d edges", nodeID, len(affectedList), edgeCount),
	}
}

// extractManifestGraph builds nodes and edges from manifest structure.
func extractManifestGraph(m *controlplanev1.Manifest) ([]graphNode, []graphEdge) {
	nodeCapacity := len(m.GetInstruments()) + len(m.GetAccountTypes()) + len(m.GetSagas())
	nodes := make([]graphNode, 0, nodeCapacity)
	edges := make([]graphEdge, 0, nodeCapacity) // rough estimate

	// Instruments
	for _, inst := range m.GetInstruments() {
		nodes = append(nodes, graphNode{
			ID:   "instrument:" + inst.GetCode(),
			Type: "instrument",
			Name: inst.GetName(),
			Metadata: map[string]string{
				"code": inst.GetCode(),
				"type": inst.GetType().String(),
			},
		})
	}

	// Account types + denominated_in edges
	for _, acct := range m.GetAccountTypes() {
		nodes = append(nodes, graphNode{
			ID:   "account_type:" + acct.GetCode(),
			Type: "account_type",
			Name: acct.GetName(),
			Metadata: map[string]string{
				"code":           acct.GetCode(),
				"normal_balance": acct.GetNormalBalance().String(),
			},
		})
		for _, instCode := range acct.GetAllowedInstruments() {
			edges = append(edges, graphEdge{
				Source:       "account_type:" + acct.GetCode(),
				Target:       "instrument:" + instCode,
				Relationship: "denominated_in",
			})
		}
	}

	// Valuation rules (converts edges)
	for _, rule := range m.GetValuationRules() {
		edges = append(edges, graphEdge{
			Source:       "instrument:" + rule.GetFromInstrument(),
			Target:       "instrument:" + rule.GetToInstrument(),
			Relationship: "converts",
		})
	}

	// Sagas + triggers_on edges
	for i, saga := range m.GetSagas() {
		sagaID := "saga:" + saga.GetName()
		nodes = append(nodes, graphNode{
			ID:   sagaID,
			Type: "saga",
			Name: saga.GetName(),
			Metadata: map[string]string{
				"trigger": saga.GetTrigger(),
			},
		})
		edges = append(edges, graphEdge{
			Source:       sagaID,
			Target:       saga.GetTrigger(),
			Relationship: "triggers_on",
			Location:     fmt.Sprintf("sagas[%d].trigger", i),
		})
	}

	return nodes, edges
}

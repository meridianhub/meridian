package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

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

	// Get current applied manifest version
	currentResp, err := historian.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if currentResp.Version == nil || currentResp.Version.Manifest == nil {
		return map[string]interface{}{
			"status":  "no_manifest",
			"message": "no manifest has been applied for this tenant",
		}, nil
	}

	version := currentResp.Version
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

// buildManifestRollbackTool returns the meridian_manifest_rollback tool.
func buildManifestRollbackTool(client ManifestHistorian) Tool {
	return Tool{
		Name:     "meridian_manifest_rollback",
		Category: CategoryWrite,
		Description: "Rollback the tenant's manifest to a previous version by sequence number. " +
			"Creates a new version record (forward-only audit trail) and re-applies the " +
			"target manifest through the standard pipeline. Use dry_run=true to preview " +
			"changes without applying. Use meridian_manifest_history to find sequence numbers.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"target_sequence_number": map[string]interface{}{
					"type":        "integer",
					"description": "The sequence number of the manifest version to rollback to. Use meridian_manifest_history to find available versions.",
					"minimum":     1,
				},
				"dry_run": map[string]interface{}{
					"type":        "boolean",
					"description": "When true, returns a diff preview without applying changes (default false).",
				},
				"applied_by": map[string]interface{}{
					"type":        "string",
					"description": "Identifier of who is performing the rollback (e.g., user email).",
				},
			},
			"required": []interface{}{"target_sequence_number", "applied_by"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleManifestRollback(ctx, client, params)
		},
	}
}

// manifestRollbackParams holds parsed parameters for meridian_manifest_rollback.
type manifestRollbackParams struct {
	TargetSequenceNumber int64  `json:"target_sequence_number"`
	DryRun               bool   `json:"dry_run"`
	AppliedBy            string `json:"applied_by"`
}

// handleManifestRollback implements the meridian_manifest_rollback handler logic.
func handleManifestRollback(ctx context.Context, client ManifestHistorian, params json.RawMessage) (interface{}, error) {
	var p manifestRollbackParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if p.TargetSequenceNumber <= 0 {
		return map[string]interface{}{
			"error":   "target_sequence_number must be greater than 0",
			"message": "Use meridian_manifest_history to find available sequence numbers.",
		}, nil
	}
	p.AppliedBy = strings.TrimSpace(p.AppliedBy)
	if p.AppliedBy == "" {
		return map[string]interface{}{
			"error":   "applied_by is required",
			"message": "Provide an identifier for who is performing the rollback.",
		}, nil
	}

	resp, err := client.RollbackManifest(ctx, &controlplanev1.RollbackManifestRequest{
		TargetSequenceNumber: p.TargetSequenceNumber,
		DryRun:               p.DryRun,
		AppliedBy:            p.AppliedBy,
	})
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	result := map[string]interface{}{
		"status":  resp.Status.String(),
		"message": resp.Message,
	}

	if resp.Version != nil {
		result["version"] = formatManifestVersion(resp.Version)
	}

	if resp.Diff != nil && resp.Diff.Summary != nil {
		result["diff_summary"] = map[string]interface{}{
			"creates":              resp.Diff.Summary.Creates,
			"updates":              resp.Diff.Summary.Updates,
			"deletes":              resp.Diff.Summary.Deletes,
			"no_changes":           resp.Diff.Summary.NoChanges,
			"has_breaking_changes": resp.Diff.Summary.HasBreakingChanges,
		}
	}

	return result, nil
}

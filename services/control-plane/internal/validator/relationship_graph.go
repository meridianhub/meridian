// Package validator provides manifest validation for the control plane.
package validator

import (
	"fmt"
	"sort"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// NodeType classifies graph nodes.
type NodeType string

// Node types for relationship graph.
const (
	NodeTypeSaga        NodeType = "saga"
	NodeTypeHandler     NodeType = "handler"
	NodeTypeInstrument  NodeType = "instrument"
	NodeTypeAccountType NodeType = "account_type"
)

// RelationshipType classifies graph edges.
type RelationshipType string

// Relationship types for graph edges.
const (
	RelCallsHandler   RelationshipType = "calls_handler"
	RelUsesInstrument RelationshipType = "uses_instrument"
	RelReadsFrom      RelationshipType = "reads_from"
	RelWritesTo       RelationshipType = "writes_to"
	RelDenominatedIn  RelationshipType = "denominated_in"
	RelConverts       RelationshipType = "converts"
	RelTriggersOn     RelationshipType = "triggers_on"
)

// GraphNode represents a resource in the relationship graph.
type GraphNode struct {
	ID       string            `json:"id"`
	Type     NodeType          `json:"type"`
	Name     string            `json:"name"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// GraphEdge represents a relationship between two resources.
type GraphEdge struct {
	Source       string           `json:"source"`
	Target       string           `json:"target"`
	Relationship RelationshipType `json:"relationship"`
	IsDynamic    bool             `json:"is_dynamic"`
	Location     string           `json:"location,omitempty"`
}

// RelationshipGraph contains the full set of nodes and edges extracted from a manifest.
type RelationshipGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// ImpactResult describes what resources are affected by removing a given resource.
type ImpactResult struct {
	RemovedNode   string   `json:"removed_node"`
	AffectedNodes []string `json:"affected_nodes"`
	AffectedEdges int      `json:"affected_edges"`
	ImpactSummary string   `json:"impact_summary"`
}

// Impact returns all nodes and edges that would be affected by removing the given node ID.
func (g *RelationshipGraph) Impact(nodeID string) *ImpactResult {
	affected := make(map[string]bool)
	edgeCount := 0

	for _, edge := range g.Edges {
		if edge.Source == nodeID || edge.Target == nodeID {
			edgeCount++
			if edge.Source == nodeID {
				affected[edge.Target] = true
			} else {
				affected[edge.Source] = true
			}
		}
	}

	nodes := make([]string, 0, len(affected))
	for n := range affected {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	return &ImpactResult{
		RemovedNode:   nodeID,
		AffectedNodes: nodes,
		AffectedEdges: edgeCount,
		ImpactSummary: fmt.Sprintf("removing %s affects %d nodes via %d edges", nodeID, len(nodes), edgeCount),
	}
}

// Dependents returns nodes that depend on the given node ID (i.e., nodes that reference it
// via incoming edges where nodeID is the target). This is distinct from Impact which considers
// all connected edges. Dependents answers "what breaks if I remove this node?"
func (g *RelationshipGraph) Dependents(nodeID string) []string {
	seen := make(map[string]bool)
	for _, edge := range g.Edges {
		if edge.Target == nodeID && edge.Source != nodeID {
			seen[edge.Source] = true
		}
	}
	nodes := make([]string, 0, len(seen))
	for n := range seen {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return nodes
}

// ExtractRelationshipGraph builds a relationship graph from a manifest and Starlark handler call logs.
// The callLogs map is keyed by saga name, with each value being the handler calls made by that saga's script.
// Returns a graph containing nodes (instruments, account_types, sagas, handlers) and edges
// (denominated_in, converts, triggers_on, calls_handler, uses_instrument, reads_from, writes_to).
func ExtractRelationshipGraph(
	manifest *controlplanev1.Manifest,
	callLogs map[string][]schema.HandlerCallInfo,
) *RelationshipGraph {
	g := &RelationshipGraph{}

	// Add instrument nodes
	for _, inst := range manifest.GetInstruments() {
		g.Nodes = append(g.Nodes, GraphNode{
			ID:   "instrument:" + inst.GetCode(),
			Type: NodeTypeInstrument,
			Name: inst.GetName(),
			Metadata: map[string]string{
				"code": inst.GetCode(),
				"type": inst.GetType().String(),
			},
		})
	}

	// Add account type nodes and denominated_in edges
	for _, acct := range manifest.GetAccountTypes() {
		g.Nodes = append(g.Nodes, GraphNode{
			ID:   "account_type:" + acct.GetCode(),
			Type: NodeTypeAccountType,
			Name: acct.GetName(),
			Metadata: map[string]string{
				"code":           acct.GetCode(),
				"normal_balance": acct.GetNormalBalance().String(),
			},
		})

		for _, instCode := range acct.GetAllowedInstruments() {
			g.Edges = append(g.Edges, GraphEdge{
				Source:       "account_type:" + acct.GetCode(),
				Target:       "instrument:" + instCode,
				Relationship: RelDenominatedIn,
			})
		}
	}

	// Add valuation rule edges (converts)
	for _, rule := range manifest.GetValuationRules() {
		g.Edges = append(g.Edges, GraphEdge{
			Source:       "instrument:" + rule.GetFromInstrument(),
			Target:       "instrument:" + rule.GetToInstrument(),
			Relationship: RelConverts,
		})
	}

	// Track handler nodes across all sagas to avoid duplicates
	handlersSeen := make(map[string]bool)

	// Add saga nodes and trigger edges
	for i, saga := range manifest.GetSagas() {
		sagaID := "saga:" + saga.GetName()
		g.Nodes = append(g.Nodes, GraphNode{
			ID:   sagaID,
			Type: NodeTypeSaga,
			Name: saga.GetName(),
			Metadata: map[string]string{
				"trigger": saga.GetTrigger(),
			},
		})

		// triggers_on edge
		g.Edges = append(g.Edges, GraphEdge{
			Source:       sagaID,
			Target:       saga.GetTrigger(),
			Relationship: RelTriggersOn,
			Location:     fmt.Sprintf("sagas[%d].trigger", i),
		})

		// Extract handler call relationships from call logs
		if calls, ok := callLogs[saga.GetName()]; ok {
			extractHandlerCallEdges(g, sagaID, calls, fmt.Sprintf("sagas[%d].script", i), handlersSeen)
		}
	}

	return g
}

// edgeKey identifies a unique edge for deduplication.
type edgeKey struct {
	source, target string
	rel            RelationshipType
}

// extractHandlerCallEdges adds edges from handler call info gathered during Starlark validation.
// handlersSeen is shared across all sagas to deduplicate handler nodes graph-wide.
func extractHandlerCallEdges(g *RelationshipGraph, sagaID string, calls []schema.HandlerCallInfo, location string, handlersSeen map[string]bool) {
	for _, call := range calls {
		handlerID := "handler:" + call.HandlerName

		// Add handler node if first time seen across all sagas
		if !handlersSeen[handlerID] {
			handlersSeen[handlerID] = true
			g.Nodes = append(g.Nodes, GraphNode{
				ID:   handlerID,
				Type: NodeTypeHandler,
				Name: call.HandlerName,
			})
		}

		// calls_handler edge
		g.Edges = append(g.Edges, GraphEdge{
			Source:       sagaID,
			Target:       handlerID,
			Relationship: RelCallsHandler,
			Location:     location,
		})

		// Extract param-derived edges (instrument and account references)
		extractParamEdges(g, sagaID, handlerID, call, location)
	}
}

// extractParamEdges analyzes handler call parameters for instrument and account references.
// All param-derived edges are dynamic (values resolved at runtime from variables).
// Duplicates are collapsed when multiple params match the same pattern.
func extractParamEdges(g *RelationshipGraph, sagaID, handlerID string, call schema.HandlerCallInfo, location string) {
	edgesSeen := make(map[edgeKey]bool)

	for _, paramName := range call.ParamNames {
		lowerParam := strings.ToLower(paramName)

		if strings.Contains(lowerParam, "instrument_code") || strings.Contains(lowerParam, "instrument") {
			addDynamicEdge(g, edgesSeen, sagaID, handlerID, RelUsesInstrument, location)
		}

		if strings.Contains(lowerParam, "account_id") || strings.Contains(lowerParam, "account") {
			rel := accountRelationship(call.HandlerName)
			addDynamicEdge(g, edgesSeen, sagaID, handlerID, rel, location)
		}
	}
}

// accountRelationship determines whether a handler represents a read or write based on its name.
func accountRelationship(handlerName string) RelationshipType {
	handlerLower := strings.ToLower(handlerName)
	if strings.Contains(handlerLower, "initiate") ||
		strings.Contains(handlerLower, "create") ||
		strings.Contains(handlerLower, "update") ||
		strings.Contains(handlerLower, "post") {
		return RelWritesTo
	}
	return RelReadsFrom
}

// addDynamicEdge adds a dynamic edge if not already present in edgesSeen.
func addDynamicEdge(g *RelationshipGraph, edgesSeen map[edgeKey]bool, source, target string, rel RelationshipType, location string) {
	key := edgeKey{source, target, rel}
	if edgesSeen[key] {
		return
	}
	edgesSeen[key] = true
	g.Edges = append(g.Edges, GraphEdge{
		Source:       source,
		Target:       target,
		Relationship: rel,
		IsDynamic:    true,
		Location:     location,
	})
}

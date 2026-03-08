package validator

import (
	"fmt"
	"sort"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRelationshipGraph_CompleteManifest(t *testing.T) {
	manifest := validManifest()
	callLogs := map[string][]schema.HandlerCallInfo{
		"process_settlement": {
			{
				HandlerName: "position_keeping.initiate_log",
				ParamNames:  []string{"account_id", "instrument_code", "amount", "direction"},
			},
		},
	}

	g := ExtractRelationshipGraph(manifest, callLogs)

	require.NotNil(t, g)

	// Check instrument nodes
	assertNodeExists(t, g, "instrument:GBP", NodeTypeInstrument)
	assertNodeExists(t, g, "instrument:KWH", NodeTypeInstrument)

	// Check account type node
	assertNodeExists(t, g, "account_type:SETTLEMENT", NodeTypeAccountType)

	// Check saga node
	assertNodeExists(t, g, "saga:process_settlement", NodeTypeSaga)

	// Check handler node
	assertNodeExists(t, g, "handler:position_keeping.initiate_log", NodeTypeHandler)

	// Check denominated_in edge
	assertEdgeExists(t, g, "account_type:SETTLEMENT", "instrument:GBP", RelDenominatedIn)

	// Check converts edge
	assertEdgeExists(t, g, "instrument:KWH", "instrument:GBP", RelConverts)

	// Check triggers_on edge
	assertEdgeExists(t, g, "saga:process_settlement", "api:/v1/settlements", RelTriggersOn)

	// Check calls_handler edge
	assertEdgeExists(t, g, "saga:process_settlement", "handler:position_keeping.initiate_log", RelCallsHandler)

	// Check uses_instrument edge from instrument_code param
	assertEdgeExists(t, g, "saga:process_settlement", "handler:position_keeping.initiate_log", RelUsesInstrument)

	// Check writes_to edge from account_id param + "initiate" handler name
	assertEdgeExists(t, g, "saga:process_settlement", "handler:position_keeping.initiate_log", RelWritesTo)
}

func TestExtractRelationshipGraph_EmptyManifest(t *testing.T) {
	manifest := &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name: "Empty",
		},
	}

	g := ExtractRelationshipGraph(manifest, nil)

	require.NotNil(t, g)
	assert.Empty(t, g.Nodes)
	assert.Empty(t, g.Edges)
}

func TestExtractRelationshipGraph_NoHandlerCalls(t *testing.T) {
	manifest := validManifest()
	// Saga with no handler calls
	callLogs := map[string][]schema.HandlerCallInfo{}

	g := ExtractRelationshipGraph(manifest, callLogs)

	require.NotNil(t, g)
	// Should still have instrument, account type, saga nodes
	assertNodeExists(t, g, "instrument:GBP", NodeTypeInstrument)
	assertNodeExists(t, g, "saga:process_settlement", NodeTypeSaga)

	// No handler nodes
	for _, n := range g.Nodes {
		assert.NotEqual(t, NodeTypeHandler, n.Type, "no handler nodes expected without call logs")
	}
}

func TestExtractRelationshipGraph_DynamicParams(t *testing.T) {
	manifest := validManifest()
	callLogs := map[string][]schema.HandlerCallInfo{
		"process_settlement": {
			{
				HandlerName: "current_account.retrieve_balance",
				ParamNames:  []string{"account_id"},
			},
		},
	}

	g := ExtractRelationshipGraph(manifest, callLogs)

	// account_id params are marked dynamic
	found := false
	for _, edge := range g.Edges {
		if edge.Relationship == RelReadsFrom && edge.IsDynamic {
			found = true
			break
		}
	}
	assert.True(t, found, "expected reads_from edge with is_dynamic=true for account_id param")
}

func TestExtractRelationshipGraph_ReadVsWriteHeuristic(t *testing.T) {
	manifest := validManifest()

	tests := []struct {
		name        string
		handlerName string
		expectedRel RelationshipType
	}{
		{"initiate writes", "position_keeping.initiate_log", RelWritesTo},
		{"create writes", "current_account.create_account", RelWritesTo},
		{"update writes", "current_account.update_status", RelWritesTo},
		{"retrieve reads", "current_account.retrieve_balance", RelReadsFrom},
		{"get reads", "position_keeping.get_position", RelReadsFrom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callLogs := map[string][]schema.HandlerCallInfo{
				"process_settlement": {
					{HandlerName: tt.handlerName, ParamNames: []string{"account_id"}},
				},
			}

			g := ExtractRelationshipGraph(manifest, callLogs)

			found := false
			for _, edge := range g.Edges {
				if edge.Relationship == tt.expectedRel {
					found = true
					break
				}
			}
			assert.True(t, found, "expected %s edge for handler %s", tt.expectedRel, tt.handlerName)
		})
	}
}

func TestImpact(t *testing.T) {
	manifest := validManifest()
	callLogs := map[string][]schema.HandlerCallInfo{
		"process_settlement": {
			{
				HandlerName: "position_keeping.initiate_log",
				ParamNames:  []string{"instrument_code", "amount"},
			},
		},
	}

	g := ExtractRelationshipGraph(manifest, callLogs)

	// Impact of removing GBP instrument
	impact := g.Impact("instrument:GBP")
	require.NotNil(t, impact)
	assert.Equal(t, "instrument:GBP", impact.RemovedNode)
	assert.Greater(t, len(impact.AffectedNodes), 0, "removing GBP should affect at least the settlement account type")
	assert.Greater(t, impact.AffectedEdges, 0)

	// Verify SETTLEMENT account type is affected (via denominated_in)
	assert.Contains(t, impact.AffectedNodes, "account_type:SETTLEMENT")
}

func TestImpact_UnknownNode(t *testing.T) {
	g := &RelationshipGraph{
		Nodes: []GraphNode{{ID: "instrument:GBP", Type: NodeTypeInstrument, Name: "GBP"}},
		Edges: []GraphEdge{},
	}

	impact := g.Impact("instrument:NONEXISTENT")
	assert.Equal(t, 0, len(impact.AffectedNodes))
	assert.Equal(t, 0, impact.AffectedEdges)
}

func TestExtractRelationshipGraph_DeduplicatesHandlerNodes(t *testing.T) {
	manifest := validManifest()
	callLogs := map[string][]schema.HandlerCallInfo{
		"process_settlement": {
			{HandlerName: "position_keeping.initiate_log", ParamNames: []string{"amount"}},
			{HandlerName: "position_keeping.initiate_log", ParamNames: []string{"amount"}},
		},
	}

	g := ExtractRelationshipGraph(manifest, callLogs)

	handlerCount := 0
	for _, n := range g.Nodes {
		if n.ID == "handler:position_keeping.initiate_log" {
			handlerCount++
		}
	}
	assert.Equal(t, 1, handlerCount, "handler node should appear only once despite multiple calls")
}

func TestExtractRelationshipGraph_DeduplicatesHandlerNodesAcrossSagas(t *testing.T) {
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "another_saga",
		Trigger: "api:/v1/other",
		Script:  "x = 1",
	})
	callLogs := map[string][]schema.HandlerCallInfo{
		"process_settlement": {
			{HandlerName: "position_keeping.initiate_log", ParamNames: []string{"amount"}},
		},
		"another_saga": {
			{HandlerName: "position_keeping.initiate_log", ParamNames: []string{"amount"}},
		},
	}

	g := ExtractRelationshipGraph(m, callLogs)

	handlerCount := 0
	for _, n := range g.Nodes {
		if n.ID == "handler:position_keeping.initiate_log" {
			handlerCount++
		}
	}
	assert.Equal(t, 1, handlerCount, "handler node should appear only once across sagas")
}

func TestExtractRelationshipGraph_CollapsesDuplicateEdgesPerCall(t *testing.T) {
	manifest := validManifest()
	// Handler with two account params - should only produce one writes_to edge
	callLogs := map[string][]schema.HandlerCallInfo{
		"process_settlement": {
			{
				HandlerName: "position_keeping.initiate_log",
				ParamNames:  []string{"source_account_id", "destination_account_id"},
			},
		},
	}

	g := ExtractRelationshipGraph(manifest, callLogs)

	writesCount := 0
	for _, e := range g.Edges {
		if e.Relationship == RelWritesTo {
			writesCount++
		}
	}
	assert.Equal(t, 1, writesCount, "duplicate account params should collapse into one writes_to edge")
}

func TestValidate_PopulatesGraphOnSuccess(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := v.Validate(validManifest(), nil)
	require.True(t, result.Valid)
	require.NotNil(t, result.Graph, "graph should be populated for valid manifests")
	assert.Greater(t, len(result.Graph.Nodes), 0)
}

func TestValidate_NoGraphOnError(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Invalid manifest (missing required fields)
	m := &controlplanev1.Manifest{}
	result := v.Validate(m, nil)
	assert.False(t, result.Valid)
	assert.Nil(t, result.Graph, "graph should not be populated for invalid manifests")
}

// assertNodeExists checks that a node with the given ID and type exists in the graph.
func assertNodeExists(t *testing.T, g *RelationshipGraph, id string, nodeType NodeType) {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			assert.Equal(t, nodeType, n.Type, "node %s has wrong type", id)
			return
		}
	}
	// Collect all node IDs for debugging
	ids := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)
	t.Errorf("node %s not found in graph; existing nodes: %v", id, ids)
}

// assertEdgeExists checks that an edge with the given source, target, and relationship exists.
func assertEdgeExists(t *testing.T, g *RelationshipGraph, source, target string, rel RelationshipType) {
	t.Helper()
	for _, e := range g.Edges {
		if e.Source == source && e.Target == target && e.Relationship == rel {
			return
		}
	}
	edges := make([]string, 0, len(g.Edges))
	for _, e := range g.Edges {
		edges = append(edges, fmt.Sprintf("%s -> %s [%s]", e.Source, e.Target, e.Relationship))
	}
	sort.Strings(edges)
	t.Errorf("edge %s -> %s [%s] not found in graph; existing edges: %v", source, target, rel, edges)
}

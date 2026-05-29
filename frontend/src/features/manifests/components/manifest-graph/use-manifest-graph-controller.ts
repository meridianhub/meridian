import { useCallback, useMemo, useState } from 'react'
import { useNodesState, useEdgesState, type Node, type Edge, type NodeMouseHandler } from '@xyflow/react'
import { useNavigate } from 'react-router-dom'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import {
  buildManifestGraph,
  type ManifestGraph,
  type ManifestNode,
  type ManifestEdge,
  type ManifestNodeType,
} from '../../lib/manifest-graph-model'
import { NODE_TYPE_REGISTRY } from '../../lib/node-type-registry'
import { useEventChain } from '../../hooks/use-event-chain'
import type { ManifestNodeData } from './node-renderers'
import { getNodeNavigationPath } from './node-navigation'
import { useGraphHighlight } from './use-graph-highlight'
import { useGraphLayoutEffect } from './use-graph-layout-effect'
import { useVisibleTypes } from './use-visible-types'

/** Count how many nodes of each type the graph contains (zero-filled). */
function countNodesByType(graph: ManifestGraph): Record<ManifestNodeType, number> {
  const counts = Object.fromEntries(
    (Object.keys(NODE_TYPE_REGISTRY) as ManifestNodeType[]).map((t) => [t, 0]),
  ) as Record<ManifestNodeType, number>
  for (const n of graph.nodes) {
    counts[n.type]++
  }
  return counts
}

/** Edges whose endpoints are both currently visible, used for hover highlighting. */
function visibleSubgraphEdges(graph: ManifestGraph, visibleTypes: Set<ManifestNodeType>): ManifestEdge[] {
  const visibleIds = new Set(graph.nodes.filter((n) => visibleTypes.has(n.type)).map((n) => n.id))
  return graph.edges.filter((e) => visibleIds.has(e.source) && visibleIds.has(e.target))
}

/**
 * Owns all data, derived state, and interaction handlers for the manifest
 * graph. Keeps the `ManifestGraph` component itself a thin presentational
 * shell that simply wires this output into React Flow and the overlay panels.
 */
export function useManifestGraphController(manifest: Manifest) {
  const navigate = useNavigate()
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [hoveredNode, setHoveredNode] = useState<string | null>(null)
  const [selectedNode, setSelectedNode] = useState<string | null>(null)
  const [showEventChain, setShowEventChain] = useState(false)
  const visibility = useVisibleTypes()
  const { visibleTypes } = visibility

  const graph = useMemo(() => buildManifestGraph(manifest), [manifest])

  // Clear the selection if the selected node no longer exists in the graph.
  const effectiveSelectedNode = useMemo(
    () => (selectedNode && graph.nodes.some((n) => n.id === selectedNode) ? selectedNode : null),
    [selectedNode, graph],
  )

  const selectedManifestNode = useMemo<ManifestNode | null>(
    () => (effectiveSelectedNode ? graph.nodes.find((n) => n.id === effectiveSelectedNode) ?? null : null),
    [graph, effectiveSelectedNode],
  )

  const eventChain = useEventChain(graph, showEventChain ? effectiveSelectedNode : null)
  const nodeCountByType = useMemo(() => countNodesByType(graph), [graph])
  const currentEdges = useMemo(() => visibleSubgraphEdges(graph, visibleTypes), [graph, visibleTypes])

  useGraphLayoutEffect(graph, visibleTypes, setNodes, setEdges)
  useGraphHighlight({ hoveredNode, selectedNode: effectiveSelectedNode, currentEdges, setNodes, setEdges })

  const clearSelection = useCallback(() => {
    setSelectedNode(null)
    setShowEventChain(false)
  }, [])

  // Hiding the selected node's type (or hiding everything) clears the selection.
  const toggleType = useCallback(
    (type: ManifestNodeType) => {
      visibility.toggleType(type)
      if (selectedManifestNode?.type === type) clearSelection()
    },
    [visibility, selectedManifestNode, clearSelection],
  )

  const hideAllTypes = useCallback(() => {
    visibility.hideAllTypes()
    clearSelection()
  }, [visibility, clearSelection])

  const onNodeClick = useCallback<NodeMouseHandler>((_event, node) => {
    setSelectedNode((prev) => (prev === node.id ? null : node.id))
    setShowEventChain(false)
  }, [])

  const onNodeDoubleClick = useCallback<NodeMouseHandler>(
    (_event, node) => {
      const path = getNodeNavigationPath((node.data as ManifestNodeData).manifestNode)
      if (path) navigate(path)
    },
    [navigate],
  )

  const onNodeMouseEnter = useCallback<NodeMouseHandler>((_event, node) => setHoveredNode(node.id), [])
  const onNodeMouseLeave = useCallback<NodeMouseHandler>(() => setHoveredNode(null), [])

  return {
    nodes,
    edges,
    onNodesChange,
    onEdgesChange,
    graph,
    selectedManifestNode,
    eventChain,
    nodeCountByType,
    totalVisible: nodes.length,
    visibleTypes,
    toggleType,
    showAllTypes: visibility.showAllTypes,
    hideAllTypes,
    showEventChain,
    setShowEventChain,
    clearSelection,
    onNodeClick,
    onNodeDoubleClick,
    onPaneClick: clearSelection,
    onNodeMouseEnter,
    onNodeMouseLeave,
  }
}

import { useState } from 'react'
import { ReactFlow, Controls, Background, MiniMap, BackgroundVariant } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useNavigate } from 'react-router-dom'
import { Maximize2 } from 'lucide-react'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Button } from '@/components/ui/button'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { ApplyResourceModal } from '@/features/economy/components/apply-resource-modal'
import { getResourceSchema } from '@/features/economy/lib/resource-schema-registry'
import { nodeTypes, type ManifestNodeData } from './node-renderers'
import { EdgeLegend, FilterSidebar, SelectionToolbar, EventChainSidePanel } from './graph-panels'
import { useManifestGraphController } from './use-manifest-graph-controller'

interface ManifestGraphProps {
  manifest: Manifest
  className?: string
  /** @internal Suppresses the fullscreen button to prevent recursive nesting. */
  _fullscreen?: boolean
}

type GraphController = ReturnType<typeof useManifestGraphController>

export function ManifestGraph({ manifest, className, _fullscreen }: ManifestGraphProps) {
  const c = useManifestGraphController(manifest)

  if (c.graph.nodes.length === 0) {
    return (
      <div className={`${className ?? ''} w-full h-full`} data-testid="manifest-graph-empty">
        <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
          No elements in manifest to visualize.
        </div>
      </div>
    )
  }

  return (
    <div className={`${className ?? ''} w-full h-full relative`}>
      <TooltipProvider>
        <ReactFlow
          nodes={c.nodes}
          edges={c.edges}
          onNodesChange={c.onNodesChange}
          onEdgesChange={c.onEdgesChange}
          onNodeClick={c.onNodeClick}
          onNodeDoubleClick={c.onNodeDoubleClick}
          onPaneClick={c.onPaneClick}
          onNodeMouseEnter={c.onNodeMouseEnter}
          onNodeMouseLeave={c.onNodeMouseLeave}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.3 }}
          proOptions={{ hideAttribution: true }}
        >
          <Controls />
          <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
          <MiniMap nodeColor={(n) => (n.data as ManifestNodeData).color} maskColor="rgba(0, 0, 0, 0.15)" />
        </ReactFlow>
      </TooltipProvider>

      <FilterSidebar
        visibleTypes={c.visibleTypes}
        nodeCountByType={c.nodeCountByType}
        totalVisible={c.totalVisible}
        onToggle={c.toggleType}
        onShowAll={c.showAllTypes}
        onHideAll={c.hideAllTypes}
      />
      <EdgeLegend />

      <GraphChrome manifest={manifest} _fullscreen={_fullscreen} controller={c} />
    </div>
  )
}

/** Selection-driven overlays: toolbar, event-chain drawer, fullscreen, edit modal. */
function GraphChrome({
  manifest,
  _fullscreen,
  controller: c,
}: {
  manifest: Manifest
  _fullscreen?: boolean
  controller: GraphController
}) {
  const navigate = useNavigate()
  const [fullscreen, setFullscreen] = useState(false)
  const [editModalOpen, setEditModalOpen] = useState(false)
  const selected = c.selectedManifestNode

  const canShowEventChain = selected?.type === 'instrument' || selected?.type === 'account_type'
  const canEditResource = selected ? getResourceSchema(selected.type) !== undefined : false

  return (
    <>
      {selected && (
        <SelectionToolbar
          selectedNode={selected}
          canEditResource={canEditResource}
          canShowEventChain={canShowEventChain}
          onEdit={() => setEditModalOpen(true)}
          onShowEventChain={() => c.setShowEventChain(true)}
          onDeselect={c.clearSelection}
        />
      )}

      {c.showEventChain && c.eventChain && selected && (
        <EventChainSidePanel
          chain={c.eventChain}
          startNodeLabel={selected.label}
          onSagaClick={(sagaId) => navigate(`/starlark-config/${sagaId.replace(/^saga:/, '')}`)}
          onClose={c.clearSelection}
        />
      )}

      {/* Fullscreen button + dialog - suppressed in nested (already-fullscreen) instances */}
      {!_fullscreen && !selected && (
        <Button
          variant="outline"
          size="icon"
          className="absolute top-3 right-3 z-10 size-8 bg-background/80 backdrop-blur-sm"
          onClick={() => setFullscreen(true)}
          aria-label="View fullscreen"
        >
          <Maximize2 className="size-4" />
        </Button>
      )}

      {!_fullscreen && (
        <Dialog open={fullscreen} onOpenChange={setFullscreen}>
          <DialogContent className="max-w-[calc(100vw-2rem)] h-[calc(100vh-2rem)] sm:max-w-[calc(100vw-4rem)] sm:h-[calc(100vh-4rem)] flex flex-col p-4">
            <DialogHeader className="shrink-0">
              <DialogTitle>Economy Graph</DialogTitle>
            </DialogHeader>
            <div className="flex-1 min-h-0 rounded-lg border">
              <ManifestGraph manifest={manifest} className="h-full w-full" _fullscreen />
            </div>
          </DialogContent>
        </Dialog>
      )}

      {selected && canEditResource && (
        <ApplyResourceModal
          key={selected.id}
          open={editModalOpen}
          onOpenChange={setEditModalOpen}
          nodeType={selected.type}
          initialData={selected.data}
        />
      )}
    </>
  )
}

import { Pencil, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { ManifestNode, ManifestNodeType } from '../../lib/manifest-graph-model'
import { getNodeThemes } from '../../lib/node-type-registry'
import type { EventChain } from '../../lib/transitive-closure'
import { EventChainPanel } from '../event-chain-panel'

const NODE_THEMES = getNodeThemes()

const EDGE_LEGEND: { label: string; color: string; dashed?: boolean }[] = [
  { label: 'Allowed by', color: 'var(--graph-instrument)' },
  { label: 'Converts from', color: 'var(--graph-valuation-rule)', dashed: true },
  { label: 'Converts to', color: 'var(--graph-valuation-rule)' },
]

function LegendItem({ label, color, dashed }: { label: string; color: string; dashed?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <svg width="32" height="12">
        <line
          x1="0"
          y1="6"
          x2="32"
          y2="6"
          stroke={color}
          strokeWidth={2}
          strokeDasharray={dashed ? '6 3' : undefined}
        />
      </svg>
      <span className="text-xs text-muted-foreground">{label}</span>
    </div>
  )
}

/** Bottom-left edge relationship legend. */
export function EdgeLegend() {
  return (
    <div className="absolute bottom-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
      <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Edges</span>
      {EDGE_LEGEND.map((item) => (
        <LegendItem key={item.label} {...item} />
      ))}
    </div>
  )
}

interface FilterSidebarProps {
  visibleTypes: Set<ManifestNodeType>
  nodeCountByType: Record<ManifestNodeType, number>
  totalVisible: number
  onToggle: (type: ManifestNodeType) => void
  onShowAll: () => void
  onHideAll: () => void
}

/** Top-left element-type filter with show-all / hide-all controls. */
export function FilterSidebar({
  visibleTypes,
  nodeCountByType,
  totalVisible,
  onToggle,
  onShowAll,
  onHideAll,
}: FilterSidebarProps) {
  return (
    <div className="absolute top-3 left-3 z-10 flex flex-col gap-2 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Element Types</span>
        <div className="flex gap-1">
          <button
            type="button"
            onClick={onShowAll}
            className="text-[10px] text-muted-foreground hover:text-foreground transition-colors px-1"
            aria-label="Show all types"
          >
            All
          </button>
          <span className="text-[10px] text-muted-foreground">/</span>
          <button
            type="button"
            onClick={onHideAll}
            className="text-[10px] text-muted-foreground hover:text-foreground transition-colors px-1"
            aria-label="Hide all types"
          >
            None
          </button>
        </div>
      </div>
      {(Object.keys(NODE_THEMES) as ManifestNodeType[]).map((type) => {
        const theme = NODE_THEMES[type]
        const count = nodeCountByType[type]
        if (count === 0) return null
        return (
          <label key={type} className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={visibleTypes.has(type)}
              onChange={() => onToggle(type)}
              className="rounded"
              aria-label={`Show ${theme.label}`}
            />
            <span className="w-2.5 h-2.5 rounded-full" style={{ backgroundColor: theme.color }} />
            <span className="text-xs text-foreground">{theme.label}</span>
            <span className="text-[10px] text-muted-foreground">({count})</span>
          </label>
        )
      })}
      <span className="text-[10px] text-muted-foreground mt-1">{totalVisible} nodes visible</span>
    </div>
  )
}

interface SelectionToolbarProps {
  selectedNode: ManifestNode
  canEditResource: boolean
  canShowEventChain: boolean
  onEdit: () => void
  onShowEventChain: () => void
  onDeselect: () => void
}

/** Top-right toolbar shown when a node is selected. */
export function SelectionToolbar({
  selectedNode,
  canEditResource,
  canShowEventChain,
  onEdit,
  onShowEventChain,
  onDeselect,
}: SelectionToolbarProps) {
  return (
    <div
      className="absolute top-3 right-3 z-10 flex items-center gap-2 rounded-lg border bg-background/95 p-2 backdrop-blur-sm shadow-sm"
      data-testid="node-toolbar"
    >
      <span className="text-xs font-medium text-foreground px-1">{selectedNode.label}</span>
      {canEditResource && (
        <Button
          size="sm"
          variant="outline"
          className="text-xs h-7"
          onClick={onEdit}
          data-testid="edit-resource-button"
        >
          <Pencil className="h-3 w-3 mr-1" />
          Edit
        </Button>
      )}
      {canShowEventChain && (
        <Button
          size="sm"
          variant="outline"
          className="text-xs h-7"
          onClick={onShowEventChain}
          data-testid="show-event-chain-button"
        >
          Show Event Chain
        </Button>
      )}
      <Button
        size="sm"
        variant="ghost"
        className="h-7 w-7 p-0"
        onClick={onDeselect}
        aria-label="Deselect node"
      >
        <X className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}

interface EventChainSidePanelProps {
  chain: EventChain
  startNodeLabel: string
  onSagaClick: (sagaId: string) => void
  onClose: () => void
}

/** Right-hand drawer showing the transitive event chain for the selected node. */
export function EventChainSidePanel({
  chain,
  startNodeLabel,
  onSagaClick,
  onClose,
}: EventChainSidePanelProps) {
  return (
    <div
      className="absolute top-0 right-0 z-20 h-full w-96 border-l bg-background shadow-lg overflow-y-auto"
      data-testid="event-chain-side-panel"
    >
      <div className="flex items-center justify-between p-3 border-b">
        <h3 className="text-sm font-semibold">Event Chain</h3>
        <Button
          size="sm"
          variant="ghost"
          className="h-7 w-7 p-0"
          onClick={onClose}
          aria-label="Close event chain panel"
          data-testid="close-event-chain-panel"
        >
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="p-3">
        <EventChainPanel chain={chain} startNodeLabel={startNodeLabel} onSagaClick={onSagaClick} />
      </div>
    </div>
  )
}

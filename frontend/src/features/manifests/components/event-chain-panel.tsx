import { useState } from 'react'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Badge } from '@/components/ui/badge'
import type { EventChain, EventHop } from '../lib/transitive-closure'

const FILTER_BADGE_STYLES: Record<string, string> = {
  pass: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300',
  fail: 'bg-red-100 text-red-700 dark:bg-red-950/40 dark:text-red-300',
  indeterminate: 'bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300',
}

const TERMINATION_LABELS: Record<string, string> = {
  filter_rejection: 'Chain terminated: all sagas filtered out',
  chain_depth_limit: 'Chain terminated: maximum depth reached',
  no_matching_sagas: 'Chain terminated: no matching sagas found',
}

interface EventChainPanelProps {
  chain: EventChain
  startNodeLabel: string
  onSagaClick?: (sagaName: string) => void
}

export function EventChainPanel({ chain, startNodeLabel, onSagaClick }: EventChainPanelProps) {
  if (chain.hops.length === 0) {
    return (
      <div className="rounded-lg border p-4" data-testid="event-chain-panel">
        <div className="text-sm text-muted-foreground">
          No event chain from {startNodeLabel}.
        </div>
        <TerminationBadge reason={chain.terminationReason} />
      </div>
    )
  }

  const hopsByDepth = groupHopsByDepth(chain.hops)

  return (
    <div className="rounded-lg border p-4" data-testid="event-chain-panel">
      <div className="mb-4 flex items-center justify-between">
        <h3 className="text-sm font-semibold">
          Event chain from {startNodeLabel}
        </h3>
        <Badge variant="outline" className="text-xs">
          Depth: {chain.maxDepthUsed}
        </Badge>
      </div>

      <div className="relative ml-3 border-l-2 border-muted-foreground/20 pl-6">
        {[...hopsByDepth.entries()].map(([depth, hops]) => (
          <DepthGroup
            key={depth}
            depth={depth}
            hops={hops}
            onSagaClick={onSagaClick}
          />
        ))}
      </div>

      <div className="mt-4">
        <TerminationBadge reason={chain.terminationReason} />
      </div>
    </div>
  )
}

function TerminationBadge({ reason }: { reason: EventChain['terminationReason'] }) {
  return (
    <div className="mt-2" data-testid="termination-reason">
      <Badge variant="secondary" className="text-xs">
        {TERMINATION_LABELS[reason] ?? reason}
      </Badge>
    </div>
  )
}

function groupHopsByDepth(hops: EventHop[]): Map<number, EventHop[]> {
  const map = new Map<number, EventHop[]>()
  for (const hop of hops) {
    const group = map.get(hop.depth) ?? []
    group.push(hop)
    map.set(hop.depth, group)
  }
  return map
}

interface DepthGroupProps {
  depth: number
  hops: EventHop[]
  onSagaClick?: (sagaName: string) => void
}

function DepthGroup({ depth, hops, onSagaClick }: DepthGroupProps) {
  return (
    <div className="relative mb-4 last:mb-0">
      <div className="absolute -left-[31px] top-1 h-3 w-3 rounded-full border-2 border-muted-foreground/40 bg-background" />
      <div className="mb-2 text-xs font-medium text-muted-foreground">
        Hop {depth}
      </div>
      <Accordion type="multiple" className="space-y-1">
        {hops.map((hop, idx) => (
          <HopItem
            key={`${hop.saga}-${idx}`}
            hop={hop}
            index={idx}
            onSagaClick={onSagaClick}
          />
        ))}
      </Accordion>
    </div>
  )
}

interface HopItemProps {
  hop: EventHop
  index: number
  onSagaClick?: (sagaName: string) => void
}

function HopItem({ hop, index, onSagaClick }: HopItemProps) {
  const [showDiagram, setShowDiagram] = useState(false)
  const filterStyle = FILTER_BADGE_STYLES[hop.filterResult] ?? ''

  return (
    <AccordionItem value={`${hop.saga}-${index}`} className="border rounded-md px-3">
      <div className="flex items-center gap-2 py-2">
        {onSagaClick ? (
          <button
            type="button"
            className="font-medium text-sm hover:underline text-left"
            onClick={() => onSagaClick(hop.saga)}
            data-testid={`saga-link-${hop.saga}`}
          >
            {hop.saga}
          </button>
        ) : (
          <span className="font-medium text-sm" data-testid={`saga-link-${hop.saga}`}>
            {hop.saga}
          </span>
        )}
        <Badge
          variant="outline"
          className={`text-[10px] ${filterStyle}`}
          data-testid={`filter-badge-${hop.filterResult}`}
        >
          {hop.filterResult}
        </Badge>
        <div className="ml-auto">
          <AccordionTrigger
            aria-label={`Toggle details for ${hop.saga}`}
            className="py-0 text-sm hover:no-underline"
          />
        </div>
      </div>
      <AccordionContent>
        <div className="space-y-3 text-xs">
          {hop.trigger && (
            <div>
              <span className="font-medium text-muted-foreground">Trigger: </span>
              <span>{hop.trigger.channel}</span>
              {hop.trigger.instrumentCode && (
                <span className="ml-1 text-muted-foreground">({hop.trigger.instrumentCode})</span>
              )}
            </div>
          )}

          {hop.filterExpression && (
            <div>
              <span className="font-medium text-muted-foreground">Filter: </span>
              <code className="rounded bg-muted px-1 py-0.5 text-[11px]">
                {hop.filterExpression}
              </code>
            </div>
          )}

          {hop.filterReason && (
            <div>
              <span className="font-medium text-muted-foreground">Reason: </span>
              <span>{hop.filterReason}</span>
            </div>
          )}

          {hop.producedEvents.length > 0 && (
            <div>
              <span className="font-medium text-muted-foreground">Produced events:</span>
              <ul className="mt-1 space-y-0.5 list-disc list-inside">
                {hop.producedEvents.map((pe, i) => (
                  <li key={i}>
                    {pe.channel}
                    {pe.instrumentCode && ` [${pe.instrumentCode}]`}
                    {pe.direction && ` (${pe.direction})`}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <SagaDiagramToggle
            showDiagram={showDiagram}
            onToggle={() => setShowDiagram(!showDiagram)}
            sagaId={hop.saga}
          />
        </div>
      </AccordionContent>
    </AccordionItem>
  )
}

interface SagaDiagramToggleProps {
  showDiagram: boolean
  onToggle: () => void
  sagaId: string
}

function SagaDiagramToggle({ showDiagram, onToggle, sagaId }: SagaDiagramToggleProps) {
  // The saga source would need to come from the manifest graph node data.
  // For now, we show a toggle button. The SagaFlowDiagram requires a parsed SagaFlow.
  // In real usage, the parent would provide saga sources via context or props.
  return (
    <div>
      <button
        type="button"
        className="text-xs text-primary hover:underline"
        onClick={onToggle}
        data-testid={`saga-diagram-toggle-${sagaId}`}
      >
        {showDiagram ? 'Hide saga flow' : 'Show saga flow'}
      </button>
      {showDiagram && (
        <div className="mt-2 h-64 rounded border" data-testid={`saga-diagram-${sagaId}`}>
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            Saga flow diagram requires source for {sagaId}
          </div>
        </div>
      )}
    </div>
  )
}

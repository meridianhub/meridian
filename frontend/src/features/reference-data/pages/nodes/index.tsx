import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { PageShell } from '@/shared/page-shell'
import { PageHeader } from '@/shared/page-header'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ChevronRight, ChevronDown, Plus } from 'lucide-react'
import type { ReferenceDataNode } from '@/api/gen/meridian/reference_data/v1/node_pb'
import { CreateNodeDialog } from './create-node-dialog'

interface NodeRowProps {
  node: ReferenceDataNode
  depth: number
  asAt: string
  onAddChild: (parentId: string) => void
}

function NodeRow({ node, depth, asAt, onAddChild }: NodeRowProps) {
  const clients = useApiClients()
  const [expanded, setExpanded] = React.useState(false)

  const childrenQueryKey = ['node-children', node.id, asAt]

  const { data: childrenData, isFetching } = useQuery({
    queryKey: childrenQueryKey,
    queryFn: async () => {
      const result = await clients.node.getChildren({
        parentId: node.id,
        activeOnly: !asAt,
      })
      return result.nodes ?? []
    },
    enabled: expanded,
    staleTime: 30_000,
  })

  const children = childrenData ?? []

  function handleToggle() {
    if (expanded) {
      setExpanded(false)
    } else {
      setExpanded(true)
    }
  }

  return (
    <>
      <div
        className="group flex items-center gap-1 py-1.5 hover:bg-muted/30 rounded px-1"
        style={{ paddingLeft: `${depth * 20 + 4}px` }}
      >
        <Button
          variant="ghost"
          size="sm"
          className="h-6 w-6 p-0"
          onClick={handleToggle}
          aria-label={expanded ? 'Collapse' : 'Expand'}
          disabled={isFetching}
        >
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" />
          )}
        </Button>

        <div className="flex items-center gap-3 min-w-0 flex-1">
          <span className="font-mono text-sm font-medium truncate">{node.id}</span>
          <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground shrink-0">
            {node.nodeType}
          </span>
        </div>

        <Button
          variant="ghost"
          size="sm"
          className="h-6 w-6 p-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 shrink-0"
          onClick={() => onAddChild(node.id)}
          aria-label={`Add child to ${node.id}`}
        >
          <Plus className="h-3 w-3" />
        </Button>
      </div>

      {expanded && children.map((child) => (
        <NodeRow key={child.id} node={child} depth={depth + 1} asAt={asAt} onAddChild={onAddChild} />
      ))}
    </>
  )
}

export function NodesPage() {
  const clients = useApiClients()
  const [asAt, setAsAt] = React.useState('')
  const [createDialogOpen, setCreateDialogOpen] = React.useState(false)
  const [createDialogParentId, setCreateDialogParentId] = React.useState<string | undefined>(undefined)

  const rootsQueryKey = ['node-roots', asAt]

  const { data: rootsData, isLoading } = useQuery({
    queryKey: rootsQueryKey,
    queryFn: async () => {
      // Root nodes have no parent — use empty parentId
      const result = await clients.node.getChildren({
        parentId: '',
        activeOnly: !asAt,
      })
      return result.nodes ?? []
    },
    staleTime: 30_000,
  })

  const roots = rootsData ?? []

  function handleAddChildNode(parentId: string) {
    setCreateDialogParentId(parentId)
    setCreateDialogOpen(true)
  }

  function handleAddRootNode() {
    setCreateDialogParentId(undefined)
    setCreateDialogOpen(true)
  }

  return (
    <PageShell>
      <PageHeader
        title="Nodes"
        description="Hierarchical reference data node browser with bi-temporal query support."
        actions={
          <Button onClick={handleAddRootNode} aria-label="Add Node">
            <Plus className="h-4 w-4 mr-1" />
            Add Node
          </Button>
        }
      />

      <CreateNodeDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
        defaultParentId={createDialogParentId}
      />

      <Card>
        <CardHeader>
          <div className="flex items-center gap-4">
            <CardTitle>Node Tree</CardTitle>
            <div className="flex items-center gap-2">
              <label className="text-sm text-muted-foreground whitespace-nowrap">
                As At:
              </label>
              <Input
                type="date"
                data-testid="temporal-date-picker"
                value={asAt}
                onChange={(e) => setAsAt(e.target.value)}
                className="h-8 w-40"
              />
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-2 py-4">
              {Array.from({ length: 5 }).map((_, i) => (
                <div key={i} className="h-8 w-full animate-pulse rounded bg-muted" />
              ))}
            </div>
          ) : roots.length === 0 ? (
            <div
              data-testid="empty-tree-state"
              className="flex h-32 flex-col items-center justify-center gap-2 text-muted-foreground"
            >
              <span className="text-sm font-medium">No nodes found</span>
              <span className="text-xs">No root nodes available for this tenant.</span>
            </div>
          ) : (
            <div className="font-mono text-sm">
              {roots.map((node) => (
                <NodeRow key={node.id} node={node} depth={0} asAt={asAt} onAddChild={handleAddChildNode} />
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </PageShell>
  )
}

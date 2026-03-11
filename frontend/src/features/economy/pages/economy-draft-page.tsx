import { useNavigate } from 'react-router-dom'
import { FileEdit, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { EmptyState } from '@/components/ui/empty-state'
import { useDraftStore } from '../lib/draft-manager'

const CHANGE_TYPE_LABELS: Record<string, string> = {
  add_saga: 'Add Saga',
  override_default: 'Override Default',
  add_instrument: 'Add Instrument',
  modify_account_type: 'Modify Account Type',
}

function formatTimestamp(ms: number): string {
  return new Date(ms).toLocaleString()
}

export function EconomyDraftPage() {
  const navigate = useNavigate()
  const changes = useDraftStore((s) => s.changes)
  const removeChange = useDraftStore((s) => s.removeChange)
  const clearAll = useDraftStore((s) => s.clearAll)

  const hasChanges = changes.length > 0

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Draft Changes</h1>
          <p className="mt-1 text-muted-foreground">
            {hasChanges
              ? `${changes.length} pending change${changes.length === 1 ? '' : 's'}`
              : 'No pending changes'}
          </p>
        </div>

        {hasChanges && (
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => clearAll()}>
              Clear All
            </Button>
            <Button onClick={() => navigate('/economy/edit', { state: { reviewDraft: true } })}>
              Review Draft
            </Button>
          </div>
        )}
      </div>

      {!hasChanges && (
        <div data-testid="draft-empty-state">
          <EmptyState
            icon={FileEdit}
            title="No draft changes"
            description="Changes you make in the economy editor will appear here before being applied."
          />
        </div>
      )}

      {hasChanges && (
        <div className="space-y-3">
          {changes.map((change) => (
            <div
              key={change.id}
              className="flex items-start justify-between rounded-lg border p-4 gap-4"
            >
              <div className="space-y-1 min-w-0">
                <div className="flex items-center gap-2">
                  <Badge variant="secondary" className="shrink-0">
                    {CHANGE_TYPE_LABELS[change.type] ?? change.type}
                  </Badge>
                </div>
                <p className="text-sm font-medium">{change.description}</p>
                <p className="text-xs text-muted-foreground">{formatTimestamp(change.createdAt)}</p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => removeChange(change.id)}
                className="shrink-0 text-destructive hover:text-destructive"
              >
                <Trash2 className="size-4 mr-1" />
                Revert
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

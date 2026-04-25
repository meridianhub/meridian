import { ClipboardList } from 'lucide-react'

export function AuditEmptyState() {
  return (
    <div
      data-testid="empty-state"
      className="flex flex-col items-center justify-center gap-3 py-12 px-4 text-center"
    >
      <ClipboardList className="size-10 text-muted-foreground" />
      <div className="flex flex-col gap-1.5 max-w-sm">
        <p className="text-sm font-medium text-foreground">No audit events yet</p>
        <p className="text-sm text-muted-foreground">
          Audit entries appear here when you create parties, update accounts, or run sagas.
          Try creating a party to see your first event.
        </p>
      </div>
    </div>
  )
}

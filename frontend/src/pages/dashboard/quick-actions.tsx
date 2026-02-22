import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export interface QuickAction {
  id: string
  label: string
  description?: string
  icon?: React.ReactNode
  onClick: () => void
  disabled?: boolean
}

interface QuickActionsProps {
  actions: QuickAction[]
  className?: string
}

export function QuickActions({ actions, className }: QuickActionsProps) {
  if (actions.length === 0) {
    return (
      <div className={cn('py-4 text-center text-sm text-muted-foreground', className)}>
        No quick actions available
      </div>
    )
  }

  return (
    <div className={cn('grid gap-2', className)}>
      {actions.map((action) => (
        <Button
          key={action.id}
          variant="outline"
          className="h-auto w-full justify-start gap-3 px-4 py-3 text-left"
          onClick={action.onClick}
          disabled={action.disabled}
        >
          {action.icon && (
            <span className="flex-shrink-0 text-muted-foreground">{action.icon}</span>
          )}
          <div className="min-w-0">
            <div className="text-sm font-medium">{action.label}</div>
            {action.description && (
              <div className="text-xs text-muted-foreground">{action.description}</div>
            )}
          </div>
        </Button>
      ))}
    </div>
  )
}

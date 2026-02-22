import * as React from "react"
import { type LucideIcon, FileQuestion } from "lucide-react"

import { cn } from "@/lib/utils"
import { Button } from "./button"

interface EmptyStateProps {
  icon?: LucideIcon
  title: string
  description?: string
  action?: {
    label: string
    onClick: () => void
  }
}

export function EmptyState({
  icon: Icon = FileQuestion,
  title,
  description,
  action,
}: EmptyStateProps) {
  return (
    <div
      data-slot="empty-state"
      className={cn(
        "flex min-h-[400px] flex-col items-center justify-center gap-4 px-4 py-8"
      )}
    >
      <div
        data-slot="empty-state-icon"
        className="text-muted-foreground"
      >
        <Icon className="size-12" />
      </div>

      <div
        data-slot="empty-state-content"
        className={cn("flex flex-col items-center gap-2 text-center")}
      >
        <h3 className="text-lg font-semibold text-foreground">
          {title}
        </h3>

        {description && (
          <p
            data-slot="empty-state-description"
            className="text-sm text-muted-foreground"
          >
            {description}
          </p>
        )}
      </div>

      {action && (
        <Button
          onClick={action.onClick}
          className="mt-2"
        >
          {action.label}
        </Button>
      )}
    </div>
  )
}

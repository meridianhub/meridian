import * as React from 'react'
import { cn } from '@/lib/utils'

export interface PageShellProps {
  children: React.ReactNode
  className?: string
}

export function PageShell({ children, className }: PageShellProps) {
  return (
    <div className={cn('space-y-6', className)}>
      {children}
    </div>
  )
}

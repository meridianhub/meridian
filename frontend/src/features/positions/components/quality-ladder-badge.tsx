import { Circle, CircleDot, RefreshCw, TrendingUp } from 'lucide-react'
import { cn } from '@/lib/utils'

type QualityLevel = 'ESTIMATE' | 'COEFFICIENT' | 'ACTUAL' | 'REVISED'

interface QualityConfig {
  icon: React.ElementType
  colorClass: string
  label: string
}

const QUALITY_LEVELS: Record<QualityLevel, QualityConfig> = {
  ESTIMATE: {
    icon: Circle,
    colorClass: 'text-warning-foreground bg-warning-muted border-warning/30',
    label: 'Estimate',
  },
  COEFFICIENT: {
    // COEFFICIENT is a data source mapping to ESTIMATE quality (ADR-0017),
    // so it takes the amber estimate-quality treatment
    icon: TrendingUp,
    colorClass: 'text-warning-foreground bg-warning-muted border-warning/30',
    label: 'Coefficient',
  },
  ACTUAL: {
    icon: CircleDot,
    colorClass: 'text-success-foreground bg-success-muted border-success/30',
    label: 'Actual',
  },
  REVISED: {
    icon: RefreshCw,
    colorClass: 'text-accent-foreground bg-accent border-accent-foreground/20',
    label: 'Revised',
  },
}

const DEFAULT_QUALITY = QUALITY_LEVELS.ESTIMATE

interface QualityLadderBadgeProps {
  quality: string
  showLabel?: boolean
}

export function QualityLadderBadge({ quality, showLabel = true }: QualityLadderBadgeProps) {
  const config = QUALITY_LEVELS[quality as QualityLevel] ?? DEFAULT_QUALITY
  const Icon = config.icon

  return (
    <span
      data-testid="quality-ladder-badge"
      data-quality={quality}
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        config.colorClass,
      )}
    >
      <Icon className="h-3 w-3" aria-hidden="true" />
      {showLabel && <span>{config.label}</span>}
    </span>
  )
}

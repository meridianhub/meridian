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
    colorClass: 'text-yellow-600 bg-yellow-50 border-yellow-200',
    label: 'Estimate',
  },
  COEFFICIENT: {
    icon: TrendingUp,
    colorClass: 'text-blue-600 bg-blue-50 border-blue-200',
    label: 'Coefficient',
  },
  ACTUAL: {
    icon: CircleDot,
    colorClass: 'text-green-600 bg-green-50 border-green-200',
    label: 'Actual',
  },
  REVISED: {
    icon: RefreshCw,
    colorClass: 'text-purple-600 bg-purple-50 border-purple-200',
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

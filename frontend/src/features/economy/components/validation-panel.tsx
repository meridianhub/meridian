import { AlertCircle, AlertTriangle } from 'lucide-react'
import type { ValidationError } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

interface ValidationPanelProps {
  errors: ValidationError[]
  warnings: ValidationError[]
  onLineClick: (path: string) => void
  onSuggestionApply: (path: string, suggestion: string) => void
}

export function ValidationPanel({
  errors,
  warnings,
  onLineClick,
  onSuggestionApply,
}: ValidationPanelProps) {
  if (errors.length === 0 && warnings.length === 0) {
    return null
  }

  const sortedErrors = [...errors].sort((a, b) => a.path.localeCompare(b.path))
  const sortedWarnings = [...warnings].sort((a, b) => a.path.localeCompare(b.path))

  return (
    <ul className="space-y-1 text-sm" role="list">
      {sortedErrors.map((error, i) => (
        <ValidationItem
          key={`error-${i}`}
          item={error}
          severity="error"
          onLineClick={onLineClick}
          onSuggestionApply={onSuggestionApply}
        />
      ))}
      {sortedWarnings.map((warning, i) => (
        <ValidationItem
          key={`warning-${i}`}
          item={warning}
          severity="warning"
          onLineClick={onLineClick}
          onSuggestionApply={onSuggestionApply}
        />
      ))}
    </ul>
  )
}

function ValidationItem({
  item,
  severity,
  onLineClick,
  onSuggestionApply,
}: {
  item: ValidationError
  severity: 'error' | 'warning'
  onLineClick: (path: string) => void
  onSuggestionApply: (path: string, suggestion: string) => void
}) {
  const Icon = severity === 'error' ? AlertCircle : AlertTriangle
  const iconColor = severity === 'error' ? 'text-destructive' : 'text-yellow-500'

  return (
    <li className="flex items-start gap-2 rounded-md px-2 py-1.5">
      <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${iconColor}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          {item.path && (
            <button
              type="button"
              className="shrink-0 font-mono text-xs text-muted-foreground underline decoration-dotted hover:text-foreground"
              onClick={() => onLineClick(item.path)}
            >
              {item.path}
            </button>
          )}
          <span>{item.message}</span>
        </div>
        {item.suggestion && (
          <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
            <span>
              Did you mean <strong>{item.suggestion}</strong>?
            </span>
            <button
              type="button"
              className="rounded border px-1.5 py-0.5 text-xs hover:bg-muted"
              onClick={() => onSuggestionApply(item.path, item.suggestion)}
            >
              Apply
            </button>
          </div>
        )}
      </div>
    </li>
  )
}

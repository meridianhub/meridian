import { useState, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { StarlarkEditor, type ValidationError, type ComplexityMetrics } from '@/components/shared/starlark-editor'
import { Breadcrumbs } from '@/components/shared'
import { useApiClients } from '@/api/context'
import { SagaStatus, ErrorCategory } from '@/api/gen/meridian/saga/v1/saga_registry_pb'
import type { SagaDefinition } from '@/api/gen/meridian/saga/v1/saga_registry_pb'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function sagaStatusLabel(status: SagaStatus): string {
  switch (status) {
    case SagaStatus.ACTIVE:
      return 'ACTIVE'
    case SagaStatus.DRAFT:
      return 'DRAFT'
    case SagaStatus.DEPRECATED:
      return 'DEPRECATED'
    default:
      return 'UNKNOWN'
  }
}

function errorCategoryLabel(category: ErrorCategory): ValidationError['category'] {
  switch (category) {
    case ErrorCategory.SYNTAX:
      return 'SYNTAX'
    case ErrorCategory.UNDEFINED_HANDLER:
    case ErrorCategory.TYPE_MISMATCH:
    case ErrorCategory.RUNTIME:
    case ErrorCategory.TIMEOUT:
      return 'ERROR'
    default:
      return 'ERROR'
  }
}

function isReadOnly(saga: SagaDefinition): boolean {
  // Active system sagas are read-only
  // Active non-system sagas are also read-only (script is immutable once activated)
  return saga.isSystem || saga.status === SagaStatus.ACTIVE || saga.status === SagaStatus.DEPRECATED
}

// ---------------------------------------------------------------------------
// Loading Skeleton
// ---------------------------------------------------------------------------

function DetailSkeleton() {
  return (
    <div data-testid="detail-skeleton" className="flex flex-col gap-6 animate-pulse">
      <div>
        <div className="h-9 w-64 bg-muted rounded" />
        <div className="mt-2 h-5 w-80 bg-muted rounded" />
      </div>
      <div className="h-64 bg-muted rounded" />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Split Pane - platform default vs tenant override diff
// ---------------------------------------------------------------------------

interface SplitPaneProps {
  platformSaga: SagaDefinition
  tenantSaga: SagaDefinition
  tenantScript: string
  onTenantScriptChange: (value: string) => void
  validationErrors: ValidationError[]
  complexityMetrics?: ComplexityMetrics
}

function SplitPane({
  platformSaga,
  tenantSaga,
  tenantScript,
  onTenantScriptChange,
  validationErrors,
  complexityMetrics,
}: SplitPaneProps) {
  return (
    <div data-testid="split-pane" className="grid grid-cols-2 gap-4">
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium text-muted-foreground">Platform Default</span>
          <StatusBadge status={sagaStatusLabel(platformSaga.status)} />
        </div>
        <StarlarkEditor
          value={platformSaga.script}
          onChange={() => {}}
          readOnly={true}
          className="min-h-[400px]"
        />
      </div>
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium text-muted-foreground">Tenant Override</span>
          <StatusBadge status={sagaStatusLabel(tenantSaga.status)} />
        </div>
        <StarlarkEditor
          value={tenantScript}
          onChange={onTenantScriptChange}
          readOnly={isReadOnly(tenantSaga)}
          errors={validationErrors}
          complexityMetrics={complexityMetrics}
          className="min-h-[400px]"
        />
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main Detail Page
// ---------------------------------------------------------------------------

export function StarlarkDetailPage() {
  const { definitionId } = useParams<{ definitionId: string }>()
  const { sagaRegistry } = useApiClients()
  const qc = useQueryClient()

  const [script, setScript] = useState<string | null>(null)
  const [validationErrors, setValidationErrors] = useState<ValidationError[]>([])
  const [complexityMetrics, setComplexityMetrics] = useState<ComplexityMetrics | undefined>(undefined)

  // Fetch the specific saga definition
  const { data: sagaData, isLoading } = useQuery({
    queryKey: ['starlark-config', definitionId],
    queryFn: async () => {
      const response = await sagaRegistry.getSaga({ id: definitionId ?? '' })
      return response.saga
    },
    enabled: !!definitionId,
  })

  // For non-system sagas: also fetch the platform default (system saga with same name)
  // to show the split pane comparison
  const { data: activeSagaData } = useQuery({
    queryKey: ['starlark-config', 'active', sagaData?.name],
    queryFn: async () => {
      if (!sagaData?.name) return null
      try {
        const response = await sagaRegistry.getActiveSaga({ name: sagaData.name })
        return response
      } catch {
        return null
      }
    },
    enabled: !!sagaData?.name && !sagaData.isSystem,
  })

  const effectiveScript = script ?? sagaData?.script ?? ''

  // Validate saga
  const validateMutation = useMutation({
    mutationFn: async () => {
      if (!sagaData) return null
      const response = await sagaRegistry.validateSaga({
        sagaName: sagaData.name,
        script: effectiveScript,
        version: String(sagaData.version),
        blockOnFailure: false,
      })
      return response
    },
    onSuccess: (response) => {
      if (!response) return
      const errors: ValidationError[] = (response.errors ?? []).map((e) => ({
        line: e.line,
        column: e.column,
        message: e.message,
        category: errorCategoryLabel(e.category),
      }))
      setValidationErrors(errors)
      if (response.metrics) {
        setComplexityMetrics({
          handlerCalls: response.metrics.handlerCallCount,
          operations: response.metrics.operationCount,
          estimatedDurationMs: response.metrics.estimatedDurationMs,
          complexityScore: response.metrics.complexityScore,
        })
      }
    },
  })

  // Activate saga
  const activateMutation = useMutation({
    mutationFn: async () => {
      if (!sagaData?.id) return null
      return sagaRegistry.activateSaga({ id: sagaData.id })
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['starlark-config', definitionId] })
    },
  })

  // Deprecate saga
  const deprecateMutation = useMutation({
    mutationFn: async () => {
      if (!sagaData?.id) return null
      return sagaRegistry.deprecateSaga({ id: sagaData.id, successorId: '' })
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['starlark-config', definitionId] })
    },
  })

  const handleScriptChange = useCallback((value: string) => {
    setScript(value)
    // Clear errors when script changes
    setValidationErrors([])
    setComplexityMetrics(undefined)
  }, [])

  if (isLoading) {
    return <DetailSkeleton />
  }

  if (!sagaData) {
    return (
      <div className="p-6">
        <p className="text-muted-foreground">Saga definition not found.</p>
      </div>
    )
  }

  // Determine if we should show split pane:
  // - The current saga is NOT a system saga (it's a tenant override or draft)
  // - There's an active system saga with the same name
  const platformDefault = activeSagaData?.saga
  const showSplitPane =
    !sagaData.isSystem &&
    platformDefault != null &&
    platformDefault.id !== sagaData.id

  const readOnly = isReadOnly(sagaData)

  return (
    <div className="flex flex-col gap-6">
      {/* Breadcrumb navigation */}
      <Breadcrumbs
        items={[
          { label: 'Starlark Config', href: '/starlark' },
          { label: sagaData.name },
        ]}
      />

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-3xl font-bold tracking-tight font-mono">
              {sagaData.name}
            </h1>
            <StatusBadge status={sagaStatusLabel(sagaData.status)} />
          </div>
          {sagaData.displayName && (
            <p className="mt-1 text-muted-foreground">{sagaData.displayName}</p>
          )}
          {sagaData.description && (
            <p className="mt-1 text-sm text-muted-foreground">{sagaData.description}</p>
          )}
          <div className="mt-2 flex items-center gap-4 text-sm text-muted-foreground">
            <span>Version {sagaData.version}</span>
            {sagaData.updatedAt && (
              <span>
                Updated <TimeDisplay timestamp={sagaData.updatedAt} format="relative" />
              </span>
            )}
          </div>
        </div>

        {/* Action buttons */}
        <div className="flex items-center gap-2 shrink-0">
          {(!readOnly || showSplitPane) && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => validateMutation.mutate()}
              disabled={validateMutation.isPending}
            >
              Validate
            </Button>
          )}
          {sagaData.status === SagaStatus.DRAFT && (
            <Button
              variant="default"
              size="sm"
              onClick={() => activateMutation.mutate()}
              disabled={activateMutation.isPending}
            >
              Activate
            </Button>
          )}
          {sagaData.status === SagaStatus.ACTIVE && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => deprecateMutation.mutate()}
              disabled={deprecateMutation.isPending}
            >
              Deprecate
            </Button>
          )}
        </div>
      </div>

      {/* Editor area */}
      <Card className="p-6">
        {showSplitPane ? (
          <SplitPane
            platformSaga={platformDefault}
            tenantSaga={sagaData}
            tenantScript={effectiveScript}
            onTenantScriptChange={handleScriptChange}
            validationErrors={validationErrors}
            complexityMetrics={complexityMetrics}
          />
        ) : (
          <StarlarkEditor
            value={effectiveScript}
            onChange={handleScriptChange}
            readOnly={readOnly}
            errors={validationErrors}
            complexityMetrics={complexityMetrics}
            className="min-h-[400px]"
          />
        )}
      </Card>
    </div>
  )
}

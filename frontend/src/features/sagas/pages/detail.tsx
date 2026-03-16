import { useState, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { StarlarkEditor, type ValidationError, type ComplexityMetrics } from '@/features/sagas/components/starlark-editor'
import { Breadcrumbs, DetailSkeleton, ErrorState, PageShell, PageHeader } from '@/shared'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { SagaStatus, ErrorCategory } from '@/api/gen/meridian/saga/v1/saga_registry_pb'
import type { SagaDefinition } from '@/api/gen/meridian/saga/v1/saga_registry_pb'
import { usePageTitle } from '@/hooks/use-page-title'
import { useActiveSaga } from '../hooks'

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
// Main Detail Page
// ---------------------------------------------------------------------------

export function StarlarkDetailPage() {
  const { sagaName } = useParams<{ sagaName: string }>()
  usePageTitle(sagaName ? `Saga ${sagaName}` : 'Saga')
  const { sagaRegistry } = useApiClients()
  const tenantSlug = useTenantSlug()
  const qc = useQueryClient()
  const sagaQueryRoot = tenantSlug
    ? [...tenantKeys.sagas(tenantSlug), sagaName]
    : ['starlark-config', sagaName]

  const [script, setScript] = useState<string | null>(null)
  const [validationErrors, setValidationErrors] = useState<ValidationError[]>([])
  const [complexityMetrics, setComplexityMetrics] = useState<ComplexityMetrics | undefined>(undefined)

  const { data: activeSagaResponse, isLoading } = useActiveSaga(sagaName)

  const sagaData = activeSagaResponse?.saga

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
      void qc.invalidateQueries({ queryKey: sagaQueryRoot })
    },
  })

  // Deprecate saga
  const deprecateMutation = useMutation({
    mutationFn: async () => {
      if (!sagaData?.id) return null
      return sagaRegistry.deprecateSaga({ id: sagaData.id, successorId: '' })
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: sagaQueryRoot })
    },
  })

  const handleScriptChange = useCallback((value: string) => {
    setScript(value)
    // Clear errors when script changes
    setValidationErrors([])
    setComplexityMetrics(undefined)
  }, [])

  if (isLoading) {
    return (
      <PageShell>
        <DetailSkeleton tabCount={0} fieldCount={2} />
      </PageShell>
    )
  }

  if (!sagaData) {
    return (
      <PageShell>
        <Breadcrumbs items={[
          { label: 'Economy', href: '/economy' },
          { label: 'Starlark Config', href: '/starlark-config' },
        ]} />
        <ErrorState title="Saga not found" message="This saga definition could not be found." />
      </PageShell>
    )
  }

  const readOnly = isReadOnly(sagaData)

  const actionButtons = (
    <>
      {!readOnly && (
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
    </>
  )

  return (
    <PageShell>
      {/* Breadcrumb navigation */}
      <Breadcrumbs
        items={[
          { label: 'Economy', href: '/economy' },
          { label: 'Starlark Config', href: '/starlark-config' },
          { label: sagaData.name },
        ]}
      />

      {/* Header */}
      <PageHeader
        title={sagaData.name}
        description={sagaData.description || undefined}
        actions={actionButtons}
      />

      <div className="flex items-center gap-3">
        <StatusBadge status={sagaStatusLabel(sagaData.status)} />
        {sagaData.displayName && (
          <span className="text-muted-foreground">{sagaData.displayName}</span>
        )}
        <span className="text-sm text-muted-foreground">Version {sagaData.version}</span>
        {sagaData.updatedAt && (
          <span className="text-sm text-muted-foreground">
            Updated <TimeDisplay timestamp={sagaData.updatedAt} format="relative" />
          </span>
        )}
      </div>

      {/* Editor area */}
      <Card className="p-6">
        <StarlarkEditor
          value={effectiveScript}
          onChange={handleScriptChange}
          readOnly={readOnly}
          errors={validationErrors}
          complexityMetrics={complexityMetrics}
          className="min-h-[400px]"
        />
      </Card>
    </PageShell>
  )
}

import * as React from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Card } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { StatusBadge } from '@/components/shared/status-badge'
import { DetailSkeleton } from '@/components/shared/detail-skeleton'
import { useApiClients } from '@/api/context'

// ─── Types ───────────────────────────────────────────────────────────────────

interface CelTransform {
  inboundCel: string
  outboundCel: string
}

interface EnumMapping {
  values: Record<string, string>
  fallback: string
  outboundFallback: string
}

interface AttributeFlatten {
  sourceKeys: string[]
  targetField: string
}

interface FieldTransform {
  cel?: CelTransform
  enumMapping?: EnumMapping
  dateFormat?: string
  defaultValue?: string
  attributeFlatten?: AttributeFlatten
}

interface FieldCorrespondence {
  externalPath: string
  internalPath: string
  transform?: FieldTransform | null
}

interface FieldMappingTrace {
  sourcePath: string
  targetPath: string
  sourceValue: string
  transformedValue: string
  transformType: string
}

interface DryRunValidationResult {
  passed: boolean
  errors: string[]
}

interface MappingDetail {
  id: string
  name: string
  targetService: string
  targetRpc: string
  version: number
  status: string
  fields: FieldCorrespondence[]
  inboundValidationCel: string
  outboundValidationCel: string
  isBatch: boolean
  batchTargetPath: string
  createdAt?: { seconds: bigint | number; nanos?: number }
  updatedAt?: { seconds: bigint | number; nanos?: number }
}

interface DryRunResult {
  transformedJson: string
  idempotencyKey: string
  validationResult: DryRunValidationResult
  executionTimeMs: number
  fieldMappingTrace: FieldMappingTrace[]
  transformError: string
}

// ─── PII masking ─────────────────────────────────────────────────────────────

const PII_PATTERNS = [
  /card[._-]?number/i,
  /cvv/i,
  /ssn/i,
  /social[._-]?security/i,
  /password/i,
  /secret/i,
  /token/i,
  /account[._-]?number/i,
]

function maskPiiValue(key: string, value: string): string {
  if (PII_PATTERNS.some((re) => re.test(key))) {
    return '"****"'
  }
  return value
}

function applyPiiMask(json: string): string {
  try {
    const obj = JSON.parse(json)
    const masked = maskObjectPii(obj)
    return JSON.stringify(masked, null, 2)
  } catch {
    return json
  }
}

function maskObjectPii(obj: unknown): unknown {
  if (obj === null || obj === undefined) return obj
  if (typeof obj !== 'object') return obj
  if (Array.isArray(obj)) return obj.map((v) => maskObjectPii(v))

  const result: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(obj as Record<string, unknown>)) {
    if (typeof value === 'string' && PII_PATTERNS.some((re) => re.test(key))) {
      result[key] = '****'
    } else {
      result[key] = maskObjectPii(value)
    }
  }
  return result
}

// ─── Field Transform Badge ────────────────────────────────────────────────────

function TransformTypeBadge({ transform }: { transform?: FieldTransform | null }) {
  if (!transform) return <span className="text-xs text-muted-foreground">—</span>
  if (transform.cel) return <Badge variant="secondary">CEL</Badge>
  if (transform.enumMapping) return <Badge variant="secondary">Enum</Badge>
  if (transform.dateFormat) return <Badge variant="secondary">Date</Badge>
  if (transform.defaultValue !== undefined) return <Badge variant="secondary">Default</Badge>
  if (transform.attributeFlatten) return <Badge variant="secondary">Flatten</Badge>
  return <span className="text-xs text-muted-foreground">—</span>
}

// ─── Field Mapper Tab ─────────────────────────────────────────────────────────

function FieldMapperTab({ mapping }: { mapping: MappingDetail }) {
  const fields = mapping.fields ?? []

  return (
    <div className="space-y-4">
      <div className="text-sm text-muted-foreground">
        {fields.length} field{fields.length !== 1 ? 's' : ''} mapped
      </div>

      {fields.length === 0 ? (
        <p className="text-sm text-muted-foreground">No field correspondences defined.</p>
      ) : (
        <div className="rounded border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/40">
                <th className="px-4 py-2 text-left font-medium">External Path (gjson)</th>
                <th className="px-4 py-2 text-left font-medium">Internal Path (proto)</th>
                <th className="px-4 py-2 text-left font-medium">Transform</th>
              </tr>
            </thead>
            <tbody>
              {fields.map((field, idx) => (
                <tr key={idx} className="border-b last:border-0 hover:bg-muted/20">
                  <td className="px-4 py-2 font-mono text-xs">{field.externalPath}</td>
                  <td className="px-4 py-2 font-mono text-xs">{field.internalPath}</td>
                  <td className="px-4 py-2">
                    <TransformTypeBadge transform={field.transform} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// ─── Dry Run Playground Tab ───────────────────────────────────────────────────

function DryRunTab({ mapping }: { mapping: MappingDetail }) {
  const clients = useApiClients()
  const [sampleJson, setSampleJson] = React.useState('{\n  \n}')
  const [direction, setDirection] = React.useState<'inbound' | 'outbound'>('inbound')
  const [maskPii, setMaskPii] = React.useState(false)

  const dryRun = useMutation({
    mutationFn: async (): Promise<DryRunResult> => {
      const response = await clients.mapping.dryRunMapping({
        mappingName: mapping.name,
        mappingVersion: mapping.version,
        direction,
        sampleJson,
      })
      return response as DryRunResult
    },
  })

  const outputJson = React.useMemo(() => {
    if (!dryRun.data?.transformedJson) return ''
    if (maskPii) return applyPiiMask(dryRun.data.transformedJson)
    try {
      return JSON.stringify(JSON.parse(dryRun.data.transformedJson), null, 2)
    } catch {
      return dryRun.data.transformedJson
    }
  }, [dryRun.data, maskPii])

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <div className="flex gap-2">
          <Button
            variant={direction === 'inbound' ? 'default' : 'outline'}
            size="sm"
            onClick={() => setDirection('inbound')}
          >
            Inbound
          </Button>
          <Button
            variant={direction === 'outbound' ? 'default' : 'outline'}
            size="sm"
            onClick={() => setDirection('outbound')}
          >
            Outbound
          </Button>
        </div>

        <label className="flex cursor-pointer items-center gap-2 text-sm">
          <input
            type="checkbox"
            role="checkbox"
            aria-label="Mask PII"
            checked={maskPii}
            onChange={(e) => setMaskPii(e.target.checked)}
            className="h-4 w-4"
          />
          Mask PII
        </label>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-2">
          <label className="text-sm font-medium">Input JSON</label>
          <textarea
            className="h-48 w-full rounded border border-input bg-background px-3 py-2 font-mono text-xs outline-none focus:border-ring"
            value={sampleJson}
            onChange={(e) => setSampleJson(e.target.value)}
            placeholder='{"key": "value"}'
          />
        </div>

        <div className="space-y-2">
          <label className="text-sm font-medium">Output JSON</label>
          <pre
            data-testid="dry-run-output"
            className="h-48 overflow-auto rounded border border-input bg-muted/30 px-3 py-2 font-mono text-xs"
          >
            {dryRun.isPending ? (
              <span className="text-muted-foreground">Running...</span>
            ) : dryRun.isError ? (
              <span className="text-destructive">
                {dryRun.error instanceof Error ? dryRun.error.message : 'Error'}
              </span>
            ) : outputJson ? (
              outputJson
            ) : (
              <span className="text-muted-foreground">Output will appear here</span>
            )}
          </pre>
        </div>
      </div>

      {dryRun.data && (
        <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
          {dryRun.data.validationResult && (
            <span>
              Validation:{' '}
              <span
                className={
                  dryRun.data.validationResult.passed ? 'text-green-700' : 'text-destructive'
                }
              >
                {dryRun.data.validationResult.passed ? 'Passed' : 'Failed'}
              </span>
            </span>
          )}
          {dryRun.data.executionTimeMs > 0 && (
            <span>{dryRun.data.executionTimeMs}ms</span>
          )}
          {dryRun.data.idempotencyKey && (
            <span>
              Idempotency Key:{' '}
              <code className="rounded bg-muted px-1">{dryRun.data.idempotencyKey}</code>
            </span>
          )}
          {dryRun.data.transformError && (
            <span className="text-destructive">Transform error: {dryRun.data.transformError}</span>
          )}
        </div>
      )}

      {dryRun.data?.validationResult &&
        !dryRun.data.validationResult.passed &&
        dryRun.data.validationResult.errors.length > 0 && (
          <div className="rounded border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">
            {dryRun.data.validationResult.errors.map((err, i) => (
              <div key={i}>{err}</div>
            ))}
          </div>
        )}

      <Button onClick={() => dryRun.mutate()} disabled={dryRun.isPending}>
        {dryRun.isPending ? 'Running...' : 'Run'}
      </Button>

      {dryRun.data && dryRun.data.fieldMappingTrace.length > 0 && (
        <div data-testid="field-mapping-trace" className="space-y-2">
          <h3 className="text-sm font-medium">Field Mapping Trace</h3>
          <div className="rounded border">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b bg-muted/40">
                  <th className="px-3 py-2 text-left font-medium">Source Path</th>
                  <th className="px-3 py-2 text-left font-medium">Target Path</th>
                  <th className="px-3 py-2 text-left font-medium">Source Value</th>
                  <th className="px-3 py-2 text-left font-medium">Transformed Value</th>
                  <th className="px-3 py-2 text-left font-medium">Transform</th>
                </tr>
              </thead>
              <tbody>
                {dryRun.data.fieldMappingTrace.map((trace, idx) => (
                  <tr key={idx} className="border-b last:border-0 hover:bg-muted/20">
                    <td className="px-3 py-2 font-mono">{trace.sourcePath}</td>
                    <td className="px-3 py-2 font-mono">{trace.targetPath}</td>
                    <td className="px-3 py-2 font-mono">{trace.sourceValue}</td>
                    <td className="px-3 py-2 font-mono">
                      {maskPii
                        ? maskPiiValue(trace.targetPath, trace.transformedValue)
                        : trace.transformedValue}
                    </td>
                    <td className="px-3 py-2">{trace.transformType}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

// ─── Overview Tab ─────────────────────────────────────────────────────────────

function OverviewTab({ mapping }: { mapping: MappingDetail }) {
  return (
    <dl className="grid grid-cols-2 gap-4 text-sm">
      <div>
        <dt className="font-medium text-muted-foreground">Target Service</dt>
        <dd className="mt-1 font-mono text-xs">{mapping.targetService}</dd>
      </div>
      <div>
        <dt className="font-medium text-muted-foreground">Target RPC</dt>
        <dd className="mt-1 font-mono text-xs">{mapping.targetRpc}</dd>
      </div>
      <div>
        <dt className="font-medium text-muted-foreground">Version</dt>
        <dd className="mt-1">v{mapping.version}</dd>
      </div>
      <div>
        <dt className="font-medium text-muted-foreground">Batch Mode</dt>
        <dd className="mt-1">{mapping.isBatch ? 'Yes' : 'No'}</dd>
      </div>
      {mapping.isBatch && mapping.batchTargetPath && (
        <div className="col-span-2">
          <dt className="font-medium text-muted-foreground">Batch Target Path</dt>
          <dd className="mt-1 font-mono text-xs">{mapping.batchTargetPath}</dd>
        </div>
      )}
      {mapping.inboundValidationCel && (
        <div className="col-span-2">
          <dt className="font-medium text-muted-foreground">Inbound Validation CEL</dt>
          <dd className="mt-1 rounded bg-muted/30 px-3 py-2 font-mono text-xs">
            {mapping.inboundValidationCel}
          </dd>
        </div>
      )}
      {mapping.outboundValidationCel && (
        <div className="col-span-2">
          <dt className="font-medium text-muted-foreground">Outbound Validation CEL</dt>
          <dd className="mt-1 rounded bg-muted/30 px-3 py-2 font-mono text-xs">
            {mapping.outboundValidationCel}
          </dd>
        </div>
      )}
    </dl>
  )
}

// ─── Mapping Header ───────────────────────────────────────────────────────────

function MappingHeader({ mapping }: { mapping: MappingDetail }) {
  const statusDisplay = mapping.status.replace(/^MAPPING_STATUS_/, '')

  return (
    <div className="flex items-center justify-between p-6">
      <div>
        <h2 className="text-xl font-semibold">{mapping.name}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          v{mapping.version} · {mapping.targetRpc}
        </p>
      </div>
      <StatusBadge status={statusDisplay} />
    </div>
  )
}

// ─── Main Page Component ──────────────────────────────────────────────────────

export function MappingDetailPage() {
  const { mappingId } = useParams<{ mappingId: string }>()
  const clients = useApiClients()

  const { data, isLoading, isError } = useQuery({
    queryKey: ['mapping', mappingId],
    queryFn: async () => {
      const response = await clients.mapping.getMapping({ id: mappingId! })
      return response.mapping as MappingDetail
    },
    enabled: !!mappingId,
  })

  if (!mappingId) {
    return <div className="p-6 text-destructive">Mapping ID not found</div>
  }

  if (isLoading) {
    return <DetailSkeleton fieldCount={4} tabCount={3} showBackNav={false} />
  }

  if (isError || !data) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Mapping Details</h1>
        </div>
        <Card className="p-6">
          <p className="text-destructive">Failed to load mapping.</p>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Mapping Details</h1>
      </div>

      <Card>
        <MappingHeader mapping={data} />
      </Card>

      <Card>
        <Tabs defaultValue="overview" className="w-full">
          <TabsList className="grid w-full grid-cols-3 border-b rounded-none">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="field-mapper">Field Mapper</TabsTrigger>
            <TabsTrigger value="dry-run">Dry Run</TabsTrigger>
          </TabsList>

          <div className="p-6">
            <TabsContent value="overview" className="mt-0">
              <OverviewTab mapping={data} />
            </TabsContent>

            <TabsContent value="field-mapper" className="mt-0">
              <FieldMapperTab mapping={data} />
            </TabsContent>

            <TabsContent value="dry-run" className="mt-0">
              <DryRunTab mapping={data} />
            </TabsContent>
          </div>
        </Tabs>
      </Card>
    </div>
  )
}

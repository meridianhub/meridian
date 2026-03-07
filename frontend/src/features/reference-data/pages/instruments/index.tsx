import * as React from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { useMutation } from '@tanstack/react-query'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { CELEditor } from '@/features/sagas/components/cel-editor'
import { useApiClients } from '@/api/context'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { AlertTriangle } from 'lucide-react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { referenceKeys } from '@/lib/query-keys'
import {
  InstrumentStatus,
  Dimension,
  type InstrumentDefinition,
} from '@/api/gen/meridian/reference_data/v1/instrument_pb'
import { RegisterInstrumentDialog } from './register-instrument-dialog'
import { ExecutionContextTab } from '../../components/execution-context-tab'

const DIMENSION_LABELS: Record<number, string> = {
  0: 'Unspecified',
  1: 'Currency',
  2: 'Energy',
  3: 'Mass',
  4: 'Volume',
  5: 'Time',
  6: 'Compute',
  7: 'Carbon',
  8: 'Data',
  9: 'Count',
}

const STATUS_LABELS: Record<number, string> = {
  0: 'Unspecified',
  1: 'Draft',
  2: 'Active',
  3: 'Deprecated',
}

const INSTRUMENT_STATUS_BADGE_MAP: Record<number, string> = {
  0: 'DRAFT',
  1: 'DRAFT',
  2: 'ACTIVE',
  3: 'DEPRECATED',
}

interface ListInstrumentsParams {
  pageToken?: string
  pageSize: number
  filters?: Record<string, string>
}

interface ListInstrumentsResult {
  items: InstrumentDefinition[]
  nextPageToken?: string
}

interface CELPlaygroundResult {
  compileErrors: string[]
  validationResult: boolean
  fungibilityKey: string
  errorMessage: string
}

export function InstrumentsPage() {
  const clients = useApiClients()
  const [registerDialogOpen, setRegisterDialogOpen] = React.useState(false)
  const [selectedInstrument, setSelectedInstrument] = React.useState<InstrumentDefinition | null>(null)

  const [validationExpression, setValidationExpression] = React.useState('amount > 0')
  const [fungibilityExpression, setFungibilityExpression] = React.useState('instrument_code')
  const [errorExpression, setErrorExpression] = React.useState('')
  const [celResult, setCelResult] = React.useState<CELPlaygroundResult | null>(null)

  const evaluateMutation = useMutation({
    mutationFn: async () => {
      const response = await clients.referenceData.evaluateInstrument({
        validationExpression,
        fungibilityKeyExpression: fungibilityExpression,
        errorMessageExpression: errorExpression,
        testAttributes: {},
        testAmount: '',
        testValidFrom: undefined,
        testValidTo: undefined,
        testSource: '',
      })
      return response
    },
    onSuccess: (data) => {
      setCelResult({
        compileErrors: data.compileErrors ?? [],
        validationResult: data.validationResult,
        fungibilityKey: data.fungibilityKey,
        errorMessage: data.errorMessage,
      })
    },
  })

  const columns: ColumnDef<InstrumentDefinition>[] = [
    {
      accessorKey: 'code',
      header: 'Code',
      cell: ({ row }) => (
        <span className="font-mono text-sm font-medium">{row.original.code}</span>
      ),
    },
    {
      accessorKey: 'dimension',
      header: 'Dimension',
      cell: ({ row }) => (
        <span className="text-sm">{DIMENSION_LABELS[row.original.dimension] ?? 'Unknown'}</span>
      ),
    },
    {
      accessorKey: 'precision',
      header: 'Precision',
      cell: ({ row }) => {
        const p = row.original.precision
        return (
          <div className="flex items-center gap-1.5">
            <span className="text-sm">{p}</span>
            {p > 12 && (
              <span data-testid="precision-overflow-warning" title="High precision may cause display overflow">
                <AlertTriangle className="h-3.5 w-3.5 text-yellow-500" />
              </span>
            )}
          </div>
        )
      },
    },
    {
      accessorKey: 'status',
      header: 'Status',
      cell: ({ row }) => {
        const badgeKey = INSTRUMENT_STATUS_BADGE_MAP[row.original.status] ?? 'DRAFT'
        return <StatusBadge status={badgeKey} />
      },
    },
    {
      accessorKey: 'displayName',
      header: 'Display Name',
      cell: ({ row }) => <span className="text-sm">{row.original.displayName || '—'}</span>,
    },
  ]

  const filters = [
    {
      field: 'status',
      label: 'Status',
      type: 'select' as const,
      options: [
        { label: STATUS_LABELS[InstrumentStatus.DRAFT], value: String(InstrumentStatus.DRAFT) },
        { label: STATUS_LABELS[InstrumentStatus.ACTIVE], value: String(InstrumentStatus.ACTIVE) },
        { label: STATUS_LABELS[InstrumentStatus.DEPRECATED], value: String(InstrumentStatus.DEPRECATED) },
      ],
    },
    {
      field: 'dimension',
      label: 'Dimension',
      type: 'select' as const,
      options: Object.entries(DIMENSION_LABELS)
        .filter(([k]) => Number(k) !== 0)
        .map(([k, label]) => ({ label, value: k })),
    },
  ]

  const queryFn = async (params: ListInstrumentsParams): Promise<ListInstrumentsResult> => {
    const statusValue = params.filters?.status
    const dimValue = params.filters?.dimension

    const response = await clients.referenceData.listInstruments({
      statusFilter: statusValue ? (Number(statusValue) as InstrumentStatus) : InstrumentStatus.UNSPECIFIED,
      dimensionFilter: dimValue ? (Number(dimValue) as Dimension) : Dimension.UNSPECIFIED,
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
    })

    return {
      items: response.instruments ?? [],
      nextPageToken: response.nextPageToken,
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Instruments</h1>
          <p className="mt-2 text-muted-foreground">
            Reference data instrument definitions with CEL validation expressions.
          </p>
        </div>
        <Button onClick={() => setRegisterDialogOpen(true)}>
          Register Instrument
        </Button>
      </div>

      <RegisterInstrumentDialog
        open={registerDialogOpen}
        onOpenChange={setRegisterDialogOpen}
      />

      <Card className="p-6">
        <DataTable
          queryKey={referenceKeys.instruments()}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          filters={filters}
          onRowClick={(inst) => setSelectedInstrument(inst)}
        />
      </Card>

      {selectedInstrument && (
        <Tabs defaultValue="overview">
          <TabsList>
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="executions">Executions</TabsTrigger>
          </TabsList>
          <TabsContent value="executions">
            <ExecutionContextTab entityType="instrument" entityCode={selectedInstrument.code} />
          </TabsContent>
          <TabsContent value="overview">
      <Card data-testid="cel-playground">
        <CardHeader>
          <CardTitle>CEL Playground</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <label className="text-sm font-medium">Validation Expression</label>
            <CELEditor
              value={validationExpression}
              onChange={setValidationExpression}
              context="validation"
              errors={celResult?.compileErrors.map((msg) => ({ message: msg })) ?? []}
            />
          </div>

          <div className="space-y-2">
            <label className="text-sm font-medium">Fungibility Key Expression</label>
            <CELEditor
              value={fungibilityExpression}
              onChange={setFungibilityExpression}
              context="bucketKey"
              showVariables={false}
            />
          </div>

          <div className="space-y-2">
            <label className="text-sm font-medium">Error Message Expression</label>
            <CELEditor
              value={errorExpression}
              onChange={setErrorExpression}
              context="value"
              showVariables={false}
            />
          </div>

          <Button
            onClick={() => evaluateMutation.mutate()}
            disabled={evaluateMutation.isPending}
          >
            {evaluateMutation.isPending ? 'Evaluating…' : 'Evaluate'}
          </Button>

          {celResult && (
            <div data-testid="cel-result" className="rounded border bg-muted/30 p-4 space-y-2">
              <div className="flex items-center gap-2 text-sm">
                <span className="font-medium">Validation:</span>
                <span className={celResult.validationResult ? 'text-green-600' : 'text-red-600'}>
                  {celResult.validationResult ? 'PASS' : 'FAIL'}
                </span>
              </div>
              {celResult.fungibilityKey && (
                <div className="text-sm">
                  <span className="font-medium">Fungibility Key:</span>{' '}
                  <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
                    {celResult.fungibilityKey}
                  </code>
                </div>
              )}
              {celResult.errorMessage && (
                <div className="text-sm text-red-600">
                  <span className="font-medium">Error:</span> {celResult.errorMessage}
                </div>
              )}
              {celResult.compileErrors.length > 0 && (
                <div className="text-sm text-red-600">
                  <span className="font-medium">Compile Errors:</span>
                  <ul className="mt-1 list-disc list-inside">
                    {celResult.compileErrors.map((e, i) => (
                      <li key={i}>{e}</li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>
          </TabsContent>
        </Tabs>
      )}
    </div>
  )
}

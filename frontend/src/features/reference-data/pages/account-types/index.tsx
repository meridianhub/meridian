import * as React from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from '@/shared/data-table'
import { StatusBadge } from '@/shared/status-badge'
import { CELEditor } from '@/features/sagas/components/cel-editor'
import { useApiClients } from '@/api/context'
import { PageShell } from '@/shared/page-shell'
import { PageHeader } from '@/shared/page-header'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { referenceKeys } from '@/lib/query-keys'
import {
  BehaviorClass,
  type AccountTypeDefinition,
} from '@/api/gen/meridian/reference_data/v1/account_type_pb'
import { CreateAccountTypeDialog } from './create-account-type-dialog'
import { ExecutionContextTab } from '../../components/execution-context-tab'

const BEHAVIOR_CLASS_LABELS: Record<number, string> = {
  0: 'Unspecified',
  1: 'Customer',
  2: 'Clearing',
  3: 'Nostro',
  4: 'Vostro',
  5: 'Internal',
  6: 'Suspense',
  7: 'Equity',
}

const ACCOUNT_TYPE_STATUS_BADGE_MAP: Record<number, string> = {
  0: 'DRAFT',
  1: 'DRAFT',
  2: 'ACTIVE',
  3: 'DEPRECATED',
}

interface ListAccountTypesParams {
  pageToken?: string
  pageSize: number
  filters?: Record<string, string>
}

interface ListAccountTypesResult {
  items: AccountTypeDefinition[]
  nextPageToken?: string
}

export function AccountTypesPage() {
  const clients = useApiClients()
  const [selectedDefinition, setSelectedDefinition] = React.useState<AccountTypeDefinition | null>(null)
  const [createDialogOpen, setCreateDialogOpen] = React.useState(false)

  const columns: ColumnDef<AccountTypeDefinition>[] = [
    {
      accessorKey: 'code',
      header: 'Code',
      cell: ({ row }) => (
        <span className="font-mono text-sm font-medium">{row.original.code}</span>
      ),
    },
    {
      accessorKey: 'displayName',
      header: 'Display Name',
      cell: ({ row }) => <span className="text-sm">{row.original.displayName || '—'}</span>,
    },
    {
      accessorKey: 'behaviorClass',
      header: 'Behavior',
      cell: ({ row }) => (
        <span className="text-sm">{BEHAVIOR_CLASS_LABELS[row.original.behaviorClass] ?? 'Unknown'}</span>
      ),
    },
    {
      accessorKey: 'instrumentCode',
      header: 'Instrument',
      cell: ({ row }) => (
        <span className="font-mono text-xs">{row.original.instrumentCode || '—'}</span>
      ),
    },
    {
      accessorKey: 'status',
      header: 'Status',
      cell: ({ row }) => {
        const badgeKey = ACCOUNT_TYPE_STATUS_BADGE_MAP[row.original.status] ?? 'DRAFT'
        return <StatusBadge status={badgeKey} />
      },
    },
  ]

  const queryFn = async (params: ListAccountTypesParams): Promise<ListAccountTypesResult> => {
    const behaviorValue = params.filters?.behavior

    const response = await clients.accountTypeRegistry.listActive({
      behaviorClassFilter: behaviorValue
        ? (Number(behaviorValue) as BehaviorClass)
        : BehaviorClass.UNSPECIFIED,
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
    })

    return {
      items: response.definitions ?? [],
      nextPageToken: response.nextPageToken,
    }
  }

  return (
    <PageShell>
      <PageHeader
        title="Account Types"
        description="Account type registry with CEL policy configuration."
        actions={
          <Button onClick={() => setCreateDialogOpen(true)}>
            Create Account Type
          </Button>
        }
      />

      <CreateAccountTypeDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
      />

      <Card className="p-6">
        <DataTable
          queryKey={referenceKeys.accountTypes()}
          queryFn={queryFn}
          columns={columns}
          pageSize={25}
          onRowClick={(def) => setSelectedDefinition(def)}
        />
      </Card>

      {selectedDefinition && (
        <Tabs defaultValue="policies">
          <TabsList>
            <TabsTrigger value="policies">Policies</TabsTrigger>
            <TabsTrigger value="executions">Executions</TabsTrigger>
          </TabsList>
          <TabsContent value="policies">
            <Card data-testid="cel-policy-editor">
              <CardHeader>
                <CardTitle>
                  CEL Policies — {selectedDefinition.code} v{selectedDefinition.version}
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-6">
                <div className="space-y-2">
                  <label className="text-sm font-medium">Validation CEL</label>
                  <p className="text-xs text-muted-foreground">
                    Validates account operations. Available: amount, attributes, valid_from, valid_to, source.
                  </p>
                  <CELEditor
                    value={selectedDefinition.validationCel}
                    onChange={() => {}}
                    context="validation"
                    readOnly
                  />
                </div>

                <div className="space-y-2">
                  <label className="text-sm font-medium">Bucketing CEL</label>
                  <p className="text-xs text-muted-foreground">
                    Determines fungibility buckets for amount pooling.
                  </p>
                  <CELEditor
                    value={selectedDefinition.bucketingCel}
                    onChange={() => {}}
                    context="bucketKey"
                    readOnly
                  />
                </div>

                <div className="space-y-2">
                  <label className="text-sm font-medium">Eligibility CEL</label>
                  <p className="text-xs text-muted-foreground">
                    Determines account eligibility for operations.
                  </p>
                  <CELEditor
                    value={selectedDefinition.eligibilityCel}
                    onChange={() => {}}
                    context="eligibility"
                    readOnly
                  />
                </div>

                {selectedDefinition.attributeSchema && (
                  <div className="space-y-2">
                    <label className="text-sm font-medium">Attribute Schema</label>
                    <pre className="rounded border bg-muted/30 p-3 text-xs font-mono overflow-x-auto">
                      {(() => {
                        try {
                          return JSON.stringify(JSON.parse(selectedDefinition.attributeSchema), null, 2)
                        } catch {
                          return selectedDefinition.attributeSchema
                        }
                      })()}
                    </pre>
                  </div>
                )}
              </CardContent>
            </Card>
          </TabsContent>
          <TabsContent value="executions">
            <ExecutionContextTab entityType="account_type" entityCode={selectedDefinition.code} />
          </TabsContent>
        </Tabs>
      )}
    </PageShell>
  )
}

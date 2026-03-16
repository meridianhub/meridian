import { useParams, useNavigate } from 'react-router-dom'
import { PageShell } from '@/shared/page-shell'
import { PageHeader } from '@/shared/page-header'
import { Button } from '@/components/ui/button'
import { useManifestDiff } from '../hooks/use-manifest-diff'
import { ManifestDiffTable } from '../components/manifest-diff-table'

export function ManifestDiffPage() {
  const { v1, v2 } = useParams<{ v1: string; v2: string }>()
  const navigate = useNavigate()

  const baseSeq = Number(v1 ?? '0')
  const targetSeq = Number(v2 ?? '0')
  const hasValidParams =
    Boolean(v1 && v2) &&
    Number.isInteger(baseSeq) &&
    Number.isInteger(targetSeq) &&
    baseSeq >= 0 &&
    targetSeq > 0

  const { data, isLoading, error } = useManifestDiff(
    hasValidParams ? baseSeq : 0,
    hasValidParams ? targetSeq : 0,
  )

  if (!hasValidParams) {
    return (
      <PageShell>
        <PageHeader title="Manifest Diff" />
        <p className="text-muted-foreground">
          Invalid version parameters. Please select two versions to compare.
        </p>
        <Button variant="outline" className="mt-4" onClick={() => navigate('/economy')}>
          Back to Economy
        </Button>
      </PageShell>
    )
  }

  return (
    <PageShell>
      <PageHeader
        title={`Manifest Diff: v${baseSeq} \u2192 v${targetSeq}`}
        actions={
          <Button variant="outline" size="sm" onClick={() => navigate(-1)}>
            Back
          </Button>
        }
      />

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
        </div>
      )}

      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 p-4">
          <p className="text-sm text-destructive">
            Failed to load diff: {error.message}
          </p>
        </div>
      )}

      {data && (
        <ManifestDiffTable
          actions={data.actions ?? []}
          summary={
            data.summary
              ? {
                  totalActions: data.summary.totalActions,
                  creates: data.summary.creates,
                  updates: data.summary.updates,
                  deletes: data.summary.deletes,
                  noChanges: data.summary.noChanges,
                  hasBreakingChanges: data.summary.hasBreakingChanges,
                }
              : undefined
          }
        />
      )}
    </PageShell>
  )
}

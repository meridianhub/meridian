import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { toJson } from '@bufbuild/protobuf'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { ManifestSchema } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import type { ManifestVersion } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { Download } from 'lucide-react'

function formatApplyStatus(status: ApplyStatus): { label: string; variant: 'default' | 'secondary' | 'destructive' | 'outline' } {
  switch (status) {
    case ApplyStatus.APPLIED:
      return { label: 'Applied', variant: 'default' }
    case ApplyStatus.FAILED:
      return { label: 'Failed', variant: 'destructive' }
    case ApplyStatus.ROLLED_BACK:
      return { label: 'Rolled Back', variant: 'secondary' }
    default:
      return { label: 'Unknown', variant: 'outline' }
  }
}

function formatTimestamp(ts?: { seconds: bigint; nanos: number }): string {
  if (!ts) return 'N/A'
  return new Date(Number(ts.seconds) * 1000).toLocaleString()
}

function serializeManifest(manifest: Manifest): string {
  try {
    return JSON.stringify(toJson(ManifestSchema, manifest), null, 2)
  } catch {
    // Fallback for plain objects (e.g. in tests) - strip protobuf $typeName fields
    return JSON.stringify(manifest, (key, value) => {
      if (key === '$typeName') return undefined
      if (typeof value === 'bigint') return value.toString()
      return value
    }, 2)
  }
}

export function ConfigPanel({ version }: { version: ManifestVersion }) {
  const format = 'json'

  const manifest = version.manifest
  const status = formatApplyStatus(version.applyStatus)

  function handleDownload() {
    if (!manifest) return
    const json = serializeManifest(manifest)
    const blob = new Blob([json], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `manifest-${version.version}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="space-y-4" data-testid="config-panel">
      {/* Metadata */}
      <Card>
        <CardContent className="px-4 py-3 space-y-3">
          <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm">
            <div>
              <span className="text-muted-foreground">Manifest Version</span>
              <p className="font-mono font-medium">{version.version}</p>
            </div>
            <div>
              <span className="text-muted-foreground">Applied At</span>
              <p className="font-medium">{formatTimestamp(version.appliedAt)}</p>
            </div>
            <div>
              <span className="text-muted-foreground">Applied By</span>
              <p className="font-medium">{version.appliedBy || 'N/A'}</p>
            </div>
            <div>
              <span className="text-muted-foreground">Apply Status</span>
              <div className="mt-0.5">
                <Badge variant={status.variant}>{status.label}</Badge>
              </div>
            </div>
            {version.applyJobId && (
              <div>
                <span className="text-muted-foreground">Job ID</span>
                <p className="font-mono text-xs">{version.applyJobId}</p>
              </div>
            )}
            {version.diffSummary && (
              <div className="col-span-2">
                <span className="text-muted-foreground">Change Summary</span>
                <p className="text-sm">{version.diffSummary}</p>
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      {/* Raw manifest */}
      {manifest && (
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">Raw Manifest</span>
              <Badge variant="outline" className="text-xs uppercase">{format}</Badge>
            </div>
            <Button variant="outline" size="sm" onClick={handleDownload}>
              <Download className="mr-2 size-3.5" />
              Download
            </Button>
          </div>
          <Card>
            <CardContent className="px-4 py-3">
              <pre className="text-xs font-mono overflow-auto max-h-96 whitespace-pre-wrap" data-testid="config-raw-manifest">
                {serializeManifest(manifest)}
              </pre>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  )
}

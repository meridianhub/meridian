import { useParams } from 'react-router-dom'
import { Breadcrumbs } from '@/shared'
import { format } from 'date-fns'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { StatusBadge } from '@/shared/status-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { useDatasetDetail, useDatasetObservations } from '../hooks'

interface ObservationPoint {
  x: number // unix seconds
  y: number // parsed float value
  quality: number
  id: string
}

function qualityLabel(quality: number): string {
  switch (quality) {
    case 1:
      return 'ESTIMATE'
    case 2:
      return 'PROVISIONAL'
    case 3:
      return 'ACTUAL'
    case 4:
      return 'REVISED'
    default:
      return 'UNKNOWN'
  }
}

function ObservationChart({ points, unit }: { points: ObservationPoint[]; unit: string }) {
  if (points.length === 0) {
    return (
      <div
        data-testid="observation-chart-empty"
        className="flex h-48 items-center justify-center rounded border bg-muted/20 text-sm text-muted-foreground"
      >
        No observations to display
      </div>
    )
  }

  const width = 800
  const height = 240
  const paddingLeft = 60
  const paddingRight = 20
  const paddingTop = 16
  const paddingBottom = 32

  const chartWidth = width - paddingLeft - paddingRight
  const chartHeight = height - paddingTop - paddingBottom

  const minX = Math.min(...points.map((p) => p.x))
  const maxX = Math.max(...points.map((p) => p.x))
  const minY = Math.min(...points.map((p) => p.y))
  const maxY = Math.max(...points.map((p) => p.y))

  const xRange = maxX - minX || 1
  const yRange = maxY - minY || 1

  const toSvgX = (x: number) => paddingLeft + ((x - minX) / xRange) * chartWidth
  const toSvgY = (y: number) => paddingTop + chartHeight - ((y - minY) / yRange) * chartHeight

  const pathData = points
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toSvgX(p.x).toFixed(1)} ${toSvgY(p.y).toFixed(1)}`)
    .join(' ')

  // Y-axis tick labels
  const yTicks = 4
  const yLabels = Array.from({ length: yTicks + 1 }, (_, i) => {
    const value = minY + (yRange / yTicks) * i
    return { value, svgY: toSvgY(value) }
  })

  // X-axis labels (first and last)
  const xLabels = [
    { x: minX, svgX: toSvgX(minX) },
    ...(points.length > 1 ? [{ x: maxX, svgX: toSvgX(maxX) }] : []),
  ]

  return (
    <div data-testid="observation-chart" className="overflow-x-auto">
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="w-full"
        aria-label={`Observation time series chart for ${unit}`}
      >
        {/* Grid lines */}
        {yLabels.map(({ svgY }, i) => (
          <line
            key={i}
            x1={paddingLeft}
            y1={svgY}
            x2={paddingLeft + chartWidth}
            y2={svgY}
            stroke="currentColor"
            strokeOpacity={0.1}
            strokeWidth={1}
          />
        ))}

        {/* Y-axis labels */}
        {yLabels.map(({ value, svgY }, i) => (
          <text
            key={i}
            x={paddingLeft - 6}
            y={svgY + 4}
            textAnchor="end"
            fontSize={10}
            fill="currentColor"
            opacity={0.6}
          >
            {value.toFixed(2)}
          </text>
        ))}

        {/* X-axis labels */}
        {xLabels.map(({ x, svgX }, i) => (
          <text
            key={i}
            x={svgX}
            y={paddingTop + chartHeight + 18}
            textAnchor={i === 0 ? 'start' : 'end'}
            fontSize={10}
            fill="currentColor"
            opacity={0.6}
          >
            {format(new Date(x * 1000), 'MMM d, HH:mm')}
          </text>
        ))}

        {/* Line path */}
        <path
          d={pathData}
          fill="none"
          stroke="#3b82f6"
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
        />

        {/* Data points */}
        {points.map((p) => (
          <circle
            key={p.id}
            cx={toSvgX(p.x)}
            cy={toSvgY(p.y)}
            r={3}
            fill="#3b82f6"
          />
        ))}
      </svg>
    </div>
  )
}

export function DatasetDetailPage() {
  const { datasetCode } = useParams<{ datasetCode: string }>()
  const tenantSlug = useTenantSlug()

  const datasetQuery = useDatasetDetail(datasetCode)
  const observationsQuery = useDatasetObservations(datasetCode)

  if (!tenantSlug) {
    return (
      <div className="p-6">
        <Breadcrumbs items={[{ label: 'Market Data', href: '/market-data' }]} />
        <p className="mt-4 text-muted-foreground">No tenant selected.</p>
      </div>
    )
  }

  if (!datasetCode) {
    return (
      <div className="p-6">
        <Breadcrumbs items={[{ label: 'Market Data', href: '/market-data' }]} />
        <p className="mt-4 text-muted-foreground">No dataset selected.</p>
      </div>
    )
  }

  const dataset = datasetQuery.data?.dataset
  const observations = observationsQuery.data?.observations ?? []

  const points: ObservationPoint[] = observations
    .filter((o) => o.observedAt)
    .map((o) => {
      const seconds =
        typeof o.observedAt!.seconds === 'bigint'
          ? Number(o.observedAt!.seconds)
          : o.observedAt!.seconds
      return {
        id: o.id,
        x: seconds,
        y: parseFloat(o.value),
        quality: o.quality,
      }
    })
    .sort((a, b) => a.x - b.x)

  function statusLabel(status: number): string {
    switch (status) {
      case 1:
        return 'DRAFT'
      case 2:
        return 'ACTIVE'
      case 3:
        return 'DEPRECATED'
      default:
        return 'UNKNOWN'
    }
  }

  return (
    <div className="p-6 space-y-6">
      <Breadcrumbs
        items={[
          { label: 'Market Data', href: '/market-data' },
          { label: dataset?.displayName || datasetCode },
        ]}
      />
      <div>
        {datasetQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-64" />
            <Skeleton className="h-5 w-48" />
          </div>
        ) : (
          <>
            <h1 className="text-2xl font-semibold">
              {dataset?.displayName || datasetCode}
            </h1>
            {dataset && (
              <div className="mt-2 flex items-center gap-3">
                <span className="font-mono text-sm text-muted-foreground">{dataset.code}</span>
                <StatusBadge status={statusLabel(dataset.status)} />
                <span className="text-sm text-muted-foreground">
                  Unit: <span className="font-mono">{dataset.unit}</span>
                </span>
              </div>
            )}
            {dataset?.description && (
              <p className="mt-2 text-sm text-muted-foreground">{dataset.description}</p>
            )}
          </>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">
            Recent Observations
            {observations.length > 0 && (
              <span className="ml-2 text-sm font-normal text-muted-foreground">
                ({observations.length} shown)
              </span>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {observationsQuery.isLoading ? (
            <div
              data-testid="chart-skeleton"
              className="h-48 animate-pulse rounded bg-muted"
            />
          ) : (
            <ObservationChart
              points={points}
              unit={dataset?.unit ?? ''}
            />
          )}
        </CardContent>
      </Card>

      {observations.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Latest Values</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {observations.slice(0, 10).map((obs) => {
                const seconds =
                  obs.observedAt
                    ? typeof obs.observedAt.seconds === 'bigint'
                      ? Number(obs.observedAt.seconds)
                      : obs.observedAt.seconds
                    : null
                return (
                  <div
                    key={obs.id}
                    className="flex items-center justify-between rounded border p-2 text-sm"
                  >
                    <span className="font-mono font-medium">{obs.value}</span>
                    <span className="text-muted-foreground">
                      {obs.resolutionKeyValue && (
                        <span className="mr-3 font-mono text-xs">{obs.resolutionKeyValue}</span>
                      )}
                      <StatusBadge status={qualityLabel(obs.quality)} />
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {seconds ? format(new Date(seconds * 1000), 'MMM d, HH:mm') : '—'}
                    </span>
                  </div>
                )
              })}
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  )
}

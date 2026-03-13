import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { format } from 'date-fns'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageShell, PageHeader } from '@/shared'
import { useApiClients } from '@/api/context'
import { usePageTitle } from '@/hooks/use-page-title'
import { useTenantContext } from '@/contexts/tenant-context'

interface ForecastPoint {
  timestamp: { seconds: bigint | number; nanos?: number }
  value: string
}

interface CurveChartProps {
  points: ForecastPoint[]
  label: string
}

function CurveChart({ points, label }: CurveChartProps) {
  if (points.length === 0) {
    return (
      <div
        data-testid="curve-chart-empty"
        className="flex h-48 items-center justify-center rounded border bg-muted/20 text-sm text-muted-foreground"
      >
        No forecast points to display
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

  const parsed = points.map((p) => {
    const seconds =
      typeof p.timestamp.seconds === 'bigint'
        ? Number(p.timestamp.seconds)
        : p.timestamp.seconds
    return { x: seconds, y: parseFloat(p.value) }
  })

  const minX = Math.min(...parsed.map((p) => p.x))
  const maxX = Math.max(...parsed.map((p) => p.x))
  const minY = Math.min(...parsed.map((p) => p.y))
  const maxY = Math.max(...parsed.map((p) => p.y))

  const xRange = maxX - minX || 1
  const yRange = maxY - minY || 1

  const toSvgX = (x: number) => paddingLeft + ((x - minX) / xRange) * chartWidth
  const toSvgY = (y: number) => paddingTop + chartHeight - ((y - minY) / yRange) * chartHeight

  const pathData = parsed
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toSvgX(p.x).toFixed(1)} ${toSvgY(p.y).toFixed(1)}`)
    .join(' ')

  const yTicks = 4
  const yLabels = Array.from({ length: yTicks + 1 }, (_, i) => {
    const value = minY + (yRange / yTicks) * i
    return { value, svgY: toSvgY(value) }
  })

  const xLabels = [
    { x: minX, svgX: toSvgX(minX) },
    ...(parsed.length > 1 ? [{ x: maxX, svgX: toSvgX(maxX) }] : []),
  ]

  return (
    <div data-testid="curve-chart" className="overflow-x-auto">
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="w-full"
        aria-label={`Forward curve chart: ${label}`}
      >
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
            {value.toFixed(3)}
          </text>
        ))}

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
            {format(new Date(x * 1000), 'MMM d')}
          </text>
        ))}

        <path
          d={pathData}
          fill="none"
          stroke="#f59e0b"
          strokeWidth={2}
          strokeDasharray="6 3"
          strokeLinejoin="round"
          strokeLinecap="round"
        />

        {parsed.map((p, i) => (
          <circle
            key={i}
            cx={toSvgX(p.x)}
            cy={toSvgY(p.y)}
            r={3}
            fill="#f59e0b"
          />
        ))}
      </svg>
    </div>
  )
}

export function ForecastingPage() {
  usePageTitle('Forecasting')
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()
  const [strategyId, setStrategyId] = useState('')

  const computeMutation = useMutation({
    mutationFn: (id: string) =>
      clients.forecasting.computeForwardCurve({ strategyId: id }),
  })

  if (!tenantSlug) {
    return (
      <PageShell>
        <p className="text-muted-foreground">No tenant selected.</p>
      </PageShell>
    )
  }

  const result = computeMutation.data
  const forecastPoints = result?.forecastPoints ?? []

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (strategyId.trim()) {
      computeMutation.mutate(strategyId.trim())
    }
  }

  return (
    <PageShell>
      <PageHeader
        title="Forecasting"
        description="Compute forward curves by executing a forecasting strategy."
      />

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Compute Forward Curve</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex gap-3" aria-label="forward curve form">
            <div className="flex-1">
              <label htmlFor="strategy-id" className="sr-only">
                Strategy ID
              </label>
              <Input
                id="strategy-id"
                placeholder="Strategy ID (UUID)"
                value={strategyId}
                onChange={(e) => setStrategyId(e.target.value)}
                disabled={computeMutation.isPending}
                aria-label="Strategy ID"
              />
            </div>
            <Button
              type="submit"
              disabled={!strategyId.trim() || computeMutation.isPending}
            >
              {computeMutation.isPending ? 'Computing…' : 'Compute'}
            </Button>
          </form>

          {computeMutation.isError && (
            <p
              role="alert"
              className="mt-3 text-sm text-destructive"
            >
              Failed to compute forward curve. Please check the strategy ID and try again.
            </p>
          )}
        </CardContent>
      </Card>

      {result && (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">
                Forward Curve
                {result.pointCount > 0 && (
                  <span className="ml-2 text-sm font-normal text-muted-foreground">
                    ({result.pointCount} points)
                  </span>
                )}
              </CardTitle>
            </CardHeader>
            <CardContent>
              <CurveChart
                points={forecastPoints}
                label={result.outputDatasetCode}
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">Computation Details</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
                <dt className="text-muted-foreground">Output Dataset</dt>
                <dd className="font-mono">{result.outputDatasetCode}</dd>

                <dt className="text-muted-foreground">Strategy Version</dt>
                <dd className="font-mono">{String(result.strategyVersion)}</dd>

                <dt className="text-muted-foreground">Point Count</dt>
                <dd>{result.pointCount}</dd>

                {result.executionTime && (
                  <>
                    <dt className="text-muted-foreground">Execution Time</dt>
                    <dd>
                      {format(
                        new Date(
                          Number(
                            typeof result.executionTime.seconds === 'bigint'
                              ? result.executionTime.seconds
                              : result.executionTime.seconds,
                          ) * 1000,
                        ),
                        'MMM d, HH:mm:ss',
                      )}
                    </dd>
                  </>
                )}
              </dl>
            </CardContent>
          </Card>
        </>
      )}
    </PageShell>
  )
}

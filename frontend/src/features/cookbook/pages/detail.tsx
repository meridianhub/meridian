import { useMemo } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Breadcrumbs } from '@/shared/breadcrumbs'
import { DetailSkeleton } from '@/shared/detail-skeleton'
import { StarlarkEditor } from '@/features/sagas/components/starlark-editor'
import { ManifestViewer } from '../components/manifest-viewer'
import { SagaFlowDiagram } from '../components/saga-flow'
import { PreviewSourceTabs } from '../components/preview-source-tabs'
import { parseStarlarkSaga } from '../lib/star-parser'
import { generateMermaidMarkup } from '../lib/saga-mermaid'
import { useCookbook } from '../hooks/use-cookbook'
import type { CookbookItem, PatternMeta } from '../hooks/use-cookbook'
import { usePatternFiles } from '../hooks/use-pattern-files'

function complexityLabel(score: number): string {
  if (score <= 3) return 'Low'
  if (score <= 6) return 'Medium'
  return 'High'
}

function complexityColor(score: number): string {
  if (score <= 3) return 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400'
  if (score <= 6) return 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400'
  return 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400'
}

function PatternInfoSection({ item }: { item: CookbookItem }) {
  const meta = item.meta as PatternMeta | undefined

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-2xl font-semibold">{item.title}</h1>
        <Badge variant="outline">{item.type === 'registry:pattern' ? 'Pattern' : 'UI Component'}</Badge>
        {meta?.complexity != null && (
          <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${complexityColor(meta.complexity)}`}>
            Complexity: {meta.complexity} ({complexityLabel(meta.complexity)})
          </span>
        )}
      </div>

      {item.description && (
        <p className="text-muted-foreground">{item.description}</p>
      )}

      <div className="flex flex-wrap gap-4 text-sm">
        {meta?.design_pattern && (
          <div>
            <span className="text-muted-foreground">Design Pattern:</span>{' '}
            <span className="font-medium">{meta.design_pattern}</span>
          </div>
        )}
        {item.categories && item.categories.length > 0 && (
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground">Categories:</span>
            {item.categories.map((cat) => (
              <Badge key={cat} variant="secondary" className="text-xs">
                {cat}
              </Badge>
            ))}
          </div>
        )}
        {meta?.industries && meta.industries.length > 0 && (
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground">Industries:</span>
            {meta.industries.map((ind) => (
              <Badge key={ind} variant="secondary" className="text-xs">
                {ind}
              </Badge>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

interface CompositionItem {
  label: string
  linkTo?: string
}

function CompositionSection({ meta }: { meta: PatternMeta }) {
  const sections: { label: string; items: CompositionItem[] }[] = []

  const asLinks = (names: string[]) => names.map((n) => ({ label: n, linkTo: `/cookbook/${encodeURIComponent(n)}` }))

  if (meta.composes_with?.length) sections.push({ label: 'Composes With', items: asLinks(meta.composes_with) })
  if (meta.extends?.length) sections.push({ label: 'Extends', items: asLinks(meta.extends) })
  if (meta.conflicts_with?.length) sections.push({ label: 'Conflicts With', items: asLinks(meta.conflicts_with) })

  if (meta.provides) {
    const provides = meta.provides
    const provideItems: CompositionItem[] = [
      ...(provides.instruments ?? []).map((i) => ({ label: `instrument:${i}` })),
      ...(provides.account_types ?? []).map((a) => ({ label: `account:${a}` })),
      ...(provides.sagas ?? []).map((s) => ({ label: `saga:${s}` })),
      ...(provides.valuation_rules ?? []).map((v) => ({ label: `valuation:${v}` })),
      ...(provides.triggers ?? []).map((t) => ({ label: `trigger:${t}` })),
    ]
    if (provideItems.length) sections.push({ label: 'Provides', items: provideItems })
  }

  if (meta.requires) {
    const requires = meta.requires
    const requireItems: CompositionItem[] = [
      ...(requires.instruments ?? []).map((i) => ({ label: `instrument:${i}` })),
      ...(requires.market_data ?? []).map((m) => ({ label: `market_data:${m}` })),
    ]
    if (requireItems.length) sections.push({ label: 'Requires', items: requireItems })
  }

  if (sections.length === 0) {
    return <p className="text-sm text-muted-foreground">No composition metadata defined.</p>
  }

  return (
    <div className="space-y-4">
      {sections.map((section) => (
        <div key={section.label}>
          <h3 className="mb-2 text-sm font-medium text-muted-foreground">{section.label}</h3>
          <div className="flex flex-wrap gap-1.5">
            {section.items.map((item) =>
              item.linkTo ? (
                <Link key={item.label} to={item.linkTo}>
                  <Badge variant="outline" className="cursor-pointer hover:bg-accent">
                    {item.label}
                  </Badge>
                </Link>
              ) : (
                <Badge key={item.label} variant="secondary">
                  {item.label}
                </Badge>
              ),
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

export function CookbookDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { items, isLoading: catalogueLoading } = useCookbook()
  const { starlarkContent, manifestContent, isLoading: filesLoading } = usePatternFiles(name)

  const item = items.find((i) => i.name === name)
  const isLoading = catalogueLoading || filesLoading

  const sagaFlow = useMemo(() => {
    if (!starlarkContent) return null
    return parseStarlarkSaga(starlarkContent)
  }, [starlarkContent])

  const mermaidMarkup = useMemo(() => {
    if (!sagaFlow) return ''
    return generateMermaidMarkup(sagaFlow)
  }, [sagaFlow])

  if (catalogueLoading) {
    return <DetailSkeleton fieldCount={3} tabCount={3} showBackNav />
  }

  if (!item) {
    return (
      <div className="space-y-4">
        <Breadcrumbs items={[{ label: 'Cookbook', href: '/cookbook' }, { label: name ?? 'Unknown' }]} />
        <p className="text-muted-foreground">
          Pattern &quot;{name}&quot; not found. It may not be registered yet.
        </p>
      </div>
    )
  }

  const isPattern = item.type === 'registry:pattern'
  const meta = item.meta as PatternMeta | undefined

  return (
    <div className="space-y-6">
      <Breadcrumbs items={[{ label: 'Cookbook', href: '/cookbook' }, { label: item.title }]} />

      <PatternInfoSection item={item} />

      {isPattern ? (
        <Tabs defaultValue="manifest">
          <TabsList>
            <TabsTrigger value="manifest">Manifest</TabsTrigger>
            <TabsTrigger value="starlark">Starlark</TabsTrigger>
            <TabsTrigger value="flow">Flow</TabsTrigger>
            <TabsTrigger value="composition">Composition</TabsTrigger>
          </TabsList>

          <TabsContent value="manifest" className="mt-4">
            {isLoading ? (
              <div className="h-[200px] animate-pulse rounded border bg-muted" />
            ) : manifestContent ? (
              <ManifestViewer content={manifestContent} />
            ) : (
              <p className="text-sm text-muted-foreground">No manifest file found.</p>
            )}
          </TabsContent>

          <TabsContent value="starlark" className="mt-4">
            {isLoading ? (
              <div className="h-[200px] animate-pulse rounded border bg-muted" />
            ) : starlarkContent ? (
              <StarlarkEditor
                value={starlarkContent}
                onChange={() => {}}
                readOnly
              />
            ) : (
              <p className="text-sm text-muted-foreground">No Starlark file found.</p>
            )}
          </TabsContent>

          <TabsContent value="flow" className="mt-4">
            {isLoading ? (
              <div className="h-[400px] animate-pulse rounded border bg-muted" />
            ) : sagaFlow && sagaFlow.steps.length > 0 ? (
              <PreviewSourceTabs
                preview={
                  <div className="h-[500px] rounded-lg border">
                    <SagaFlowDiagram flow={sagaFlow} />
                  </div>
                }
                source={mermaidMarkup}
                sourceLabel="Mermaid"
              />
            ) : (
              <p className="text-sm text-muted-foreground">No saga flow detected in Starlark source.</p>
            )}
          </TabsContent>

          <TabsContent value="composition" className="mt-4">
            {meta ? (
              <CompositionSection meta={meta} />
            ) : (
              <p className="text-sm text-muted-foreground">No composition metadata available.</p>
            )}
          </TabsContent>
        </Tabs>
      ) : (
        <div className="rounded-lg border border-dashed p-8 text-center">
          <p className="text-sm text-muted-foreground">
            UI component preview will be available in a future update.
          </p>
        </div>
      )}
    </div>
  )
}

import { useState, useMemo, useRef, useEffect } from 'react'
import { Link, useParams } from 'react-router-dom'
import { usePageTitle } from '@/hooks/use-page-title'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Breadcrumbs } from '@/shared/breadcrumbs'
import { DetailSkeleton } from '@/shared/detail-skeleton'
import { HandlerReference } from '@/shared/handler-reference'
import { StarlarkEditor } from '@/features/sagas/components/starlark-editor'
import { ManifestViewer } from '../components/manifest-viewer'
import { ComponentDetail } from '../components/component-detail'
import { LinkedPatternDetail } from '../components/linked-detail'
import { parseStarlarkSaga, countFlowNodes } from '../lib/star-parser'
import type { SagaFlow } from '../lib/star-parser'
import { useCookbook } from '../hooks/use-cookbook'
import type { CookbookItem, PatternMeta } from '../hooks/use-cookbook'
import { usePatternFiles } from '../hooks/use-pattern-files'
import type { StarlarkFile } from '../hooks/use-pattern-files'

function complexityLabel(score: number): string {
  if (score <= 3) return 'Low'
  if (score <= 6) return 'Medium'
  return 'High'
}

function complexityColor(score: number): string {
  if (score <= 3) return 'bg-success-muted text-success-foreground'
  if (score <= 6) return 'bg-warning-muted text-warning-foreground'
  return 'bg-destructive/10 text-destructive'
}

function PatternInfoSection({ item, computedComplexity }: { item: CookbookItem; computedComplexity: number | null }) {
  const meta = item.meta as PatternMeta | undefined
  const complexity = computedComplexity ?? meta?.complexity

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-2xl font-semibold">{item.title}</h1>
        <Badge variant="outline">{item.type === 'registry:pattern' ? 'Pattern' : 'UI Component'}</Badge>
        {complexity != null && (
          <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${complexityColor(complexity)}`}>
            Complexity: {complexity} ({complexityLabel(complexity)})
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
        {meta?.trigger && (
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground">Trigger:</span>
            <Badge variant="secondary" className="text-xs">{meta.trigger}</Badge>
          </div>
        )}
        {item.categories && item.categories.length > 0 && (
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground">Categories:</span>
            {item.categories.map((cat) => (
              <Link key={cat} to={`/cookbook/patterns?category=${encodeURIComponent(cat)}`}>
                <Badge variant="secondary" className="cursor-pointer text-xs hover:bg-accent">
                  {cat}
                </Badge>
              </Link>
            ))}
          </div>
        )}
        {meta?.industries != null && (
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground">Industries:</span>
            {meta.industries.length > 0 ? (
              meta.industries.map((ind) => (
                <Link key={ind} to={`/cookbook/patterns?industry=${encodeURIComponent(ind)}`}>
                  <Badge variant="secondary" className="cursor-pointer text-xs hover:bg-accent">
                    {ind}
                  </Badge>
                </Link>
              ))
            ) : (
              <span className="text-xs text-muted-foreground italic">Industry-agnostic</span>
            )}
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

function HandlerReferenceTab({ flows }: { flows: SagaFlow[] }) {
  const serviceNames = useMemo(() => {
    const names = new Set<string>()
    for (const flow of flows) {
      for (const step of flow.steps) {
        for (const call of step.serviceCalls) {
          names.add(call.service)
        }
      }
    }
    return Array.from(names)
  }, [flows])

  return (
    <HandlerReference
      serviceNames={serviceNames.length > 0 ? serviceNames : undefined}
    />
  )
}

function StarlarkTabContent({ starlarkFiles }: { starlarkFiles: StarlarkFile[] }) {
  const [activeFile, setActiveFile] = useState(0)
  const activeIndex = activeFile >= starlarkFiles.length ? 0 : activeFile
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([])

  useEffect(() => {
    tabRefs.current[activeIndex]?.focus()
  }, [activeIndex])

  function handleTabKeyDown(e: React.KeyboardEvent<HTMLButtonElement>, index: number) {
    if (e.key === 'ArrowRight') {
      e.preventDefault()
      setActiveFile((index + 1) % starlarkFiles.length)
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      setActiveFile((index - 1 + starlarkFiles.length) % starlarkFiles.length)
    } else if (e.key === 'Home') {
      e.preventDefault()
      setActiveFile(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      setActiveFile(starlarkFiles.length - 1)
    }
  }

  if (starlarkFiles.length === 0) {
    return <p className="text-sm text-muted-foreground">No Starlark file found.</p>
  }

  if (starlarkFiles.length === 1) {
    return <StarlarkEditor value={starlarkFiles[0].content} onChange={() => {}} readOnly />
  }

  return (
    <div className="space-y-2">
      <div className="flex gap-1 border-b" role="tablist" aria-label="Starlark files">
        {starlarkFiles.map((f, i) => (
          <button
            key={f.name}
            ref={(el) => { tabRefs.current[i] = el }}
            type="button"
            id={`starlark-tab-${i}`}
            role="tab"
            aria-selected={i === activeIndex}
            aria-controls="starlark-panel"
            tabIndex={i === activeIndex ? 0 : -1}
            onKeyDown={(e) => handleTabKeyDown(e, i)}
            onClick={() => setActiveFile(i)}
            className={`px-3 py-1.5 text-xs font-medium border-b-2 transition-colors ${
              i === activeIndex
                ? 'border-primary text-foreground'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            {f.name}
          </button>
        ))}
      </div>
      <div
        id="starlark-panel"
        role="tabpanel"
        aria-labelledby={`starlark-tab-${activeIndex}`}
      >
        <StarlarkEditor value={starlarkFiles[activeIndex].content} onChange={() => {}} readOnly />
      </div>
    </div>
  )
}

export function CookbookDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { items, isLoading: catalogueLoading } = useCookbook()

  usePageTitle(name ? `Cookbook: ${name}` : 'Cookbook')

  const item = items.find((i) => i.name === name)
  const { starlarkFiles, manifestContent, manifestSagas, hasSagas } = usePatternFiles(item)

  // Parse ALL starlark files into flows, matching each to its manifest saga trigger
  const sagaFlows = useMemo(() => {
    const flows: SagaFlow[] = []
    for (const file of starlarkFiles) {
      const flow = parseStarlarkSaga(file.content)
      // Match this starlark file to its manifest saga by name to get the trigger
      const manifestSaga = manifestSagas.find((ms) => ms.name === flow.name)
      if (manifestSaga?.trigger) {
        flow.trigger = manifestSaga.trigger
      }
      if (manifestSaga?.filter) {
        flow.filter = manifestSaga.filter
      }
      flows.push(flow)
    }
    return flows
  }, [starlarkFiles, manifestSagas])

  // Auto-compute complexity from node count
  const computedComplexity = useMemo(() => {
    if (sagaFlows.length === 0) return null
    return countFlowNodes(sagaFlows)
  }, [sagaFlows])

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
  const parentSection = isPattern
    ? { label: 'Patterns', href: '/cookbook/patterns' }
    : { label: 'UI Components', href: '/cookbook/components' }

  const hasFlowSteps = sagaFlows.some((f) => f.steps.length > 0)

  return (
    <div className="space-y-6">
      <Breadcrumbs items={[{ label: 'Cookbook', href: '/cookbook' }, parentSection, { label: item.title }]} />

      <PatternInfoSection item={item} computedComplexity={computedComplexity} />

      {isPattern ? (
        <Tabs defaultValue="manifest">
          <TabsList>
            <TabsTrigger value="manifest">Manifest</TabsTrigger>
            {hasSagas && <TabsTrigger value="starlark">Starlark</TabsTrigger>}
            {hasSagas && <TabsTrigger value="flow">Flow</TabsTrigger>}
            {hasSagas && <TabsTrigger value="handlers">Handlers</TabsTrigger>}
            <TabsTrigger value="composition">Composition</TabsTrigger>
          </TabsList>

          <TabsContent value="manifest" className="mt-4">
            {manifestContent ? (
              <ManifestViewer content={manifestContent} />
            ) : (
              <p className="text-sm text-muted-foreground">No manifest file found.</p>
            )}
          </TabsContent>

          {hasSagas && (
            <TabsContent value="starlark" className="mt-4">
              <StarlarkTabContent starlarkFiles={starlarkFiles} />
            </TabsContent>
          )}

          {hasSagas && (
            <TabsContent value="flow" className="mt-4">
              {hasFlowSteps ? (
                <LinkedPatternDetail flows={sagaFlows} />
              ) : (
                <p className="text-sm text-muted-foreground">No saga flow detected in Starlark source.</p>
              )}
            </TabsContent>
          )}

          {hasSagas && (
            <TabsContent value="handlers" className="mt-4">
              <HandlerReferenceTab flows={sagaFlows} />
            </TabsContent>
          )}

          <TabsContent value="composition" className="mt-4">
            {meta ? (
              <CompositionSection meta={meta} />
            ) : (
              <p className="text-sm text-muted-foreground">No composition metadata available.</p>
            )}
          </TabsContent>
        </Tabs>
      ) : (
        <ComponentDetail item={item} />
      )}
    </div>
  )
}

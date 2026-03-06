import { CompositionGraph } from '../components/composition-graph'
import { useCookbook } from '../hooks/use-cookbook'

export function CookbookGraphPage() {
  const { items, isLoading } = useCookbook()

  if (isLoading) {
    return (
      <div className="flex h-[calc(100vh-4rem)] items-center justify-center">
        <p className="text-muted-foreground">Loading patterns...</p>
      </div>
    )
  }

  if (items.length === 0) {
    return (
      <div className="flex h-[calc(100vh-4rem)] items-center justify-center">
        <p className="text-muted-foreground">No patterns available. Cookbook data is provided by the Vite plugin.</p>
      </div>
    )
  }

  return (
    <div className="h-[calc(100vh-4rem)] relative">
      <CompositionGraph patterns={items} className="h-full w-full" />
    </div>
  )
}

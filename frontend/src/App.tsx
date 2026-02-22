import { QueryClientProvider } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { queryClient } from '@/lib/query-client'
import { PageErrorBoundary } from '@/components/error-boundary'

function App() {
  return (
    <PageErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <div>
          <h1>Meridian Operations Console</h1>
        </div>
        {import.meta.env.DEV && <ReactQueryDevtools initialIsOpen={false} />}
      </QueryClientProvider>
    </PageErrorBoundary>
  )
}

export default App

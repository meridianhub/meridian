import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { createTenantTransport } from './transport'
import { createServiceClients, type ServiceClients } from './clients'
import type { TokenGetter } from './interceptors/auth-interceptor'

interface ApiClientContextValue {
  clients: ServiceClients
}

const ApiClientContext = createContext<ApiClientContextValue | null>(null)

interface ApiClientProviderProps {
  tenantSlug: string | null
  getToken: TokenGetter
  children: ReactNode
}

export function ApiClientProvider({
  tenantSlug,
  getToken,
  children,
}: ApiClientProviderProps) {
  const clients = useMemo(() => {
    const transport = createTenantTransport(tenantSlug, getToken)
    return createServiceClients(transport)
  }, [tenantSlug, getToken])

  return (
    <ApiClientContext.Provider value={{ clients }}>
      {children}
    </ApiClientContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useApiClients(): ServiceClients {
  const ctx = useContext(ApiClientContext)
  if (!ctx) {
    throw new Error('useApiClients must be used within an ApiClientProvider')
  }
  return ctx.clients
}

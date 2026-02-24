const DEFAULT_API_BASE_URL = 'http://localhost:8090'

function getApiBaseUrl(): string {
  const url = import.meta.env.VITE_API_BASE_URL
  if (!url) {
    return DEFAULT_API_BASE_URL
  }
  try {
    new URL(url)
    return url
  } catch {
    console.warn(`Invalid VITE_API_BASE_URL: "${url}", falling back to default`)
    return DEFAULT_API_BASE_URL
  }
}

export const apiConfig = {
  baseUrl: getApiBaseUrl(),
  useBinaryFormat: import.meta.env.PROD && import.meta.env.VITE_E2E_MODE !== 'true',
} as const

export function buildTenantBaseUrl(tenantSlug: string): string {
  const base = apiConfig.baseUrl
  const parsed = new URL(base)

  // In local dev (localhost), tenant is identified via JWT, not subdomain
  if (parsed.hostname === 'localhost' || parsed.hostname === '127.0.0.1') {
    return base.replace(/\/$/, '')
  }

  // In production, use tenant subdomain routing
  parsed.hostname = `${tenantSlug}.${parsed.hostname}`
  return parsed.toString().replace(/\/$/, '')
}

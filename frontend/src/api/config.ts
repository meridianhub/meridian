const DEFAULT_API_BASE_URL = 'http://localhost:8080'

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
  useBinaryFormat: import.meta.env.PROD,
} as const

export function buildTenantBaseUrl(tenantSlug: string): string {
  const base = apiConfig.baseUrl
  if (base && base !== DEFAULT_API_BASE_URL) {
    const parsed = new URL(base)
    parsed.hostname = `${tenantSlug}.${parsed.hostname}`
    return parsed.toString().replace(/\/$/, '')
  }
  return `https://${tenantSlug}.api.meridian.io`
}

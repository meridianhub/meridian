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

/**
 * Returns true if the browser is on a tenant subdomain (e.g. acme.demo.meridianhub.cloud).
 * Detects this by checking if the hostname has more segments than the configured base URL.
 */
export function isOnTenantSubdomain(): boolean {
  const base = apiConfig.baseUrl
  try {
    const baseParts = new URL(base).hostname.split('.')
    const currentParts = window.location.hostname.split('.')
    return currentParts.length > baseParts.length
  } catch {
    return false
  }
}

/**
 * Builds the tenant-scoped MCP server base URL.
 * In production, injects the tenant slug as a subdomain (e.g., https://acme.demo.meridianhub.cloud).
 * For localhost, returns the MCP server URL unchanged (tenant identified via JWT).
 */
export function buildMcpTenantUrl(mcpBaseUrl: string, tenantSlug: string | null): string {
  const stripped = mcpBaseUrl.replace(/\/$/, '')
  if (!tenantSlug) {
    return stripped
  }

  try {
    const parsed = new URL(mcpBaseUrl)

    // In local dev (localhost), tenant is identified via JWT, not subdomain
    if (parsed.hostname === 'localhost' || parsed.hostname === '127.0.0.1') {
      return stripped
    }

    // In production, use tenant subdomain routing
    parsed.hostname = `${tenantSlug}.${parsed.hostname}`
    return parsed.toString().replace(/\/$/, '')
  } catch {
    return stripped
  }
}

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

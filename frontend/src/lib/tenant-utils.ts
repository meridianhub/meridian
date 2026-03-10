/**
 * Extracts the tenant slug from the current hostname's subdomain.
 *
 * For example, given hostname "acme.demo.meridianhub.cloud" and base domain
 * "demo.meridianhub.cloud", returns "acme".
 *
 * Returns null for localhost, bare domain, or when no subdomain is present.
 */
/**
 * Returns true if the hostname is exactly the configured base domain
 * (e.g., "meridianhub.cloud" or "demo.meridianhub.cloud").
 * Returns false for localhost, tenant subdomains, and unrelated domains.
 */
export function isBaseDomain(hostname: string): boolean {
  if (hostname === 'localhost' || hostname === '127.0.0.1') return false
  const baseDomain = (import.meta.env.VITE_BASE_DOMAIN ?? 'meridianhub.cloud').toLowerCase()
  return hostname.toLowerCase() === baseDomain
}

export function getTenantSlugFromSubdomain(hostname: string): string | null {
  // No subdomain support on localhost
  if (hostname === 'localhost' || hostname === '127.0.0.1') {
    return null
  }

  const normalizedHostname = hostname.toLowerCase()
  const baseDomain = (import.meta.env.VITE_BASE_DOMAIN ?? 'meridianhub.cloud').toLowerCase()

  // Hostname must end with the base domain and have at least one extra segment
  if (!normalizedHostname.endsWith(`.${baseDomain}`)) {
    return null
  }

  const slug = normalizedHostname.slice(0, normalizedHostname.length - baseDomain.length - 1)

  // Slug must be non-empty and a single segment (no nested subdomains)
  if (!slug || slug.includes('.')) {
    return null
  }

  return slug
}

/**
 * Extracts the tenant slug from the current hostname's subdomain.
 *
 * For example, given hostname "acme.demo.meridianhub.cloud" and base domain
 * "demo.meridianhub.cloud", returns "acme".
 *
 * Returns null for localhost, bare domain, or when no subdomain is present.
 */
export function getTenantSlugFromSubdomain(hostname: string): string | null {
  // No subdomain support on localhost
  if (hostname === 'localhost' || hostname === '127.0.0.1') {
    return null
  }

  const baseDomain = import.meta.env.VITE_BASE_DOMAIN ?? 'meridianhub.cloud'

  // Hostname must end with the base domain and have at least one extra segment
  if (!hostname.endsWith(`.${baseDomain}`)) {
    return null
  }

  const slug = hostname.slice(0, hostname.length - baseDomain.length - 1)

  // Slug must be non-empty and a single segment (no nested subdomains)
  if (!slug || slug.includes('.')) {
    return null
  }

  return slug
}

/** Validates a tenant slug: lowercase alphanumeric + hyphens, 3-63 chars. */
export function validateSlug(slug: string): string | null {
  if (slug.length < 3) return 'Slug must be at least 3 characters'
  if (slug.length > 63) return 'Slug must be at most 63 characters'
  if (!/^[a-z0-9][a-z0-9-]*[a-z0-9]$/.test(slug))
    return 'Slug may only contain lowercase letters, numbers, and hyphens, and must start and end with a letter or number'
  return null
}

/** Returns 0-4 strength score for a password. */
export function passwordStrength(password: string): number {
  if (password.length === 0) return 0
  let score = 0
  if (password.length >= 8) score++
  if (password.length >= 12) score++
  if (/[A-Z]/.test(password) && /[a-z]/.test(password)) score++
  if (/[0-9]/.test(password)) score++
  if (/[^A-Za-z0-9]/.test(password)) score++
  return Math.min(score, 4)
}

export type SlugAvailability = 'idle' | 'checking' | 'available' | 'taken' | 'error'

/** Returns true if the URL is HTTPS and shares the current hostname suffix. */
export function isSafeRedirectUrl(url: string): boolean {
  try {
    const parsed = new URL(url)
    if (parsed.protocol !== 'https:') return false
    const currentHost = window.location.hostname
    return parsed.hostname.endsWith(`.${currentHost}`) || parsed.hostname === currentHost
  } catch {
    return false
  }
}

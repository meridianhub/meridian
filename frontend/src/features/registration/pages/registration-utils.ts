/** Validates a tenant slug: lowercase alphanumeric + hyphens, 3-63 chars. */
export function validateSlug(slug: string): string | null {
  if (slug.length < 3) return 'Slug must be at least 3 characters'
  if (slug.length > 63) return 'Slug must be at most 63 characters'
  if (!/^[a-z0-9][a-z0-9-]*[a-z0-9]$/.test(slug))
    return 'Slug may only contain lowercase letters, numbers, and hyphens, and must start and end with a letter or number'
  return null
}

/**
 * Returns 0-4 strength score for a password. Caps below 4 ('Strong') for
 * passwords that fail the policy minimum (12 chars + upper/lower/digit), so
 * the strength bar can never label a password 'Strong' that the form will
 * reject on submit.
 */
export function passwordStrength(password: string): number {
  if (password.length === 0) return 0
  let score = 0
  if (password.length >= 8) score++
  if (password.length >= 12) score++
  if (/[A-Z]/.test(password) && /[a-z]/.test(password)) score++
  if (/[0-9]/.test(password)) score++
  if (/[^A-Za-z0-9]/.test(password)) score++

  // Keep the top tier reserved for passwords that pass policy validation.
  const meetsPolicy = validatePassword(password) === null
  return Math.min(score, meetsPolicy ? 4 : 3)
}

export type SlugAvailability = 'idle' | 'checking' | 'available' | 'taken' | 'error'

/** Validates all registration form fields. Returns a map of field name to error message. */
export function validateRegistrationFields(slug: string, email: string, password: string): Record<string, string> {
  const errors: Record<string, string> = {}
  if (!slug) {
    errors.slug = 'Organization slug is required'
  } else {
    const slugValidation = validateSlug(slug)
    if (slugValidation) errors.slug = slugValidation
  }
  const normalizedEmail = email.trim().toLowerCase()
  if (!normalizedEmail) {
    errors.email = 'Email is required'
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalizedEmail)) {
    errors.email = 'Please enter a valid email address'
  }
  const passwordError = validatePassword(password)
  if (passwordError) {
    errors.password = passwordError
  }
  return errors
}

/**
 * Validates a password against the platform's minimum policy: at least
 * 12 characters with one uppercase letter, one lowercase letter, and one digit.
 *
 * Mirrors the backend rules in shared/pkg/credentials/password.go
 * (ValidatePasswordPolicy). Length is checked before complexity so the
 * surfaced error matches the backend's first failure.
 */
export function validatePassword(password: string): string | null {
  if (!password) return 'Password is required'
  if (password.length < 12) return 'Password must be at least 12 characters'
  if (!/[A-Z]/.test(password)) return 'Password must contain an uppercase letter'
  if (!/[a-z]/.test(password)) return 'Password must contain a lowercase letter'
  if (!/[0-9]/.test(password)) return 'Password must contain a digit'
  return null
}

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

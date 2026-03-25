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

const STRENGTH_LABEL = ['', 'Weak', 'Fair', 'Good', 'Strong']
const STRENGTH_COLOR = ['', 'bg-destructive', 'bg-yellow-500', 'bg-blue-500', 'bg-green-500']

export function PasswordStrengthBar({ password }: { password: string }) {
  const score = passwordStrength(password)
  if (!password) return null
  return (
    <div className="mt-1 space-y-1">
      <div className="flex gap-1">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className={`h-1 flex-1 rounded-full transition-colors ${i <= score ? STRENGTH_COLOR[score] : 'bg-muted'}`}
          />
        ))}
      </div>
      <p className="text-xs text-muted-foreground">{STRENGTH_LABEL[score]}</p>
    </div>
  )
}

export type SlugAvailability = 'idle' | 'checking' | 'available' | 'taken' | 'error'

interface SlugStatusProps {
  slug: string
  error: string | null
  availability: SlugAvailability
}

export function SlugStatus({ slug, error, availability }: SlugStatusProps) {
  if (!slug) return null
  if (error) {
    return <p className="mt-1 text-xs text-destructive">{error}</p>
  }
  switch (availability) {
    case 'checking':
      return <p className="mt-1 text-xs text-muted-foreground">Checking availability...</p>
    case 'available':
      return <p className="mt-1 text-xs text-green-600">Slug is available</p>
    case 'taken':
      return <p className="mt-1 text-xs text-destructive">Slug is already taken</p>
    case 'error':
      return <p className="mt-1 text-xs text-muted-foreground">Could not check availability</p>
    default:
      return <p className="mt-1 text-xs text-green-600">Slug format looks good</p>
  }
}

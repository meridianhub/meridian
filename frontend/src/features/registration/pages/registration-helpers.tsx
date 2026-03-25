import { passwordStrength, type SlugAvailability } from './registration-utils'

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

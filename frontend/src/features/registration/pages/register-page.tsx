import { useState, useCallback, useEffect, type FormEvent } from 'react'
import { useNavigate, Link } from 'react-router-dom'

/** Validates a tenant slug: lowercase alphanumeric + hyphens, 3-63 chars. */
function validateSlug(slug: string): string | null {
  if (slug.length < 3) return 'Slug must be at least 3 characters'
  if (slug.length > 63) return 'Slug must be at most 63 characters'
  if (!/^[a-z0-9][a-z0-9-]*[a-z0-9]$/.test(slug))
    return 'Slug may only contain lowercase letters, numbers, and hyphens, and must start and end with a letter or number'
  return null
}

/** Returns 0-4 strength score for a password. */
function passwordStrength(password: string): number {
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

interface PasswordStrengthBarProps {
  password: string
}

function PasswordStrengthBar({ password }: PasswordStrengthBarProps) {
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
}

function SlugStatus({ slug, error }: SlugStatusProps) {
  if (!slug) return null
  if (error) {
    return <p className="mt-1 text-xs text-destructive">{error}</p>
  }
  return <p className="mt-1 text-xs text-green-600">Slug format looks good</p>
}

export function RegisterPage() {
  const navigate = useNavigate()
  const [slug, setSlug] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [slugError, setSlugError] = useState<string | null>(null)
  const [formError, setFormError] = useState('')
  const [loading, setLoading] = useState(false)

  // Debounced slug validation
  useEffect(() => {
    if (!slug) {
      setSlugError(null)
      return
    }
    const timer = setTimeout(() => {
      setSlugError(validateSlug(slug))
    }, 300)
    return () => clearTimeout(timer)
  }, [slug])

  const handleSlugChange = useCallback((value: string) => {
    // Normalize to lowercase as user types
    setSlug(value.toLowerCase().replace(/[^a-z0-9-]/g, ''))
  }, [])

  const handleSubmit = useCallback(
    async (e: FormEvent) => {
      e.preventDefault()
      setFormError('')

      const slugValidation = validateSlug(slug)
      if (slugValidation) {
        setSlugError(slugValidation)
        return
      }

      if (password.length < 8) {
        setFormError('Password must be at least 8 characters')
        return
      }

      setLoading(true)
      try {
        const response = await fetch('/api/v1/register', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            slug,
            email,
            password,
            display_name: displayName || undefined,
          }),
        })

        if (response.status === 409) {
          setFormError('That slug is already taken. Please choose a different one.')
          return
        }

        if (response.status === 429) {
          setFormError('Too many registration attempts. Please wait a moment and try again.')
          return
        }

        if (!response.ok) {
          const data = (await response.json().catch(() => null)) as { error?: string } | null
          setFormError(data?.error ?? 'Registration failed. Please try again.')
          return
        }

        void navigate('/login?registered=1')
      } catch {
        setFormError('Unable to reach the server. Please check your connection and try again.')
      } finally {
        setLoading(false)
      }
    },
    [slug, email, password, displayName, navigate],
  )

  const inputClass =
    'w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="w-full max-w-sm space-y-6 px-4">
        <div className="text-center">
          <h1 className="text-2xl font-semibold">Create your account</h1>
          <p className="mt-2 text-muted-foreground">
            Set up a new Meridian tenant to get started.
          </p>
        </div>

        <form onSubmit={(e) => void handleSubmit(e)} className="space-y-4" noValidate>
          <div>
            <label htmlFor="slug" className="block text-sm font-medium mb-1">
              Organization slug <span className="text-destructive" aria-hidden>*</span>
            </label>
            <input
              id="slug"
              type="text"
              value={slug}
              onChange={(e) => handleSlugChange(e.target.value)}
              required
              autoComplete="organization"
              aria-describedby="slug-hint slug-status"
              className={inputClass}
              placeholder="my-org"
              minLength={3}
              maxLength={63}
            />
            <p id="slug-hint" className="mt-1 text-xs text-muted-foreground">
              Lowercase letters, numbers, and hyphens. 3-63 characters.
            </p>
            <div id="slug-status" aria-live="polite">
              <SlugStatus slug={slug} error={slugError} />
            </div>
          </div>

          <div>
            <label htmlFor="display-name" className="block text-sm font-medium mb-1">
              Display name
            </label>
            <input
              id="display-name"
              type="text"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              autoComplete="organization-title"
              className={inputClass}
              placeholder="My Organization"
              maxLength={100}
            />
          </div>

          <div>
            <label htmlFor="email" className="block text-sm font-medium mb-1">
              Email <span className="text-destructive" aria-hidden>*</span>
            </label>
            <input
              id="email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              autoComplete="email"
              className={inputClass}
              placeholder="admin@example.com"
            />
          </div>

          <div>
            <label htmlFor="password" className="block text-sm font-medium mb-1">
              Password <span className="text-destructive" aria-hidden>*</span>
            </label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              autoComplete="new-password"
              aria-describedby="password-strength"
              className={inputClass}
              minLength={8}
            />
            <div id="password-strength" aria-live="polite">
              <PasswordStrengthBar password={password} />
            </div>
          </div>

          {formError && (
            <p role="alert" className="text-sm text-destructive">
              {formError}
            </p>
          )}

          <button
            type="submit"
            disabled={loading || !!slugError}
            className="w-full rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {loading ? 'Creating account...' : 'Create account'}
          </button>
        </form>

        <p className="text-center text-sm text-muted-foreground">
          Already have an account?{' '}
          <Link to="/login" className="text-primary underline-offset-4 hover:underline">
            Sign in
          </Link>
        </p>
      </div>
    </div>
  )
}

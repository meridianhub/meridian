import { useState, useCallback, useEffect, useRef, type FormEvent } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { validateSlug, validateRegistrationFields, isSafeRedirectUrl, type SlugAvailability } from './registration-utils'
import { PasswordInput, SlugStatus, RedirectSuccess } from './registration-helpers'

export function RegisterPage() {
  const navigate = useNavigate()
  const [slug, setSlug] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [slugError, setSlugError] = useState<string | null>(null)
  const [slugAvailability, setSlugAvailability] = useState<SlugAvailability>('idle')
  const [formError, setFormError] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(false)
  const [redirecting, setRedirecting] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const slugCheckController = useRef<AbortController | null>(null)
  const redirectTimerRef = useRef<number | null>(null)

  // Clean up redirect timer on unmount
  useEffect(() => {
    return () => {
      if (redirectTimerRef.current !== null) {
        window.clearTimeout(redirectTimerRef.current)
        redirectTimerRef.current = null
      }
    }
  }, [])

  // Debounced slug validation + availability check
  useEffect(() => {
    if (!slug) {
      setSlugError(null)
      setSlugAvailability('idle')
      return
    }

    const formatError = validateSlug(slug)
    if (formatError) {
      setSlugError(formatError)
      setSlugAvailability('idle')
      return
    }

    setSlugError(null)
    setSlugAvailability('checking')

    const timer = setTimeout(() => {
      slugCheckController.current?.abort()
      const controller = new AbortController()
      slugCheckController.current = controller

      fetch(`/api/v1/slugs/${encodeURIComponent(slug)}/available`, {
        signal: controller.signal,
      })
        .then((res) => {
          if (!res.ok) throw new Error(`status ${res.status}`)
          return res.json() as Promise<{ available: boolean; reason?: string }>
        })
        .then((data) => {
          if (!controller.signal.aborted) {
            setSlugAvailability(data.available ? 'available' : 'taken')
          }
        })
        .catch((err: unknown) => {
          if (err instanceof DOMException && err.name === 'AbortError') return
          if (!controller.signal.aborted) {
            setSlugAvailability('error')
          }
        })
    }, 400)

    return () => {
      clearTimeout(timer)
      slugCheckController.current?.abort()
    }
  }, [slug])

  const clearFieldError = useCallback((field: string) => {
    setFieldErrors((prev) => { const next = { ...prev }; delete next[field]; return next })
  }, [])

  const handleSlugChange = useCallback((value: string) => {
    setSlug(value.toLowerCase().replace(/[^a-z0-9-]/g, ''))
    clearFieldError('slug')
    setFormError('')
  }, [clearFieldError])

  const validateFields = useCallback((): boolean => {
    const errors = validateRegistrationFields(slug, email, password)
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }, [slug, email, password])

  const handleSubmit = useCallback(
    async (e: FormEvent) => {
      e.preventDefault()
      setFormError('')
      setSubmitted(true)

      if (!validateFields()) return

      if (slugAvailability === 'taken') {
        setFormError('That slug is already taken. Please choose a different one.')
        return
      }

      setLoading(true)
      const controller = new AbortController()
      const timeoutId = window.setTimeout(() => controller.abort(), 15_000)
      try {
        const response = await fetch('/api/v1/register', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          signal: controller.signal,
          body: JSON.stringify({
            slug,
            email: email.trim().toLowerCase(),
            password,
            display_name: displayName || undefined,
          }),
        })

        if (response.status === 409) {
          setFormError('That slug is already taken. Please choose a different one.')
          return
        }

        if (response.status === 429) {
          setFormError('Too many registration attempts. Please try again later.')
          return
        }

        if (!response.ok) {
          const data = (await response.json().catch(() => null)) as { error?: string } | null
          setFormError(data?.error ?? 'Registration failed. Please try again.')
          return
        }

        const data = (await response.json().catch(() => null)) as {
          tenant_id?: string
          login_url?: string
        } | null
        const loginUrl = typeof data?.login_url === 'string' ? data.login_url : undefined

        if (loginUrl && isSafeRedirectUrl(loginUrl)) {
          setRedirecting(true)
          redirectTimerRef.current = window.setTimeout(() => {
            window.location.href = loginUrl
          }, 1500)
        } else {
          const fallbackPath = (loginUrl && !loginUrl.startsWith('http')) ? loginUrl : '/login?registered=1'
          void navigate(fallbackPath)
        }
      } catch (error) {
        if (error instanceof DOMException && error.name === 'AbortError') {
          setFormError('Registration timed out. Please try again.')
        } else {
          setFormError('Unable to reach the server. Please check your connection and try again.')
        }
      } finally {
        window.clearTimeout(timeoutId)
        setLoading(false)
      }
    },
    [slug, slugAvailability, email, password, displayName, navigate, validateFields],
  )

  const inputClass =
    'w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const inputErrorClass = inputClass + ' border-destructive'

  if (redirecting) {
    return <RedirectSuccess />
  }

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
              Organization slug <span className="text-destructive" aria-hidden="true">*</span>
              <span className="sr-only">(required)</span>
            </label>
            <input
              id="slug"
              type="text"
              value={slug}
              onChange={(e) => handleSlugChange(e.target.value)}
              required
              autoComplete="organization"
              aria-describedby="slug-hint slug-status"
              aria-invalid={!!slugError || (submitted && !!fieldErrors.slug) ? true : undefined}
              className={!!slugError || (submitted && !!fieldErrors.slug) ? inputErrorClass : inputClass}
              placeholder="my-org"
              minLength={3}
              maxLength={63}
            />
            <p id="slug-hint" className="mt-1 text-xs text-muted-foreground">
              Lowercase letters, numbers, and hyphens. 3-63 characters.
            </p>
            <div id="slug-status" role="status" aria-live="polite">
              {submitted && fieldErrors.slug ? (
                <p className="mt-1 text-xs text-destructive">{fieldErrors.slug}</p>
              ) : (
                <SlugStatus slug={slug} error={slugError} availability={slugAvailability} />
              )}
            </div>
          </div>

          <div>
            <label htmlFor="display-name" className="block text-sm font-medium mb-1">
              Display name <span className="text-xs text-muted-foreground font-normal">(optional)</span>
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
              Email <span className="text-destructive" aria-hidden="true">*</span>
              <span className="sr-only">(required)</span>
            </label>
            <input
              id="email"
              type="email"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value)
                clearFieldError('email')
              }}
              required
              autoComplete="email"
              aria-invalid={submitted && !!fieldErrors.email ? true : undefined}
              className={submitted && fieldErrors.email ? inputErrorClass : inputClass}
              placeholder="admin@example.com"
            />
            {submitted && fieldErrors.email && (
              <p className="mt-1 text-xs text-destructive" role="alert">{fieldErrors.email}</p>
            )}
          </div>

          <PasswordInput
            value={password}
            onChange={(e) => {
              setPassword(e.target.value)
              clearFieldError('password')
            }}
            inputClass={inputClass}
            error={fieldErrors.password}
            submitted={submitted}
          />

          {formError && (
            <p role="alert" className="text-sm text-destructive">
              {formError}
            </p>
          )}

          <button
            type="submit"
            disabled={loading || !!slugError || slugAvailability === 'taken'}
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

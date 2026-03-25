import { useState, useCallback, useEffect, useRef, type FormEvent } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { validateSlug, isSafeRedirectUrl, type SlugAvailability } from './registration-utils'
import { PasswordStrengthBar, SlugStatus, RedirectSuccess } from './registration-helpers'

export function RegisterPage() {
  const navigate = useNavigate()
  const [slug, setSlug] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
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
      // Abort any in-flight request
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

  const handleSlugChange = useCallback((value: string) => {
    setSlug(value.toLowerCase().replace(/[^a-z0-9-]/g, ''))
    setFieldErrors((prev) => {
      const next = { ...prev }
      delete next.slug
      return next
    })
  }, [])

  const validateFields = useCallback((): boolean => {
    const errors: Record<string, string> = {}

    if (!slug) {
      errors.slug = 'Organization slug is required'
    } else {
      const slugValidation = validateSlug(slug)
      if (slugValidation) {
        errors.slug = slugValidation
      }
    }

    const normalizedEmail = email.trim().toLowerCase()
    const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
    if (!normalizedEmail) {
      errors.email = 'Email is required'
    } else if (!emailPattern.test(normalizedEmail)) {
      errors.email = 'Please enter a valid email address'
    }

    if (!password) {
      errors.password = 'Password is required'
    } else if (password.length < 8) {
      errors.password = 'Password must be at least 8 characters'
    }

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
          setFormError('Too many registration attempts. Please try again in a few minutes.')
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
          // Safe tenant subdomain URL - show success then redirect
          setRedirecting(true)
          redirectTimerRef.current = window.setTimeout(() => {
            window.location.href = loginUrl
          }, 1500)
        } else {
          // Relative path, missing, or untrusted URL - use client-side navigation
          // Reject absolute URLs that failed validation to avoid broken SPA routes
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
              aria-invalid={submitted && (!!slugError || !!fieldErrors.slug) ? true : undefined}
              className={submitted && fieldErrors.slug ? inputErrorClass : inputClass}
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
                setFieldErrors((prev) => {
                  const next = { ...prev }
                  delete next.email
                  return next
                })
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

          <div>
            <label htmlFor="password" className="block text-sm font-medium mb-1">
              Password <span className="text-destructive" aria-hidden="true">*</span>
              <span className="sr-only">(required)</span>
            </label>
            <div className="relative">
              <input
                id="password"
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value)
                  setFieldErrors((prev) => {
                    const next = { ...prev }
                    delete next.password
                    return next
                  })
                }}
                required
                autoComplete="new-password"
                aria-describedby="password-strength password-hint"
                aria-invalid={submitted && !!fieldErrors.password ? true : undefined}
                className={`${submitted && fieldErrors.password ? inputErrorClass : inputClass} pr-10`}
                minLength={8}
              />
              <button
                type="button"
                onClick={() => setShowPassword((v) => !v)}
                className="absolute inset-y-0 right-0 flex items-center pr-3 text-muted-foreground hover:text-foreground"
                aria-label={showPassword ? 'Hide password' : 'Show password'}
              >
                {showPassword ? (
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="h-4 w-4" aria-hidden="true">
                    <path fillRule="evenodd" d="M3.28 2.22a.75.75 0 00-1.06 1.06l14.5 14.5a.75.75 0 101.06-1.06l-1.745-1.745a10.029 10.029 0 003.3-4.38 1.651 1.651 0 000-1.185A10.004 10.004 0 009.999 3a9.956 9.956 0 00-4.744 1.194L3.28 2.22zM7.752 6.69l1.092 1.092a2.5 2.5 0 013.374 3.373l1.092 1.092a4 4 0 00-5.558-5.558z" clipRule="evenodd" />
                    <path d="M10.748 13.93l2.523 2.523a9.987 9.987 0 01-3.27.547c-4.258 0-7.894-2.66-9.337-6.41a1.651 1.651 0 010-1.186A10.007 10.007 0 012.839 6.02L6.07 9.252a4 4 0 004.678 4.678z" />
                  </svg>
                ) : (
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="h-4 w-4" aria-hidden="true">
                    <path d="M10 12.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5z" />
                    <path fillRule="evenodd" d="M.664 10.59a1.651 1.651 0 010-1.186A10.004 10.004 0 0110 3c4.257 0 7.893 2.66 9.336 6.41.147.381.146.804 0 1.186A10.004 10.004 0 0110 17c-4.257 0-7.893-2.66-9.336-6.41zM14 10a4 4 0 11-8 0 4 4 0 018 0z" clipRule="evenodd" />
                  </svg>
                )}
              </button>
            </div>
            <p id="password-hint" className="mt-1 text-xs text-muted-foreground">
              Minimum 8 characters.
            </p>
            {submitted && fieldErrors.password ? (
              <p className="mt-1 text-xs text-destructive" role="alert">{fieldErrors.password}</p>
            ) : (
              <div id="password-strength" aria-live="polite">
                <PasswordStrengthBar password={password} />
              </div>
            )}
          </div>

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

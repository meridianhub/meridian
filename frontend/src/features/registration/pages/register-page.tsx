import { useState, useCallback, useEffect, useRef, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  validateSlug,
  validateRegistrationFields,
  isSafeRedirectUrl,
  type SlugAvailability,
} from './registration-utils'
import { RedirectSuccess, ProvisioningProgress } from './registration-helpers'
import { RegistrationForm } from './registration-form'
import { useProvisioningPoll } from './use-provisioning-poll'

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
  const [pendingLoginUrl, setPendingLoginUrl] = useState<string | undefined>(undefined)
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

  const navigateToLogin = useCallback(
    (loginUrl: string | undefined) => {
      if (loginUrl && isSafeRedirectUrl(loginUrl)) {
        setRedirecting(true)
        redirectTimerRef.current = window.setTimeout(() => {
          window.location.href = loginUrl
        }, 1500)
      } else {
        const fallbackPath = loginUrl && !loginUrl.startsWith('http') ? loginUrl : '/login?registered=1'
        void navigate(fallbackPath)
      }
    },
    [navigate],
  )

  // Hook owns the polling lifecycle (timer cleanup, cancellation,
  // PENDING/COMPLETED/FAILED transitions). The page wires it to the login URL
  // received with the registration response.
  const provisioning = useProvisioningPoll(() => navigateToLogin(pendingLoginUrl))

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
          const errorMsg = data?.error ?? 'Registration failed. Please try again.'

          // Surface backend password policy errors next to the password field
          // rather than as a generic form-level toast. The backend wraps these
          // as "password policy violation: password too short|weak"
          // (registration_handler.go calls credentials.ValidatePasswordPolicy).
          if (
            /password policy violation/i.test(errorMsg) ||
            /password too short/i.test(errorMsg) ||
            /password too weak/i.test(errorMsg)
          ) {
            setFieldErrors((prev) => ({
              ...prev,
              password:
                'Password must be at least 12 characters with uppercase, lowercase, and a digit',
            }))
            return
          }

          setFormError(errorMsg)
          return
        }

        const data = (await response.json().catch(() => null)) as {
          tenant_id?: string
          login_url?: string
          provisioning_pending?: boolean
        } | null
        const loginUrl = typeof data?.login_url === 'string' ? data.login_url : undefined
        const tenantId = typeof data?.tenant_id === 'string' ? data.tenant_id : undefined

        // When the backend reports async provisioning, hold the user on a
        // progress screen until provisioning completes. Otherwise the
        // redirect to /login lands before the admin identity exists and
        // the first sign-in fails.
        if (data?.provisioning_pending && tenantId) {
          setPendingLoginUrl(loginUrl)
          provisioning.start(tenantId)
          return
        }

        navigateToLogin(loginUrl)
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
    [slug, slugAvailability, email, password, displayName, validateFields, provisioning, navigateToLogin],
  )

  if (redirecting) {
    return <RedirectSuccess />
  }

  if (provisioning.status !== null) {
    return (
      <ProvisioningProgress
        status={provisioning.status}
        onRetry={provisioning.status === 'timeout' ? provisioning.retry : undefined}
      />
    )
  }

  return (
    <RegistrationForm
      slug={slug}
      email={email}
      password={password}
      displayName={displayName}
      slugError={slugError}
      slugAvailability={slugAvailability}
      formError={formError}
      fieldErrors={fieldErrors}
      loading={loading}
      submitted={submitted}
      onSlugChange={handleSlugChange}
      onEmailChange={(e) => {
        setEmail(e.target.value)
        clearFieldError('email')
      }}
      onPasswordChange={(e) => {
        setPassword(e.target.value)
        clearFieldError('password')
      }}
      onDisplayNameChange={(e) => setDisplayName(e.target.value)}
      onSubmit={(e) => void handleSubmit(e)}
    />
  )
}

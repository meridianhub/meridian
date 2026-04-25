import type { ChangeEvent, FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { PasswordInput, SlugStatus } from './registration-helpers'
import type { SlugAvailability } from './registration-utils'

const inputClass =
  'w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const inputErrorClass = inputClass + ' border-destructive'

interface RegistrationFormProps {
  slug: string
  email: string
  password: string
  displayName: string
  slugError: string | null
  slugAvailability: SlugAvailability
  formError: string
  fieldErrors: Record<string, string>
  loading: boolean
  submitted: boolean
  onSlugChange: (value: string) => void
  onEmailChange: (e: ChangeEvent<HTMLInputElement>) => void
  onPasswordChange: (e: ChangeEvent<HTMLInputElement>) => void
  onDisplayNameChange: (e: ChangeEvent<HTMLInputElement>) => void
  onSubmit: (e: FormEvent) => void
}

/**
 * Visual layer of the registration form. State, validation, and submission
 * live in RegisterPage; this component is purely presentational so the page
 * stays under the architecture file-size limit.
 */
export function RegistrationForm(props: RegistrationFormProps) {
  const {
    slug,
    email,
    password,
    displayName,
    slugError,
    slugAvailability,
    formError,
    fieldErrors,
    loading,
    submitted,
    onSlugChange,
    onEmailChange,
    onPasswordChange,
    onDisplayNameChange,
    onSubmit,
  } = props

  const slugHasError = !!slugError || (submitted && !!fieldErrors.slug)

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="w-full max-w-sm space-y-6 px-4">
        <div className="text-center">
          <h1 className="text-2xl font-semibold">Create your account</h1>
          <p className="mt-2 text-muted-foreground">
            Set up a new Meridian tenant to get started.
          </p>
        </div>

        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <div>
            <label htmlFor="slug" className="block text-sm font-medium mb-1">
              Organization slug <span className="text-destructive" aria-hidden="true">*</span>
              <span className="sr-only">(required)</span>
            </label>
            <input
              id="slug"
              type="text"
              value={slug}
              onChange={(e) => onSlugChange(e.target.value)}
              required
              autoComplete="organization"
              aria-describedby="slug-hint slug-status"
              aria-invalid={slugHasError ? true : undefined}
              className={slugHasError ? inputErrorClass : inputClass}
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
              onChange={onDisplayNameChange}
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
              onChange={onEmailChange}
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
            onChange={onPasswordChange}
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

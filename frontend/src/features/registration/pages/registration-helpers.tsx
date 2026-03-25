import { useState, type ChangeEvent } from 'react'
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

export function RedirectSuccess() {
  return (
    <div className="flex min-h-screen items-center justify-center">
      <div
        className="w-full max-w-sm space-y-4 px-4 text-center"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        <h1 className="text-2xl font-semibold">Account created!</h1>
        <p className="text-muted-foreground">
          Redirecting to your organization...
        </p>
      </div>
    </div>
  )
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

function EyeIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="h-4 w-4" aria-hidden="true">
      <path d="M10 12.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5z" />
      <path fillRule="evenodd" d="M.664 10.59a1.651 1.651 0 010-1.186A10.004 10.004 0 0110 3c4.257 0 7.893 2.66 9.336 6.41.147.381.146.804 0 1.186A10.004 10.004 0 0110 17c-4.257 0-7.893-2.66-9.336-6.41zM14 10a4 4 0 11-8 0 4 4 0 018 0z" clipRule="evenodd" />
    </svg>
  )
}

function EyeSlashIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="h-4 w-4" aria-hidden="true">
      <path fillRule="evenodd" d="M3.28 2.22a.75.75 0 00-1.06 1.06l14.5 14.5a.75.75 0 101.06-1.06l-1.745-1.745a10.029 10.029 0 003.3-4.38 1.651 1.651 0 000-1.185A10.004 10.004 0 009.999 3a9.956 9.956 0 00-4.744 1.194L3.28 2.22zM7.752 6.69l1.092 1.092a2.5 2.5 0 013.374 3.373l1.092 1.092a4 4 0 00-5.558-5.558z" clipRule="evenodd" />
      <path d="M10.748 13.93l2.523 2.523a9.987 9.987 0 01-3.27.547c-4.258 0-7.894-2.66-9.337-6.41a1.651 1.651 0 010-1.186A10.007 10.007 0 012.839 6.02L6.07 9.252a4 4 0 004.678 4.678z" />
    </svg>
  )
}

interface PasswordInputProps {
  value: string
  onChange: (e: ChangeEvent<HTMLInputElement>) => void
  inputClass: string
  error?: string | null
  submitted?: boolean
}

export function PasswordInput({ value, onChange, inputClass, error, submitted }: PasswordInputProps) {
  const [showPassword, setShowPassword] = useState(false)
  const hasError = submitted && !!error
  const errorClass = hasError ? inputClass + ' border-destructive' : inputClass

  return (
    <div>
      <label htmlFor="password" className="block text-sm font-medium mb-1">
        Password <span className="text-destructive" aria-hidden="true">*</span>
        <span className="sr-only">(required)</span>
      </label>
      <div className="relative">
        <input
          id="password"
          type={showPassword ? 'text' : 'password'}
          value={value}
          onChange={onChange}
          required
          autoComplete="new-password"
          aria-describedby={hasError ? 'password-hint' : 'password-strength password-hint'}
          aria-invalid={hasError ? true : undefined}
          className={`${errorClass} pr-10`}
          minLength={8}
        />
        <button
          type="button"
          onClick={() => setShowPassword((v) => !v)}
          className="absolute inset-y-0 right-0 flex items-center pr-3 text-muted-foreground hover:text-foreground"
          aria-label={showPassword ? 'Hide password' : 'Show password'}
        >
          {showPassword ? <EyeSlashIcon /> : <EyeIcon />}
        </button>
      </div>
      <p id="password-hint" className="mt-1 text-xs text-muted-foreground">
        Minimum 8 characters.
      </p>
      {hasError ? (
        <p className="mt-1 text-xs text-destructive" role="alert">{error}</p>
      ) : (
        <div id="password-strength" aria-live="polite">
          <PasswordStrengthBar password={value} />
        </div>
      )}
    </div>
  )
}

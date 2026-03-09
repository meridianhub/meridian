import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { GoogleIcon, GitHubIcon, MicrosoftIcon, DefaultProviderIcon } from './provider-icons'
import type { AuthProvider } from '@/hooks/use-auth-providers'

interface ProviderButtonProps {
  provider: AuthProvider
  onClick: () => Promise<void>
}

function ProviderIcon({ providerId }: { providerId: string }) {
  const normalized = providerId.toLowerCase()
  if (normalized.includes('google')) return <GoogleIcon className="size-4" />
  if (normalized.includes('github')) return <GitHubIcon className="size-4" />
  if (normalized.includes('microsoft')) return <MicrosoftIcon className="size-4" />
  return <DefaultProviderIcon className="size-4" />
}

export function ProviderButton({ provider, onClick }: ProviderButtonProps) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleClick = async () => {
    setLoading(true)
    setError(null)
    try {
      await onClick()
    } catch {
      setError('Failed to start sign-in. Please try again.')
      setLoading(false)
    }
  }

  return (
    <div>
      <Button
        variant="outline"
        className="w-full"
        disabled={loading}
        onClick={() => void handleClick()}
      >
        <ProviderIcon providerId={provider.id} />
        {loading ? 'Redirecting...' : `Sign in with ${provider.displayName}`}
      </Button>
      {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
    </div>
  )
}

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

  const handleClick = async () => {
    setLoading(true)
    try {
      await onClick()
    } catch {
      setLoading(false)
    }
  }

  return (
    <Button
      variant="outline"
      className="w-full"
      disabled={loading}
      onClick={() => void handleClick()}
    >
      <ProviderIcon providerId={provider.id} />
      {loading ? 'Redirecting...' : `Sign in with ${provider.displayName}`}
    </Button>
  )
}

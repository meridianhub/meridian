import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { getProviderIcon } from './provider-icons'
import type { AuthProvider } from '@/hooks/use-auth-providers'

interface ProviderButtonProps {
  provider: AuthProvider
  onClick: () => Promise<void>
}

export function ProviderButton({ provider, onClick }: ProviderButtonProps) {
  const [loading, setLoading] = useState(false)
  const Icon = getProviderIcon(provider.id)

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
      <Icon className="size-4" />
      {loading ? 'Redirecting...' : `Sign in with ${provider.displayName}`}
    </Button>
  )
}

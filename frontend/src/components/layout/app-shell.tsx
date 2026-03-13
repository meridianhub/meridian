import { useState } from 'react'
import { Sidebar } from '@/components/layout/sidebar'
import { Header } from '@/components/layout/header'
import { useAuth } from '@/contexts/auth-context'

const SIDEBAR_ID = 'app-sidebar'

interface AppShellProps {
  children: React.ReactNode
  currentPath?: string
}

export function AppShell({ children, currentPath = '/' }: AppShellProps) {
  const { lens } = useAuth()
  const [sidebarOpen, setSidebarOpen] = useState(false)

  return (
    <div className="flex h-screen overflow-hidden bg-muted">
      <Sidebar
        id={SIDEBAR_ID}
        lens={lens}
        currentPath={currentPath}
        isOpen={sidebarOpen}
        onClose={() => setSidebarOpen(false)}
      />

      <div className="flex flex-1 flex-col overflow-hidden">
        <Header
          onMenuToggle={() => setSidebarOpen((prev) => !prev)}
          sidebarOpen={sidebarOpen}
          sidebarId={SIDEBAR_ID}
        />
        <main className="flex-1 overflow-y-auto p-4">
          {children}
        </main>
      </div>
    </div>
  )
}

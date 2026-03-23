import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { McpConnectionCard } from './mcp-connection-card'

vi.mock('@/contexts/tenant-context', () => ({
  useTenantContext: vi.fn(() => ({
    tenantSlug: 'test-tenant',
    currentTenant: { id: 'tid', slug: 'test-tenant', name: 'Test Tenant' },
    isPlatformAdmin: false,
    switchTenant: vi.fn(),
    clearTenant: vi.fn(),
  })),
}))

vi.mock('@/contexts/auth-context', () => ({
  useAuth: vi.fn(() => ({
    isAuthenticated: true,
    accessToken: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature',
    claims: null,
    lens: 'tenant',
    login: vi.fn(),
    logout: vi.fn(),
    refreshToken: vi.fn(),
  })),
}))

import { useTenantContext } from '@/contexts/tenant-context'
import { useAuth } from '@/contexts/auth-context'

function Wrapper({ children }: { children: React.ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>
}

const defaultTenantContext = {
  tenantSlug: 'test-tenant',
  currentTenant: { id: 'tid', slug: 'test-tenant', name: 'Test Tenant' },
  isPlatformAdmin: false,
  switchTenant: vi.fn(),
  clearTenant: vi.fn(),
}

const defaultAuthContext = {
  isAuthenticated: true,
  accessToken: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature',
  claims: null,
  lens: 'tenant' as const,
  login: vi.fn(),
  logout: vi.fn(),
  refreshToken: vi.fn(),
}

describe('McpConnectionCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useTenantContext).mockReturnValue(defaultTenantContext)
    vi.mocked(useAuth).mockReturnValue(defaultAuthContext)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  describe('rendering', () => {
    it('renders card title', () => {
      render(<McpConnectionCard />, { wrapper: Wrapper })

      expect(screen.getByText('AI Connection')).toBeInTheDocument()
      expect(screen.getByText('MCP')).toBeInTheDocument()
    })

    it('renders MCP server URL', () => {
      render(<McpConnectionCard />, { wrapper: Wrapper })

      const urlEl = screen.getByTestId('mcp-card-url')
      expect(urlEl).toHaveTextContent('/mcp')
    })

    it('renders Claude Code config with streamable-http type', () => {
      render(<McpConnectionCard />, { wrapper: Wrapper })

      const configEl = screen.getByTestId('mcp-card-claude-config')
      expect(configEl).toHaveTextContent('streamable-http')
      expect(configEl).toHaveTextContent('mcpServers')
      expect(configEl).toHaveTextContent('/mcp')
    })

    it('renders truncated auth token preview', () => {
      render(<McpConnectionCard />, { wrapper: Wrapper })

      const tokenEl = screen.getByTestId('mcp-card-token-preview')
      expect(tokenEl).toHaveTextContent('...')
      // Should show first 12 chars
      expect(tokenEl).toHaveTextContent('eyJhbGciOiJI')
    })

    it('renders connection instructions', () => {
      render(<McpConnectionCard />, { wrapper: Wrapper })

      expect(screen.getByText(/Add the config above/i)).toBeInTheDocument()
      expect(screen.getAllByText(/.mcp.json/i).length).toBeGreaterThan(0)
    })

    it('does not render when no tenant selected', () => {
      vi.mocked(useTenantContext).mockReturnValue({
        tenantSlug: null,
        currentTenant: null,
        isPlatformAdmin: true,
        switchTenant: vi.fn(),
        clearTenant: vi.fn(),
      })

      const { container } = render(<McpConnectionCard />, { wrapper: Wrapper })

      expect(container).toBeEmptyDOMElement()
    })

    it('does not render token preview when no access token', () => {
      vi.mocked(useAuth).mockReturnValue({
        ...defaultAuthContext,
        accessToken: null,
      })

      render(<McpConnectionCard />, { wrapper: Wrapper })

      expect(screen.queryByTestId('mcp-card-token-preview')).not.toBeInTheDocument()
    })
  })

  describe('copy to clipboard', () => {
    it('copies MCP URL when clicking copy server URL button', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConnectionCard />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy MCP server URL/i })
      await act(async () => {
        copyButton.click()
      })

      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('/mcp'))
    })

    it('copies Claude Code config when clicking copy config button', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConnectionCard />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy Claude Code config/i })
      await act(async () => {
        copyButton.click()
      })

      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('mcpServers'))
    })

    it('shows Copied! feedback after clicking copy', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConnectionCard />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy MCP server URL/i })
      await act(async () => {
        copyButton.click()
      })

      await waitFor(() => {
        expect(screen.getByText('Copied!')).toBeInTheDocument()
      })
    })

    it('copies auth token when clicking copy token button', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConnectionCard />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy auth token/i })
      await act(async () => {
        copyButton.click()
      })

      expect(writeText).toHaveBeenCalledWith(defaultAuthContext.accessToken)
    })
  })
})

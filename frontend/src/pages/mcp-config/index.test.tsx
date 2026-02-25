import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { McpConfigPage } from './index'

// Mock tenant context for tests that need a specific tenant
vi.mock('@/contexts/tenant-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/contexts/tenant-context')>()
  return {
    ...actual,
    useTenantContext: vi.fn(() => ({
      tenantSlug: 'test-tenant',
      currentTenant: { id: 'tid', slug: 'test-tenant', name: 'Test Tenant' },
      isPlatformAdmin: false,
      switchTenant: vi.fn(),
      clearTenant: vi.fn(),
    })),
  }
})

import { useTenantContext } from '@/contexts/tenant-context'

function Wrapper({ children }: { children: React.ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>
}

describe('McpConfigPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering', () => {
    it('renders page title and description', () => {
      render(<McpConfigPage />, { wrapper: Wrapper })

      expect(screen.getByRole('heading', { name: /MCP Configuration/i })).toBeInTheDocument()
      expect(screen.getByText(/Model Context Protocol/i)).toBeInTheDocument()
    })

    it('renders server connection section with SSE URL', () => {
      render(<McpConfigPage />, { wrapper: Wrapper })

      expect(screen.getByText('Server Connection')).toBeInTheDocument()
      expect(screen.getByTestId('sse-url')).toHaveTextContent('/sse')
    })

    it('renders Claude Desktop config section with JSON', () => {
      render(<McpConfigPage />, { wrapper: Wrapper })

      expect(screen.getByText('Claude Desktop Configuration')).toBeInTheDocument()
      const configEl = screen.getByTestId('claude-desktop-config')
      expect(configEl).toHaveTextContent('mcpServers')
      expect(configEl).toHaveTextContent('meridian')
      expect(configEl).toHaveTextContent('mcp-remote')
    })

    it('renders OAuth authorization section', () => {
      render(<McpConfigPage />, { wrapper: Wrapper })

      expect(screen.getByText('OAuth Authorization')).toBeInTheDocument()
      expect(screen.getByTestId('oauth-url')).toHaveTextContent('/oauth/authorize')
    })

    it('renders documentation link', () => {
      render(<McpConfigPage />, { wrapper: Wrapper })

      const link = screen.getByTestId('readme-link')
      expect(link).toBeInTheDocument()
      expect(link).toHaveTextContent('MCP Server README')
      expect(link).toHaveAttribute('target', '_blank')
    })

    it('renders MCP tools accordion', () => {
      render(<McpConfigPage />, { wrapper: Wrapper })

      expect(screen.getByText('Available Capabilities')).toBeInTheDocument()
      expect(screen.getByText('Read Tools')).toBeInTheDocument()
      expect(screen.getByText('Simulate Tools')).toBeInTheDocument()
      expect(screen.getByText('Write Tools')).toBeInTheDocument()
      expect(screen.getByText('Resources')).toBeInTheDocument()
    })

    it('shows tenant badge when tenant is selected', () => {
      vi.mocked(useTenantContext).mockReturnValue({
        tenantSlug: 'my-tenant',
        currentTenant: { id: 'tid', slug: 'my-tenant', name: 'My Tenant' },
        isPlatformAdmin: false,
        switchTenant: vi.fn(),
        clearTenant: vi.fn(),
      })

      render(<McpConfigPage />, { wrapper: Wrapper })

      expect(screen.getByText('Tenant: my-tenant')).toBeInTheDocument()
    })

    it('does not show tenant badge when no tenant selected', () => {
      vi.mocked(useTenantContext).mockReturnValue({
        tenantSlug: null,
        currentTenant: null,
        isPlatformAdmin: true,
        switchTenant: vi.fn(),
        clearTenant: vi.fn(),
      })

      render(<McpConfigPage />, { wrapper: Wrapper })

      expect(screen.queryByText(/Tenant:/)).not.toBeInTheDocument()
    })
  })

  describe('copy to clipboard', () => {
    it('copies SSE URL on button click', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConfigPage />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy SSE URL/i })
      await act(async () => {
        copyButton.click()
      })

      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('/sse'))

      vi.unstubAllGlobals()
    })

    it('shows Copied! feedback after clicking copy', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConfigPage />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy SSE URL/i })
      await act(async () => {
        copyButton.click()
      })

      await waitFor(() => {
        expect(screen.getByText('Copied!')).toBeInTheDocument()
      })

      vi.unstubAllGlobals()
    })

    it('copies Claude Desktop config on button click', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConfigPage />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy Claude Desktop config/i })
      await act(async () => {
        copyButton.click()
      })

      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('mcpServers'))

      vi.unstubAllGlobals()
    })

    it('copies OAuth URL on button click', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

      render(<McpConfigPage />, { wrapper: Wrapper })

      const copyButton = screen.getByRole('button', { name: /Copy OAuth URL/i })
      await act(async () => {
        copyButton.click()
      })

      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('/oauth/authorize'))

      vi.unstubAllGlobals()
    })
  })

  describe('MCP tools accordion', () => {
    it('expands Read Tools accordion and shows tool names', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<McpConfigPage />, { wrapper: Wrapper })

      const readToolsTrigger = screen.getByRole('button', { name: /Read Tools/i })
      await user.click(readToolsTrigger)

      await waitFor(() => {
        expect(screen.getByText('meridian_economy_structure')).toBeInTheDocument()
        expect(screen.getByText('meridian_instruments_list')).toBeInTheDocument()
      })
    })

    it('expands Simulate Tools accordion and shows tool names', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<McpConfigPage />, { wrapper: Wrapper })

      const simulateTrigger = screen.getByRole('button', { name: /Simulate Tools/i })
      await user.click(simulateTrigger)

      await waitFor(() => {
        expect(screen.getByText('meridian_cel_validate')).toBeInTheDocument()
        expect(screen.getByText('meridian_saga_simulate')).toBeInTheDocument()
      })
    })

    it('expands Write Tools accordion and shows tool names', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<McpConfigPage />, { wrapper: Wrapper })

      const writeTrigger = screen.getByRole('button', { name: /Write Tools/i })
      await user.click(writeTrigger)

      await waitFor(() => {
        expect(screen.getByText('meridian_manifest_apply')).toBeInTheDocument()
      })
    })

    it('expands Resources accordion and shows resource URIs', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<McpConfigPage />, { wrapper: Wrapper })

      const resourcesTrigger = screen.getByRole('button', { name: /Resources/i })
      await user.click(resourcesTrigger)

      await waitFor(() => {
        expect(screen.getByText('meridian://tenant/manifest/current')).toBeInTheDocument()
      })
    })

    it('displays tool and resource counts in badges', () => {
      render(<McpConfigPage />, { wrapper: Wrapper })

      // Total: 13 read + 8 simulate + 1 write = 22 tools, 2 resources
      expect(screen.getByText('22 tools')).toBeInTheDocument()
      expect(screen.getByText('2 resources')).toBeInTheDocument()
    })
  })
})

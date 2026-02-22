import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Sidebar } from '@/components/layout/sidebar'

describe('Sidebar', () => {
  describe('tenant lens', () => {
    it('renders all tenant nav items', () => {
      render(<Sidebar lens="tenant" />)

      expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Internal Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Payments' })).toBeInTheDocument()
    })

    it('does not render platform-only nav items', () => {
      render(<Sidebar lens="tenant" />)

      expect(screen.queryByRole('link', { name: /tenant management/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /platform monitoring/i })).not.toBeInTheDocument()
    })

    it('does not render separator between tenant and platform sections', () => {
      const { container } = render(<Sidebar lens="tenant" />)
      // No separator role element should appear
      expect(container.querySelector('[role="separator"]')).not.toBeInTheDocument()
    })
  })

  describe('platform lens', () => {
    it('renders all tenant nav items', () => {
      render(<Sidebar lens="platform" />)

      expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Internal Accounts' })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Payments' })).toBeInTheDocument()
    })

    it('renders platform-only nav items', () => {
      render(<Sidebar lens="platform" />)

      expect(screen.getByRole('link', { name: /tenant management/i })).toBeInTheDocument()
      expect(screen.getByRole('link', { name: /platform monitoring/i })).toBeInTheDocument()
    })

    it('renders separator between tenant and platform sections', () => {
      const { container } = render(<Sidebar lens="platform" />)
      expect(container.querySelector('[role="separator"]')).toBeInTheDocument()
    })
  })

  describe('active state', () => {
    it('marks the current path link as active', () => {
      render(<Sidebar lens="tenant" currentPath="/" />)

      const dashboardLink = screen.getByRole('link', { name: /dashboard/i })
      expect(dashboardLink).toHaveAttribute('aria-current', 'page')
    })

    it('does not mark non-current links as active', () => {
      render(<Sidebar lens="tenant" currentPath="/" />)

      const accountsLink = screen.getByRole('link', { name: 'Accounts' })
      expect(accountsLink).not.toHaveAttribute('aria-current', 'page')
    })

    it('marks accounts link active when on /accounts path', () => {
      render(<Sidebar lens="tenant" currentPath="/accounts" />)

      const accountsLink = screen.getByRole('link', { name: 'Accounts' })
      expect(accountsLink).toHaveAttribute('aria-current', 'page')
    })
  })

  describe('mobile collapsed state', () => {
    it('accepts isOpen prop and renders with open state', () => {
      const { container } = render(<Sidebar lens="tenant" isOpen={true} />)
      expect(container.firstChild).toHaveAttribute('data-open', 'true')
    })

    it('renders with closed state when isOpen is false', () => {
      const { container } = render(<Sidebar lens="tenant" isOpen={false} />)
      expect(container.firstChild).toHaveAttribute('data-open', 'false')
    })
  })

  describe('keyboard navigation', () => {
    it('nav links are keyboard focusable', async () => {
      render(<Sidebar lens="tenant" />)

      const dashboardLink = screen.getByRole('link', { name: /dashboard/i })
      dashboardLink.focus()
      expect(dashboardLink).toHaveFocus()
    })
  })

  describe('navigation label', () => {
    it('has an accessible nav landmark', () => {
      render(<Sidebar lens="tenant" />)
      expect(screen.getByRole('navigation')).toBeInTheDocument()
    })
  })
})

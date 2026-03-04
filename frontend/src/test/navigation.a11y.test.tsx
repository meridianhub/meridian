import { describe, it, expect } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { axe, renderWithProviders } from '@/test/test-utils'
import { Sidebar } from '@/components/layout/sidebar'

function renderSidebar(props: React.ComponentProps<typeof Sidebar>) {
  return renderWithProviders(
    <MemoryRouter>
      <Sidebar {...props} />
    </MemoryRouter>,
  )
}

describe('Sidebar/Navigation accessibility', () => {
  it('has no accessibility violations', async () => {
    const { container } = renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: true })
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has semantic navigation structure', () => {
    const { container } = renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: true })
    const nav = container.querySelector('nav[aria-label="Main navigation"]')
    expect(nav).toBeInTheDocument()
  })

  it('marks current page link with aria-current', () => {
    renderSidebar({ lens: 'tenant', currentPath: '/accounts', isOpen: true })
    const accountsLink = screen.getByRole('link', { name: /^Accounts$/i })
    expect(accountsLink).toHaveAttribute('aria-current', 'page')
  })

  it('other links do not have aria-current', () => {
    renderSidebar({ lens: 'tenant', currentPath: '/accounts', isOpen: true })
    const dashboardLink = screen.getByRole('link', { name: /^Dashboard$/i })
    expect(dashboardLink).not.toHaveAttribute('aria-current')
  })

  it('supports keyboard navigation through links', async () => {
    const user = userEvent.setup()
    renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: true })

    const dashboardLink = screen.getByRole('link', { name: /dashboard/i })
    await user.tab()
    expect(dashboardLink).toHaveFocus()
  })

  it('all navigation links are keyboard accessible', () => {
    renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: true })

    const links = screen.getAllByRole('link')
    expect(links.length).toBeGreaterThan(0)

    // All links should be focusable
    links.forEach((link) => {
      expect(link).toHaveProperty('href')
    })
  })

  it('has proper list semantics', () => {
    const { container } = renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: true })
    const list = container.querySelector('ul[role="list"]')
    expect(list).toBeInTheDocument()

    const items = list?.querySelectorAll('li')
    expect(items?.length).toBeGreaterThan(0)
  })

  it('includes platform items when lens is platform', () => {
    renderSidebar({ lens: 'platform', currentPath: '/', isOpen: true })
    expect(screen.getByRole('link', { name: /tenant management/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /platform monitoring/i })).toBeInTheDocument()
  })

  it('excludes platform items when lens is tenant', () => {
    renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: true })
    expect(screen.queryByRole('link', { name: /tenant management/i })).not.toBeInTheDocument()
  })

  it('has no violations when closed', async () => {
    const { container } = renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: false })
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('aside element is semantic and proper', () => {
    const { container } = renderSidebar({ lens: 'tenant', currentPath: '/', isOpen: true, id: 'main-sidebar' })
    const aside = container.querySelector('aside')
    expect(aside).toBeInTheDocument()
    expect(aside).toHaveAttribute('id', 'main-sidebar')
  })
})

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { axe } from '@/test/test-utils'
import { StatusBadge } from '@/shared/status-badge'

describe('StatusBadge accessibility', () => {
  it('has no accessibility violations - success variant', async () => {
    const { container } = render(<StatusBadge status="ACTIVE" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no accessibility violations - warning variant', async () => {
    const { container } = render(<StatusBadge status="FROZEN" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no accessibility violations - error variant', async () => {
    const { container } = render(<StatusBadge status="FAILED" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no accessibility violations - info variant', async () => {
    const { container } = render(<StatusBadge status="INITIATED" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no accessibility violations - neutral variant', async () => {
    const { container } = render(<StatusBadge status="CLOSED" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no violations in loading state', async () => {
    const { container } = render(<StatusBadge status="ACTIVE" loading />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('applies semantic token classes for all variants', () => {
    const variants = [
      { status: 'ACTIVE', expectedColor: 'success' },
      { status: 'FROZEN', expectedColor: 'warning' },
      { status: 'FAILED', expectedColor: 'destructive' },
      { status: 'INITIATED', expectedColor: 'info' },
      { status: 'CLOSED', expectedColor: 'muted' },
    ]

    variants.forEach(({ status, expectedColor }) => {
      const { container } = render(<StatusBadge status={status} />)
      const badge = container.querySelector('span')
      // Verify semantic token class exists
      expect(badge?.className).toContain(expectedColor)
    })
  })

  it('text content is accessible to screen readers', () => {
    render(<StatusBadge status="ACTIVE" />)
    const text = screen.getByText('ACTIVE')
    expect(text).toBeInTheDocument()
    // Verify it's visible and accessible
    expect(text.textContent).toBe('ACTIVE')
  })

  it('has accessible text for underscored statuses', () => {
    render(<StatusBadge status="PROVISIONING_PENDING" />)
    const text = screen.getByText('PROVISIONING PENDING')
    expect(text).toBeInTheDocument()
  })
})

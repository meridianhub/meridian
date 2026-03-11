import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PageShell } from './page-shell'

describe('PageShell', () => {
  it('renders children', () => {
    render(<PageShell><p>Child content</p></PageShell>)
    expect(screen.getByText('Child content')).toBeInTheDocument()
  })

  it('applies space-y-6 class', () => {
    const { container } = render(<PageShell><p>Content</p></PageShell>)
    expect(container.firstChild).toHaveClass('space-y-6')
  })

  it('applies custom className', () => {
    const { container } = render(<PageShell className="custom-class"><p>Content</p></PageShell>)
    expect(container.firstChild).toHaveClass('custom-class')
  })
})

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PageHeader } from './page-header'

describe('PageHeader', () => {
  it('renders title with correct heading level', () => {
    render(<PageHeader title="Test Title" />)
    const heading = screen.getByRole('heading', { level: 1 })
    expect(heading).toHaveTextContent('Test Title')
  })

  it('renders description when provided', () => {
    render(<PageHeader title="Title" description="A description" />)
    expect(screen.getByText('A description')).toBeInTheDocument()
  })

  it('renders actions when provided', () => {
    render(<PageHeader title="Title" actions={<button>Action</button>} />)
    expect(screen.getByRole('button', { name: 'Action' })).toBeInTheDocument()
  })

  it('does not render description when not provided', () => {
    const { container } = render(<PageHeader title="Title" />)
    expect(container.querySelector('p')).toBeNull()
  })

  it('does not render actions when not provided', () => {
    const { container } = render(<PageHeader title="Title" />)
    // The actions wrapper div should not be present
    const divs = container.querySelectorAll('div')
    // Only root div and the space-y-1 div for title
    expect(divs.length).toBe(2)
  })

  it('applies custom className', () => {
    const { container } = render(<PageHeader title="Title" className="custom-class" />)
    expect(container.firstChild).toHaveClass('custom-class')
  })
})

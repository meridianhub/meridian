import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Plus } from 'lucide-react'
import { EmptyState } from './empty-state'

describe('EmptyState', () => {
  it('renders title text', () => {
    render(
      <EmptyState title="No data found" />
    )
    expect(screen.getByText('No data found')).toBeInTheDocument()
  })

  it('renders description when provided', () => {
    render(
      <EmptyState
        title="No data found"
        description="Try adjusting your filters"
      />
    )
    expect(screen.getByText('Try adjusting your filters')).toBeInTheDocument()
  })

  it('does not render description when not provided', () => {
    const { container } = render(
      <EmptyState title="No data found" />
    )
    const description = container.querySelector('[data-slot="empty-state-description"]')
    expect(description).not.toBeInTheDocument()
  })

  it('renders default icon (FileQuestion) when no icon provided', () => {
    const { container } = render(
      <EmptyState title="No data found" />
    )
    const iconContainer = container.querySelector('[data-slot="empty-state-icon"]')
    expect(iconContainer).toBeInTheDocument()
  })

  it('renders custom icon when provided', () => {
    const { container } = render(
      <EmptyState
        title="No items"
        icon={Plus}
      />
    )
    const iconContainer = container.querySelector('[data-slot="empty-state-icon"]')
    expect(iconContainer).toBeInTheDocument()
  })

  it('renders action button when provided', () => {
    const handleClick = vi.fn()
    render(
      <EmptyState
        title="No data"
        action={{ label: 'Create New', onClick: handleClick }}
      />
    )
    expect(screen.getByText('Create New')).toBeInTheDocument()
  })

  it('does not render action button when not provided', () => {
    render(
      <EmptyState title="No data" />
    )
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('calls onClick when action button is clicked', async () => {
    const user = userEvent.setup()
    const handleClick = vi.fn()
    render(
      <EmptyState
        title="No data"
        action={{ label: 'Create New', onClick: handleClick }}
      />
    )

    const button = screen.getByText('Create New')
    await user.click(button)

    expect(handleClick).toHaveBeenCalledOnce()
  })

  it('has centered layout styling', () => {
    const { container } = render(
      <EmptyState title="No data" />
    )
    const wrapper = container.querySelector('[data-slot="empty-state"]')
    expect(wrapper?.className).toMatch(/flex/)
    expect(wrapper?.className).toMatch(/justify-center/)
    expect(wrapper?.className).toMatch(/items-center/)
  })

  it('renders with proper spacing between elements', () => {
    const { container } = render(
      <EmptyState
        title="No data"
        description="No description available"
        action={{ label: 'Create', onClick: vi.fn() }}
      />
    )
    const contentContainer = container.querySelector('[data-slot="empty-state-content"]')
    expect(contentContainer?.className).toMatch(/gap/)
  })
})

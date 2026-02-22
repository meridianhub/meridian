import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { axe } from 'vitest-axe'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

describe('Button component accessibility', () => {
  it('has no accessibility violations', async () => {
    const { container } = render(<Button>Click me</Button>)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has proper focus indicators', async () => {
    const { container } = render(<Button>Click me</Button>)
    const button = container.querySelector('button')
    expect(button).toHaveClass('focus-visible:ring-ring/50')
  })

  it('is keyboard accessible', async () => {
    const user = userEvent.setup()
    render(<Button>Click me</Button>)
    const button = screen.getByRole('button', { name: /click me/i })

    await user.tab()
    expect(button).toHaveFocus()
  })

  it('supports disabled state with proper semantics', async () => {
    const { container } = render(<Button disabled>Disabled</Button>)
    const button = container.querySelector('button')
    expect(button).toBeDisabled()
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('all variants have no violations', async () => {
    const variants = ['default', 'destructive', 'outline', 'secondary', 'ghost', 'link']

    for (const variant of variants) {
      const { container } = render(
        <Button variant={variant as any}>Test</Button>
      )
      const results = await axe(container)
      expect(results).toHaveNoViolations()
    }
  })

  it('renders accessible button with aria attributes', () => {
    render(<Button aria-label="Close dialog">×</Button>)
    const button = screen.getByRole('button', { name: /close dialog/i })
    expect(button).toBeInTheDocument()
  })
})

describe('Input component accessibility', () => {
  it('has no accessibility violations', async () => {
    const { container } = render(<Input placeholder="Enter text" aria-label="Text input" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('is keyboard accessible and focusable', async () => {
    const user = userEvent.setup()
    render(<Input placeholder="Enter text" />)
    const input = screen.getByPlaceholderText('Enter text')

    await user.tab()
    expect(input).toHaveFocus()
  })

  it('has proper focus indicators', () => {
    const { container } = render(<Input />)
    const input = container.querySelector('input')
    expect(input).toHaveClass('focus-visible:ring-ring/50')
  })

  it('supports aria-label for accessibility', () => {
    render(<Input aria-label="Search products" />)
    const input = screen.getByLabelText('Search products')
    expect(input).toBeInTheDocument()
  })

  it('supports aria-describedby for error messages', async () => {
    const { container } = render(
      <>
        <Input
          aria-label="Email input"
          aria-describedby="error-msg"
          aria-invalid={true}
        />
        <span id="error-msg">This field is required</span>
      </>
    )
    const input = container.querySelector('input')
    expect(input).toHaveAttribute('aria-describedby', 'error-msg')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('supports disabled state with proper semantics', async () => {
    const { container } = render(<Input disabled aria-label="Disabled input" />)
    const input = container.querySelector('input')
    expect(input).toBeDisabled()
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('supports placeholder without replacing label', () => {
    render(
      <div>
        <label htmlFor="search">Search</label>
        <Input id="search" placeholder="Enter search term" />
      </div>
    )
    const input = screen.getByLabelText('Search')
    expect(input).toHaveAttribute('placeholder', 'Enter search term')
  })
})

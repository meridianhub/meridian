import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { axe } from '@/test/test-utils'
import { MoneyDisplay } from '@/shared/money-display'

describe('MoneyDisplay accessibility', () => {
  it('has no accessibility violations', async () => {
    const { container } = render(<MoneyDisplay amount={10000n} currency="GBP" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no violations with null amount', async () => {
    const { container } = render(<MoneyDisplay amount={null} currency="GBP" />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no violations with different currencies', async () => {
    const { container } = render(
      <MoneyDisplay amount={1500000n} currency="kWh" />
    )
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('renders semantic HTML for screen readers', () => {
    const { container } = render(<MoneyDisplay amount={10000n} currency="GBP" />)
    const span = container.querySelector('span')
    expect(span).toBeInTheDocument()
    // Should have text content that screen readers can access
    expect(span?.textContent).toBeTruthy()
  })
})

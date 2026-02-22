import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BalanceIndicator } from './balance-indicator'

describe('BalanceIndicator', () => {
  it('shows balanced state when debits equal credits', () => {
    render(<BalanceIndicator debitTotal={10000n} creditTotal={10000n} currency="GBP" />)
    expect(screen.getByTestId('balance-indicator')).toBeInTheDocument()
    expect(screen.getByTestId('balance-indicator')).toHaveAttribute('data-balanced', 'true')
    expect(screen.getByText(/balanced/i)).toBeInTheDocument()
  })

  it('shows unbalanced state when debits differ from credits', () => {
    render(<BalanceIndicator debitTotal={10000n} creditTotal={5000n} currency="GBP" />)
    expect(screen.getByTestId('balance-indicator')).toHaveAttribute('data-balanced', 'false')
    expect(screen.getByText(/unbalanced/i)).toBeInTheDocument()
  })

  it('shows zero amounts as balanced', () => {
    render(<BalanceIndicator debitTotal={0n} creditTotal={0n} currency="GBP" />)
    expect(screen.getByTestId('balance-indicator')).toHaveAttribute('data-balanced', 'true')
  })

  it('renders debit total', () => {
    render(<BalanceIndicator debitTotal={10000n} creditTotal={10000n} currency="GBP" />)
    expect(screen.getByTestId('debit-total')).toBeInTheDocument()
  })

  it('renders credit total', () => {
    render(<BalanceIndicator debitTotal={10000n} creditTotal={10000n} currency="GBP" />)
    expect(screen.getByTestId('credit-total')).toBeInTheDocument()
  })

  it('shows difference when unbalanced', () => {
    render(<BalanceIndicator debitTotal={15000n} creditTotal={10000n} currency="GBP" />)
    expect(screen.getByTestId('balance-difference')).toBeInTheDocument()
  })

  it('hides difference when balanced', () => {
    render(<BalanceIndicator debitTotal={10000n} creditTotal={10000n} currency="GBP" />)
    expect(screen.queryByTestId('balance-difference')).not.toBeInTheDocument()
  })
})

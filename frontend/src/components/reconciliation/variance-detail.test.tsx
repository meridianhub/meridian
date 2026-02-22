import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { VarianceDetail, type Variance } from './variance-detail'

const baseVariance: Variance = {
  varianceId: 'var-001',
  reasonCode: 'AMOUNT_MISMATCH',
  expected: {
    amount: '10000',
    currency: 'GBP',
    direction: 'DEBIT',
    entryId: 'entry-exp-1',
  },
  actual: {
    amount: '9500',
    currency: 'GBP',
    direction: 'DEBIT',
    entryId: 'entry-act-1',
  },
}

describe('VarianceDetail - rendering', () => {
  it('renders the variance ID', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('var-001')).toBeInTheDocument()
  })

  it('renders the reason code badge', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('AMOUNT_MISMATCH')).toBeInTheDocument()
  })

  it('renders Expected and Actual section labels', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('Expected')).toBeInTheDocument()
    expect(screen.getByText('Actual')).toBeInTheDocument()
  })

  it('renders expected side direction', () => {
    render(<VarianceDetail variance={baseVariance} />)
    const directionTexts = screen.getAllByText(/Direction: DEBIT/)
    expect(directionTexts.length).toBeGreaterThanOrEqual(1)
  })

  it('renders expected side entry ID', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText(/entry-exp-1/)).toBeInTheDocument()
  })

  it('renders actual side entry ID', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText(/entry-act-1/)).toBeInTheDocument()
  })

  it('shows "No entry" when expected is null', () => {
    const v: Variance = { ...baseVariance, expected: null }
    render(<VarianceDetail variance={v} />)
    expect(screen.getByText('No entry')).toBeInTheDocument()
  })

  it('shows "No entry" when actual is null', () => {
    const v: Variance = { ...baseVariance, actual: null }
    render(<VarianceDetail variance={v} />)
    expect(screen.getByText('No entry')).toBeInTheDocument()
  })

  it('renders notes when provided', () => {
    const v: Variance = { ...baseVariance, notes: 'Investigate this discrepancy' }
    render(<VarianceDetail variance={v} />)
    expect(screen.getByText('Investigate this discrepancy')).toBeInTheDocument()
  })

  it('does not render notes section when notes is absent', () => {
    render(<VarianceDetail variance={baseVariance} />)
    // baseVariance has no notes
    expect(screen.queryByText(/investigate/i)).not.toBeInTheDocument()
  })

  it('has data-testid variance-detail', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByTestId('variance-detail')).toBeInTheDocument()
  })
})

describe('VarianceDetail - reason codes', () => {
  const reasonCodes = [
    'AMOUNT_MISMATCH',
    'MISSING_ENTRY',
    'DUPLICATE_ENTRY',
    'TIMING_DIFFERENCE',
    'CURRENCY_MISMATCH',
    'DIRECTION_ERROR',
    'QUALITY_UPGRADE',
    'EXTERNAL_MISMATCH',
    'CORRECTION_APPLIED',
  ] as const

  for (const code of reasonCodes) {
    it(`renders reason code: ${code}`, () => {
      const v: Variance = { ...baseVariance, reasonCode: code }
      render(<VarianceDetail variance={v} />)
      expect(screen.getByText(code)).toBeInTheDocument()
    })
  }
})

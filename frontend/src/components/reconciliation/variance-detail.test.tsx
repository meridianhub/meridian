import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { VarianceDetail, type Variance } from './variance-detail'

const baseVariance: Variance = {
  varianceId: 'var-001',
  runId: 'run-001',
  snapshotId: 'snap-001',
  accountId: 'acc-001',
  instrumentCode: 'GBP',
  expectedAmount: '100.00',
  actualAmount: '95.00',
  varianceAmount: '-5.00',
  reason: 'VARIANCE_REASON_AMOUNT_MISMATCH',
  status: 'VARIANCE_STATUS_OPEN',
  createdAt: '2026-02-23T00:00:00Z',
  updatedAt: '2026-02-23T00:00:00Z',
}

describe('VarianceDetail - rendering', () => {
  it('renders the variance ID', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('var-001')).toBeInTheDocument()
  })

  it('renders the reason code badge with prefix stripped', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('AMOUNT_MISMATCH')).toBeInTheDocument()
  })

  it('renders the status badge with prefix stripped', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('OPEN')).toBeInTheDocument()
  })

  it('renders Expected, Actual, and Variance section labels', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('Expected')).toBeInTheDocument()
    expect(screen.getByText('Actual')).toBeInTheDocument()
    expect(screen.getByText('Variance')).toBeInTheDocument()
  })

  it('renders expected amount', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('100.00')).toBeInTheDocument()
  })

  it('renders actual amount', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('95.00')).toBeInTheDocument()
  })

  it('renders variance amount', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText('-5.00')).toBeInTheDocument()
  })

  it('renders account and instrument', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByText(/acc-001/)).toBeInTheDocument()
    expect(screen.getByText(/GBP/)).toBeInTheDocument()
  })

  it('renders resolution note when provided', () => {
    const v: Variance = { ...baseVariance, resolutionNote: 'Accepted as timing difference' }
    render(<VarianceDetail variance={v} />)
    expect(screen.getByText('Accepted as timing difference')).toBeInTheDocument()
  })

  it('does not render resolution note section when absent', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.queryByText(/accepted/i)).not.toBeInTheDocument()
  })

  it('has data-testid variance-detail', () => {
    render(<VarianceDetail variance={baseVariance} />)
    expect(screen.getByTestId('variance-detail')).toBeInTheDocument()
  })
})

describe('VarianceDetail - reason codes', () => {
  const reasonCodes = [
    'VARIANCE_REASON_AMOUNT_MISMATCH',
    'VARIANCE_REASON_MISSING_ENTRY',
    'VARIANCE_REASON_DUPLICATE_ENTRY',
    'VARIANCE_REASON_TIMING_DIFFERENCE',
    'VARIANCE_REASON_CURRENCY_MISMATCH',
    'VARIANCE_REASON_DIRECTION_ERROR',
    'VARIANCE_REASON_OTHER',
  ] as const

  for (const code of reasonCodes) {
    it(`renders reason code: ${code}`, () => {
      const v: Variance = { ...baseVariance, reason: code }
      render(<VarianceDetail variance={v} />)
      expect(screen.getByText(code.replace('VARIANCE_REASON_', ''))).toBeInTheDocument()
    })
  }
})

describe('VarianceDetail - status codes', () => {
  const statuses = [
    'VARIANCE_STATUS_OPEN',
    'VARIANCE_STATUS_INVESTIGATING',
    'VARIANCE_STATUS_DISPUTED',
    'VARIANCE_STATUS_RESOLVED',
    'VARIANCE_STATUS_ACCEPTED',
  ] as const

  for (const status of statuses) {
    it(`renders status: ${status}`, () => {
      const v: Variance = { ...baseVariance, status }
      render(<VarianceDetail variance={v} />)
      expect(screen.getByText(status.replace('VARIANCE_STATUS_', ''))).toBeInTheDocument()
    })
  }
})

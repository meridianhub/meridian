import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BookingLogHeader } from './booking-log-header'
import type { FinancialBookingLog } from './types'

const mockBookingLog: FinancialBookingLog = {
  id: 'log-001',
  financialAccountType: 'CURRENT',
  productServiceReference: 'PRODUCT-A',
  businessUnitReference: 'BU-TRADING',
  chartOfAccountsRules: 'STANDARD',
  instrumentCode: 'GBP',
  status: 'PENDING',
  createdAt: { seconds: 1700000000n, nanos: 0 },
  updatedAt: { seconds: 1700001000n, nanos: 0 },
  postings: [],
}

describe('BookingLogHeader', () => {
  it('renders the booking log ID', () => {
    render(<BookingLogHeader bookingLog={mockBookingLog} />)
    expect(screen.getByText('log-001')).toBeInTheDocument()
  })

  it('renders the account type', () => {
    render(<BookingLogHeader bookingLog={mockBookingLog} />)
    expect(screen.getByText('CURRENT')).toBeInTheDocument()
  })

  it('renders the business unit reference', () => {
    render(<BookingLogHeader bookingLog={mockBookingLog} />)
    expect(screen.getByText('BU-TRADING')).toBeInTheDocument()
  })

  it('renders the status badge', () => {
    render(<BookingLogHeader bookingLog={mockBookingLog} />)
    expect(screen.getByText('PENDING')).toBeInTheDocument()
  })

  it('renders the base currency', () => {
    render(<BookingLogHeader bookingLog={mockBookingLog} />)
    expect(screen.getByText('GBP')).toBeInTheDocument()
  })

  it('renders posting count from postings array', () => {
    render(<BookingLogHeader bookingLog={mockBookingLog} />)
    expect(screen.getByTestId('posting-count')).toHaveTextContent('0')
  })

  it('renders posting count when postings exist', () => {
    const logWithPostings: FinancialBookingLog = {
      ...mockBookingLog,
      postings: [
        {
          id: 'p-001',
          financialBookingLogId: 'log-001',
          postingDirection: 'DEBIT',
          postingAmount: { currencyCode: 'GBP', units: 100n, nanos: 0 },
          accountId: 'acct-1',
          valueDate: { seconds: 1700000000n, nanos: 0 },
          postingResult: '',
          createdAt: { seconds: 1700000000n, nanos: 0 },
          status: 'PENDING',
        },
      ],
    }
    render(<BookingLogHeader bookingLog={logWithPostings} />)
    expect(screen.getByTestId('posting-count')).toHaveTextContent('1')
  })
})

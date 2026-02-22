import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DirectionBadge } from './direction-badge'

describe('DirectionBadge', () => {
  it('renders DEBIT direction', () => {
    render(<DirectionBadge direction="DEBIT" />)
    expect(screen.getByText('DEBIT')).toBeInTheDocument()
  })

  it('renders CREDIT direction', () => {
    render(<DirectionBadge direction="CREDIT" />)
    expect(screen.getByText('CREDIT')).toBeInTheDocument()
  })

  it('applies debit styling for DEBIT direction', () => {
    render(<DirectionBadge direction="DEBIT" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge).toHaveAttribute('data-direction', 'DEBIT')
  })

  it('applies credit styling for CREDIT direction', () => {
    render(<DirectionBadge direction="CREDIT" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge).toHaveAttribute('data-direction', 'CREDIT')
  })

  it('renders unknown direction gracefully', () => {
    render(<DirectionBadge direction="UNKNOWN" />)
    expect(screen.getByText('UNKNOWN')).toBeInTheDocument()
  })
})

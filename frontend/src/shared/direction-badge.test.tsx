import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DirectionBadge } from './direction-badge'

describe('DirectionBadge', () => {
  it('renders CREDIT badge with correct label', () => {
    render(<DirectionBadge direction="CREDIT" />)
    expect(screen.getByTestId('direction-badge')).toBeInTheDocument()
    expect(screen.getByText('Credit')).toBeInTheDocument()
  })

  it('renders DEBIT badge with correct label', () => {
    render(<DirectionBadge direction="DEBIT" />)
    expect(screen.getByText('Debit')).toBeInTheDocument()
  })

  it('applies success styling for CREDIT', () => {
    render(<DirectionBadge direction="CREDIT" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge.className).toMatch(/success/)
  })

  it('applies destructive styling for DEBIT', () => {
    render(<DirectionBadge direction="DEBIT" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge.className).toMatch(/destructive/)
  })

  it('sets data-direction attribute', () => {
    render(<DirectionBadge direction="CREDIT" />)
    expect(screen.getByTestId('direction-badge')).toHaveAttribute('data-direction', 'CREDIT')
  })

  it('treats unknown direction as DEBIT (destructive)', () => {
    render(<DirectionBadge direction="UNKNOWN" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge.className).toMatch(/destructive/)
  })
})

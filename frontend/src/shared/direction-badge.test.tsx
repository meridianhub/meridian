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

  it('applies filled success styling for CREDIT', () => {
    render(<DirectionBadge direction="CREDIT" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge.className).toMatch(/success/)
  })

  it('applies outlined ink styling for DEBIT (debit is not an error)', () => {
    render(<DirectionBadge direction="DEBIT" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge.className).toMatch(/border-foreground/)
    expect(badge.className).not.toMatch(/destructive/)
  })

  it('sets data-direction attribute', () => {
    render(<DirectionBadge direction="CREDIT" />)
    expect(screen.getByTestId('direction-badge')).toHaveAttribute('data-direction', 'CREDIT')
  })

  it('treats unknown direction as DEBIT (outlined ink)', () => {
    render(<DirectionBadge direction="UNKNOWN" />)
    const badge = screen.getByTestId('direction-badge')
    expect(badge.className).toMatch(/border-foreground/)
    expect(badge.className).not.toMatch(/success/)
  })
})

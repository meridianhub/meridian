import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QualityLadderBadge } from './quality-ladder-badge'

describe('QualityLadderBadge', () => {
  it('renders ESTIMATE badge with correct label', () => {
    render(<QualityLadderBadge quality="ESTIMATE" />)
    expect(screen.getByTestId('quality-ladder-badge')).toBeInTheDocument()
    expect(screen.getByText('Estimate')).toBeInTheDocument()
  })

  it('renders COEFFICIENT badge with correct label', () => {
    render(<QualityLadderBadge quality="COEFFICIENT" />)
    expect(screen.getByText('Coefficient')).toBeInTheDocument()
  })

  it('renders ACTUAL badge with correct label', () => {
    render(<QualityLadderBadge quality="ACTUAL" />)
    expect(screen.getByText('Actual')).toBeInTheDocument()
  })

  it('renders REVISED badge with correct label', () => {
    render(<QualityLadderBadge quality="REVISED" />)
    expect(screen.getByText('Revised')).toBeInTheDocument()
  })

  it('sets data-quality attribute for each quality level', () => {
    const { rerender } = render(<QualityLadderBadge quality="ESTIMATE" />)
    expect(screen.getByTestId('quality-ladder-badge')).toHaveAttribute('data-quality', 'ESTIMATE')

    rerender(<QualityLadderBadge quality="ACTUAL" />)
    expect(screen.getByTestId('quality-ladder-badge')).toHaveAttribute('data-quality', 'ACTUAL')
  })

  it('falls back to ESTIMATE for unknown quality values', () => {
    render(<QualityLadderBadge quality="UNKNOWN_QUALITY" />)
    // Should not throw and renders with fallback label
    expect(screen.getByTestId('quality-ladder-badge')).toBeInTheDocument()
    expect(screen.getByText('Estimate')).toBeInTheDocument()
  })

  it('hides label when showLabel is false', () => {
    render(<QualityLadderBadge quality="ACTUAL" showLabel={false} />)
    expect(screen.queryByText('Actual')).not.toBeInTheDocument()
    // Badge element is still present
    expect(screen.getByTestId('quality-ladder-badge')).toBeInTheDocument()
  })

  it('applies warning token for ESTIMATE quality', () => {
    render(<QualityLadderBadge quality="ESTIMATE" />)
    const badge = screen.getByTestId('quality-ladder-badge')
    expect(badge.className).toMatch(/warning/)
  })

  it('applies success token for ACTUAL quality', () => {
    render(<QualityLadderBadge quality="ACTUAL" />)
    const badge = screen.getByTestId('quality-ladder-badge')
    expect(badge.className).toMatch(/success/)
  })

  it('applies warning token for COEFFICIENT quality (estimate-quality data)', () => {
    render(<QualityLadderBadge quality="COEFFICIENT" />)
    const badge = screen.getByTestId('quality-ladder-badge')
    expect(badge.className).toMatch(/warning/)
  })

  it('applies accent token for REVISED quality', () => {
    render(<QualityLadderBadge quality="REVISED" />)
    const badge = screen.getByTestId('quality-ladder-badge')
    expect(badge.className).toMatch(/accent/)
  })
})

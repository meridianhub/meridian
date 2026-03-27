import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EmailDeliveryStatusBadge } from './email-delivery-status-badge'
import type { EmailDeliveryStatus } from '../api/types'

describe('EmailDeliveryStatusBadge', () => {
  it('renders "No email" when status is undefined', () => {
    render(<EmailDeliveryStatusBadge status={undefined} />)
    expect(screen.getByText('No email')).toBeInTheDocument()
  })

  it.each([
    ['PENDING', 'Pending'],
    ['SENT', 'Sent'],
    ['DELIVERED', 'Delivered'],
    ['BOUNCED', 'Bounced'],
    ['DEAD_LETTER', 'Dead Letter'],
    ['CANCELLED', 'Cancelled'],
  ] as const)('renders "%s" status with label "%s"', (status, label) => {
    render(<EmailDeliveryStatusBadge status={{ status }} />)
    expect(screen.getByText(label)).toBeInTheDocument()
  })

  it('renders compact mode with first character only', () => {
    render(<EmailDeliveryStatusBadge status={{ status: 'DELIVERED' }} compact />)
    expect(screen.getByText('D')).toBeInTheDocument()
  })

  it('renders without tooltip when no timestamp data', () => {
    const { container } = render(
      <EmailDeliveryStatusBadge status={{ status: 'SENT' }} />,
    )
    // No tooltip trigger wrapper when no additional data
    expect(container.querySelector('[data-slot="tooltip-trigger"]')).toBeNull()
  })

  it('renders with tooltip trigger when sentAt is provided', () => {
    const status: EmailDeliveryStatus = {
      status: 'SENT',
      sentAt: '2026-01-15T10:00:00.000Z',
    }
    const { container } = render(<EmailDeliveryStatusBadge status={status} />)
    expect(container.querySelector('[data-slot="tooltip-trigger"]')).toBeInTheDocument()
  })

  it('renders with tooltip trigger when deliveredAt is provided', () => {
    const status: EmailDeliveryStatus = {
      status: 'DELIVERED',
      deliveredAt: '2026-01-15T10:05:00.000Z',
    }
    const { container } = render(<EmailDeliveryStatusBadge status={status} />)
    expect(container.querySelector('[data-slot="tooltip-trigger"]')).toBeInTheDocument()
  })

  it('renders with tooltip trigger when bounceReason is provided', () => {
    const status: EmailDeliveryStatus = {
      status: 'BOUNCED',
      bounceReason: 'Invalid recipient',
    }
    const { container } = render(<EmailDeliveryStatusBadge status={status} />)
    expect(container.querySelector('[data-slot="tooltip-trigger"]')).toBeInTheDocument()
  })
})

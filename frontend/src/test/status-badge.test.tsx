import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StatusBadge } from '@/shared/status-badge'

describe('StatusBadge', () => {
  describe('status text formatting', () => {
    it('replaces underscores with spaces', () => {
      render(<StatusBadge status="PROVISIONING_PENDING" />)
      expect(screen.getByText('PROVISIONING PENDING')).toBeInTheDocument()
    })

    it('renders simple status without modification', () => {
      render(<StatusBadge status="ACTIVE" />)
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
  })

  describe('status variant mapping', () => {
    it('maps ACTIVE to success variant', () => {
      render(<StatusBadge status="ACTIVE" />)
      const badge = screen.getByText('ACTIVE')
      expect(badge.className).toMatch(/success/)
    })

    it('maps FROZEN to warning variant', () => {
      render(<StatusBadge status="FROZEN" />)
      const badge = screen.getByText('FROZEN')
      expect(badge.className).toMatch(/warning/)
    })

    it('maps FAILED to error variant', () => {
      render(<StatusBadge status="FAILED" />)
      const badge = screen.getByText('FAILED')
      expect(badge.className).toMatch(/destructive/)
    })

    it('maps INITIATED to info variant', () => {
      render(<StatusBadge status="INITIATED" />)
      const badge = screen.getByText('INITIATED')
      expect(badge.className).toMatch(/info/)
    })

    it('maps CLOSED to neutral variant', () => {
      render(<StatusBadge status="CLOSED" />)
      const badge = screen.getByText('CLOSED')
      expect(badge.className).toMatch(/muted/)
    })

    it('maps unknown status to neutral variant', () => {
      render(<StatusBadge status="UNKNOWN_STATUS" />)
      const badge = screen.getByText('UNKNOWN STATUS')
      expect(badge.className).toMatch(/muted/)
    })
  })

  describe('account statuses', () => {
    it.each([
      ['ACTIVE', 'success'],
      ['FROZEN', 'warning'],
      ['CLOSED', 'muted'],
      ['SUSPENDED', 'destructive'],
    ])('maps account status %s to correct color', (status, color) => {
      render(<StatusBadge status={status} />)
      const badge = screen.getByText(status.replace(/_/g, ' '))
      expect(badge.className).toMatch(new RegExp(color))
    })
  })

  describe('payment order statuses', () => {
    it.each([
      ['INITIATED', 'info'],
      ['RESERVED', 'info'],
      ['EXECUTING', 'warning'],
      ['COMPLETED', 'success'],
      ['FAILED', 'destructive'],
      ['CANCELLED', 'muted'],
      ['REVERSED', 'muted'],
    ])('maps payment status %s to correct color', (status, color) => {
      render(<StatusBadge status={status} />)
      const badge = screen.getByText(status.replace(/_/g, ' '))
      expect(badge.className).toMatch(new RegExp(color))
    })
  })

  describe('saga statuses', () => {
    it.each([
      ['DRAFT', 'muted'],
      ['DEPRECATED', 'warning'],
    ])('maps saga status %s to correct color', (status, color) => {
      render(<StatusBadge status={status} />)
      const badge = screen.getByText(status.replace(/_/g, ' '))
      expect(badge.className).toMatch(new RegExp(color))
    })
  })

  describe('tenant statuses', () => {
    it.each([
      ['PROVISIONING', 'info'],
      ['PROVISIONING_PENDING', 'info'],
      ['PROVISIONING_FAILED', 'destructive'],
      ['DEPROVISIONED', 'muted'],
    ])('maps tenant status %s to correct color', (status, color) => {
      render(<StatusBadge status={status} />)
      const badge = screen.getByText(status.replace(/_/g, ' '))
      expect(badge.className).toMatch(new RegExp(color))
    })
  })

  describe('reconciliation statuses', () => {
    it('maps RUNNING to warning variant', () => {
      render(<StatusBadge status="RUNNING" />)
      const badge = screen.getByText('RUNNING')
      expect(badge.className).toMatch(/warning/)
    })
  })

  describe('position quality ladder', () => {
    it.each([
      ['ESTIMATE', 'warning'],
      // COEFFICIENT maps to ESTIMATE quality (ADR-0017): amber treatment
      ['COEFFICIENT', 'warning'],
      ['ACTUAL', 'success'],
      ['REVISED', 'info'],
    ])('maps quality ladder %s to correct color', (status, color) => {
      render(<StatusBadge status={status} />)
      const badge = screen.getByText(status.replace(/_/g, ' '))
      expect(badge.className).toMatch(new RegExp(color))
    })
  })

  describe('loading state', () => {
    it('renders skeleton when loading is true', () => {
      const { container } = render(<StatusBadge status="ACTIVE" loading />)
      const skeleton = container.querySelector('[data-testid="status-badge-skeleton"]')
      expect(skeleton).toBeInTheDocument()
    })

    it('does not render status text when loading', () => {
      render(<StatusBadge status="ACTIVE" loading />)
      expect(screen.queryByText('ACTIVE')).not.toBeInTheDocument()
    })

    it('renders status badge when loading is false', () => {
      render(<StatusBadge status="ACTIVE" loading={false} />)
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
  })

  describe('WCAG AA color contrast', () => {
    it('success variant uses semantic token classes', () => {
      render(<StatusBadge status="ACTIVE" />)
      const badge = screen.getByText('ACTIVE')
      expect(badge.className).toMatch(/text-success-foreground/)
      expect(badge.className).toMatch(/bg-success-muted/)
    })

    it('warning variant uses semantic token classes', () => {
      render(<StatusBadge status="FROZEN" />)
      const badge = screen.getByText('FROZEN')
      expect(badge.className).toMatch(/text-warning-foreground/)
      expect(badge.className).toMatch(/bg-warning-muted/)
    })

    it('error variant uses semantic token classes', () => {
      render(<StatusBadge status="FAILED" />)
      const badge = screen.getByText('FAILED')
      expect(badge.className).toMatch(/text-destructive/)
      expect(badge.className).toMatch(/bg-destructive/)
    })

    it('info variant uses semantic token classes', () => {
      render(<StatusBadge status="INITIATED" />)
      const badge = screen.getByText('INITIATED')
      expect(badge.className).toMatch(/text-info-foreground/)
      expect(badge.className).toMatch(/bg-info-muted/)
    })

    it('neutral variant uses semantic token classes', () => {
      render(<StatusBadge status="CLOSED" />)
      const badge = screen.getByText('CLOSED')
      expect(badge.className).toMatch(/text-muted-foreground/)
      expect(badge.className).toMatch(/bg-muted/)
    })
  })
})

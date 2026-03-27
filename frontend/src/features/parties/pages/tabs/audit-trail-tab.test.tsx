import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'

// Mock the AuditTrail component to isolate tab behaviour
vi.mock('@/shared/audit-trail', () => ({
  AuditTrail: vi.fn(({ entityType, entityId }: { entityType: string; entityId: string }) => (
    <div data-testid="mock-audit-trail" data-entity-type={entityType} data-entity-id={entityId} />
  )),
}))

import { AuditTrailTab } from './audit-trail-tab'

describe('AuditTrailTab', () => {
  it('renders AuditTrail with entityType party', () => {
    const { getByTestId } = render(<AuditTrailTab partyId="party-001" />)
    const auditTrail = getByTestId('mock-audit-trail')
    expect(auditTrail).toBeInTheDocument()
    expect(auditTrail).toHaveAttribute('data-entity-type', 'party')
  })

  it('passes partyId as entityId to AuditTrail', () => {
    const { getByTestId } = render(<AuditTrailTab partyId="party-abc-123" />)
    const auditTrail = getByTestId('mock-audit-trail')
    expect(auditTrail).toHaveAttribute('data-entity-id', 'party-abc-123')
  })

  it('passes a different partyId correctly', () => {
    const { getByTestId } = render(<AuditTrailTab partyId="another-party" />)
    const auditTrail = getByTestId('mock-audit-trail')
    expect(auditTrail).toHaveAttribute('data-entity-id', 'another-party')
  })

  it('renders without crashing', () => {
    expect(() => render(<AuditTrailTab partyId="any-id" />)).not.toThrow()
  })
})

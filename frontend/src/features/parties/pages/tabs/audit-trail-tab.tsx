import * as React from 'react'
import { AuditTrail } from '@/shared/audit-trail'

interface AuditTrailTabProps {
  partyId: string
}

export function AuditTrailTab({ partyId }: AuditTrailTabProps) {
  return <AuditTrail entityType="party" entityId={partyId} />
}

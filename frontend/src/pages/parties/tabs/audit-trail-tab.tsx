import * as React from 'react'
import { AuditTrail } from '@/components/shared/audit-trail'

interface AuditTrailTabProps {
  partyId: string
}

export function AuditTrailTab({ partyId }: AuditTrailTabProps) {
  return <AuditTrail entityType="PARTY" entityId={partyId} />
}

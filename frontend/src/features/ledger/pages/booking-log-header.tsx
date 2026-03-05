import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared'
import type { FinancialBookingLog } from './types'

interface BookingLogHeaderProps {
  bookingLog: FinancialBookingLog
}

interface MetaFieldProps {
  label: string
  value: React.ReactNode
}

function MetaField({ label, value }: MetaFieldProps) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="mt-0.5 text-sm font-medium">{value}</p>
    </div>
  )
}

export function BookingLogHeader({ bookingLog }: BookingLogHeaderProps) {
  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold leading-tight">
            Booking Log
          </h2>
          <p className="mt-0.5 font-mono text-sm text-muted-foreground">{bookingLog.id}</p>
        </div>
        <StatusBadge status={bookingLog.status} />
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        <MetaField label="Account Type" value={bookingLog.financialAccountType} />
        <MetaField label="Business Unit" value={bookingLog.businessUnitReference} />
        <MetaField label="Currency" value={bookingLog.instrumentCode} />
        <MetaField
          label="Postings"
          value={
            <span data-testid="posting-count">{bookingLog.postings.length}</span>
          }
        />
        <MetaField
          label="Created"
          value={<TimeDisplay timestamp={bookingLog.createdAt} format="relative" />}
        />
      </div>
    </div>
  )
}

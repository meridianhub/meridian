export interface BillingRun {
  id: string
  billingPeriod: { start: string; end: string }
  status: 'INITIATED' | 'PROCESSING' | 'COMPLETED' | 'FAILED'
  dunningLevel: number
  invoiceCount: number
  totalAmountCents: number
  currency: string
  createdAt: string
}

export interface Invoice {
  id: string
  billingRunId: string
  partyId: string
  invoiceNumber: string
  lineItems: InvoiceLineItem[]
  subtotalCents: number
  currency: string
  status: 'DRAFT' | 'ISSUED' | 'PAID' | 'VOID' | 'OVERDUE'
  dueDate?: string
  createdAt: string
  updatedAt: string
}

export interface InvoiceLineItem {
  description: string
  quantity: string
  unitPriceCents: number
  totalCents: number
  valuationAnalysis?: Record<string, unknown>
}

export interface EmailDeliveryStatus {
  status: 'PENDING' | 'SENT' | 'DELIVERED' | 'BOUNCED' | 'DEAD_LETTER' | 'CANCELLED'
  sentAt?: string
  deliveredAt?: string
  bounceReason?: string
}

export interface InvoiceEmail {
  idempotencyKey: string
  templateName: string
  toAddresses: string[]
  status: 'PENDING' | 'SENT' | 'DELIVERED' | 'BOUNCED' | 'DEAD_LETTER' | 'CANCELLED'
  sentAt?: string
  deliveredAt?: string
  bounceReason?: string
}

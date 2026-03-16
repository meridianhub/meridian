import {
  ManifestResourceType,
} from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import {
  InstrumentType,
  NormalBalance,
  ValuationMethod,
} from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import type { ManifestNodeType } from '@/features/manifests/lib/manifest-graph-model'

export type FieldType = 'text' | 'number' | 'select' | 'multitext'

export interface SelectOption {
  value: string | number
  label: string
}

export interface FieldDefinition {
  name: string
  label: string
  type: FieldType
  required?: boolean
  placeholder?: string
  options?: SelectOption[]
  nested?: string
}

export interface ResourceSchema {
  resourceType: ManifestResourceType
  oneofCase: string
  label: string
  identifierField: string
  fields: FieldDefinition[]
}

const INSTRUMENT_TYPE_OPTIONS: SelectOption[] = [
  { value: InstrumentType.FIAT, label: 'Fiat' },
  { value: InstrumentType.COMMODITY, label: 'Commodity' },
  { value: InstrumentType.VOUCHER, label: 'Voucher' },
]

const NORMAL_BALANCE_OPTIONS: SelectOption[] = [
  { value: NormalBalance.DEBIT, label: 'Debit' },
  { value: NormalBalance.CREDIT, label: 'Credit' },
]

const VALUATION_METHOD_OPTIONS: SelectOption[] = [
  { value: ValuationMethod.SPOT_RATE, label: 'Spot Rate' },
  { value: ValuationMethod.FIXED, label: 'Fixed' },
]

export const RESOURCE_SCHEMAS: Record<string, ResourceSchema> = {
  instrument: {
    resourceType: ManifestResourceType.INSTRUMENT,
    oneofCase: 'instrument',
    label: 'Instrument',
    identifierField: 'code',
    fields: [
      { name: 'code', label: 'Code', type: 'text', required: true, placeholder: 'e.g. GBP' },
      { name: 'name', label: 'Name', type: 'text', required: true, placeholder: 'e.g. British Pound Sterling' },
      { name: 'type', label: 'Type', type: 'select', required: true, options: INSTRUMENT_TYPE_OPTIONS },
      { name: 'unit', label: 'Unit', type: 'text', placeholder: 'e.g. GBP', nested: 'dimensions' },
      { name: 'precision', label: 'Precision', type: 'number', placeholder: '2', nested: 'dimensions' },
    ],
  },
  account_type: {
    resourceType: ManifestResourceType.ACCOUNT_TYPE,
    oneofCase: 'accountType',
    label: 'Account Type',
    identifierField: 'code',
    fields: [
      { name: 'code', label: 'Code', type: 'text', required: true, placeholder: 'e.g. CURRENT' },
      { name: 'name', label: 'Name', type: 'text', required: true, placeholder: 'e.g. Current Account' },
      { name: 'normalBalance', label: 'Normal Balance', type: 'select', required: true, options: NORMAL_BALANCE_OPTIONS },
      { name: 'allowedInstruments', label: 'Allowed Instruments', type: 'multitext', placeholder: 'Comma-separated codes' },
    ],
  },
  valuation_rule: {
    resourceType: ManifestResourceType.VALUATION_RULE,
    oneofCase: 'valuationRule',
    label: 'Valuation Rule',
    identifierField: 'fromInstrument',
    fields: [
      { name: 'fromInstrument', label: 'From Instrument', type: 'text', required: true, placeholder: 'e.g. KWH' },
      { name: 'toInstrument', label: 'To Instrument', type: 'text', required: true, placeholder: 'e.g. GBP' },
      { name: 'method', label: 'Method', type: 'select', required: true, options: VALUATION_METHOD_OPTIONS },
      { name: 'source', label: 'Source', type: 'text', placeholder: 'e.g. ecb_fx_daily' },
    ],
  },
  saga: {
    resourceType: ManifestResourceType.SAGA,
    oneofCase: 'saga',
    label: 'Saga',
    identifierField: 'name',
    fields: [
      { name: 'name', label: 'Name', type: 'text', required: true, placeholder: 'e.g. process_settlement' },
      { name: 'trigger', label: 'Trigger', type: 'text', required: true, placeholder: 'e.g. event:position-keeping.transaction-captured.v1' },
      { name: 'script', label: 'Script', type: 'text', required: true, placeholder: 'Starlark script' },
    ],
  },
  party_type: {
    resourceType: ManifestResourceType.PARTY_TYPE,
    oneofCase: 'partyType',
    label: 'Party Type',
    identifierField: 'partyType',
    fields: [
      { name: 'partyType', label: 'Party Type', type: 'text', required: true, placeholder: 'e.g. CUSTOMER' },
    ],
  },
  mapping: {
    resourceType: ManifestResourceType.MAPPING,
    oneofCase: 'mapping',
    label: 'Mapping',
    identifierField: 'name',
    fields: [
      { name: 'name', label: 'Name', type: 'text', required: true, placeholder: 'e.g. stripe_payment_inbound' },
      { name: 'targetService', label: 'Target Service', type: 'text', placeholder: 'e.g. meridian.payment_order.v1.PaymentOrderService' },
      { name: 'targetRpc', label: 'Target RPC', type: 'text', placeholder: 'e.g. InitiatePaymentOrder' },
    ],
  },
  organization: {
    resourceType: ManifestResourceType.ORGANIZATION,
    oneofCase: 'organization',
    label: 'Organization',
    identifierField: 'code',
    fields: [
      { name: 'code', label: 'Code', type: 'text', required: true, placeholder: 'e.g. ACME_CORP' },
      { name: 'name', label: 'Name', type: 'text', required: true, placeholder: 'e.g. Acme Corporation' },
    ],
  },
  internal_account: {
    resourceType: ManifestResourceType.INTERNAL_ACCOUNT,
    oneofCase: 'internalAccount',
    label: 'Internal Account',
    identifierField: 'code',
    fields: [
      { name: 'code', label: 'Code', type: 'text', required: true, placeholder: 'e.g. OPERATING_GBP' },
      { name: 'accountType', label: 'Account Type', type: 'text', required: true, placeholder: 'e.g. CURRENT' },
      { name: 'instrument', label: 'Instrument', type: 'text', required: true, placeholder: 'e.g. GBP' },
      { name: 'description', label: 'Description', type: 'text', placeholder: 'Optional description' },
    ],
  },
}

export function getResourceSchema(nodeType: ManifestNodeType): ResourceSchema | undefined {
  return RESOURCE_SCHEMAS[nodeType]
}

export function buildResourcePayload(
  schema: ResourceSchema,
  formValues: Record<string, string>,
): Record<string, unknown> {
  const result: Record<string, unknown> = {}

  for (const field of schema.fields) {
    const rawValue = formValues[field.name]
    if (rawValue === undefined || rawValue === '') continue

    let value: unknown = rawValue
    if (field.type === 'number') {
      value = parseInt(rawValue, 10)
    } else if (field.type === 'select') {
      value = parseInt(rawValue, 10)
    } else if (field.type === 'multitext') {
      value = rawValue.split(',').map((s) => s.trim()).filter(Boolean)
    }

    if (field.nested) {
      const nested = (result[field.nested] ?? {}) as Record<string, unknown>
      nested[field.name] = value
      result[field.nested] = nested
    } else {
      result[field.name] = value
    }
  }

  return result
}

export function extractFormValues(
  schema: ResourceSchema,
  data: Record<string, unknown>,
): Record<string, string> {
  const values: Record<string, string> = {}

  for (const field of schema.fields) {
    let rawValue: unknown
    if (field.nested) {
      const nested = data[field.nested] as Record<string, unknown> | undefined
      rawValue = nested?.[field.name]
    } else {
      rawValue = data[field.name]
    }

    if (rawValue === undefined || rawValue === null) {
      values[field.name] = ''
    } else if (Array.isArray(rawValue)) {
      values[field.name] = rawValue.join(', ')
    } else {
      values[field.name] = String(rawValue)
    }
  }

  return values
}

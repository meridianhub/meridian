import { describe, it, expect } from 'vitest'
import { ManifestResourceType } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import { InstrumentType, NormalBalance } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import {
  RESOURCE_SCHEMAS,
  getResourceSchema,
  buildResourcePayload,
  extractFormValues,
} from './resource-schema-registry'

describe('resource-schema-registry', () => {
  describe('RESOURCE_SCHEMAS', () => {
    it('has a schema for each supported resource type', () => {
      expect(RESOURCE_SCHEMAS.instrument).toBeDefined()
      expect(RESOURCE_SCHEMAS.account_type).toBeDefined()
      expect(RESOURCE_SCHEMAS.valuation_rule).toBeDefined()
      expect(RESOURCE_SCHEMAS.saga).toBeDefined()
      expect(RESOURCE_SCHEMAS.party_type).toBeDefined()
      expect(RESOURCE_SCHEMAS.mapping).toBeDefined()
      expect(RESOURCE_SCHEMAS.organization).toBeDefined()
      expect(RESOURCE_SCHEMAS.internal_account).toBeDefined()
    })

    it('maps instrument schema to correct resource type and oneof case', () => {
      const schema = RESOURCE_SCHEMAS.instrument
      expect(schema.resourceType).toBe(ManifestResourceType.INSTRUMENT)
      expect(schema.oneofCase).toBe('instrument')
      expect(schema.identifierField).toBe('code')
    })

    it('maps account_type schema to correct resource type and oneof case', () => {
      const schema = RESOURCE_SCHEMAS.account_type
      expect(schema.resourceType).toBe(ManifestResourceType.ACCOUNT_TYPE)
      expect(schema.oneofCase).toBe('accountType')
    })

    it('has required fields marked correctly', () => {
      const schema = RESOURCE_SCHEMAS.instrument
      const codeField = schema.fields.find((f) => f.name === 'code')
      expect(codeField?.required).toBe(true)
      const unitField = schema.fields.find((f) => f.name === 'unit')
      expect(unitField?.required).toBeFalsy()
    })
  })

  describe('getResourceSchema', () => {
    it('returns schema for known types', () => {
      expect(getResourceSchema('instrument')).toBe(RESOURCE_SCHEMAS.instrument)
      expect(getResourceSchema('account_type')).toBe(RESOURCE_SCHEMAS.account_type)
    })

    it('returns undefined for unsupported types', () => {
      expect(getResourceSchema('operational_gateway')).toBeUndefined()
      expect(getResourceSchema('provider_connection')).toBeUndefined()
    })
  })

  describe('buildResourcePayload', () => {
    it('builds a flat resource payload', () => {
      const schema = RESOURCE_SCHEMAS.valuation_rule
      const values = {
        fromInstrument: 'KWH',
        toInstrument: 'GBP',
        method: '1',
        source: 'ecb_fx_daily',
      }
      const payload = buildResourcePayload(schema, values)
      expect(payload).toEqual({
        fromInstrument: 'KWH',
        toInstrument: 'GBP',
        method: 1,
        source: 'ecb_fx_daily',
      })
    })

    it('nests fields with nested property', () => {
      const schema = RESOURCE_SCHEMAS.instrument
      const values = {
        code: 'GBP',
        name: 'British Pound',
        type: String(InstrumentType.FIAT),
        unit: 'GBP',
        precision: '2',
      }
      const payload = buildResourcePayload(schema, values)
      expect(payload.code).toBe('GBP')
      expect(payload.dimensions).toEqual({ unit: 'GBP', precision: 2 })
    })

    it('skips empty values', () => {
      const schema = RESOURCE_SCHEMAS.instrument
      const values = {
        code: 'GBP',
        name: 'British Pound',
        type: String(InstrumentType.FIAT),
        unit: '',
        precision: '',
      }
      const payload = buildResourcePayload(schema, values)
      expect(payload.dimensions).toBeUndefined()
    })

    it('splits multitext into array', () => {
      const schema = RESOURCE_SCHEMAS.account_type
      const values = {
        code: 'CURRENT',
        name: 'Current Account',
        normalBalance: String(NormalBalance.DEBIT),
        allowedInstruments: 'GBP, USD, EUR',
      }
      const payload = buildResourcePayload(schema, values)
      expect(payload.allowedInstruments).toEqual(['GBP', 'USD', 'EUR'])
    })
  })

  describe('extractFormValues', () => {
    it('extracts flat values to strings', () => {
      const schema = RESOURCE_SCHEMAS.valuation_rule
      const data = {
        fromInstrument: 'KWH',
        toInstrument: 'GBP',
        method: 1,
        source: 'ecb_fx_daily',
      }
      const values = extractFormValues(schema, data)
      expect(values.fromInstrument).toBe('KWH')
      expect(values.method).toBe('1')
    })

    it('extracts nested values', () => {
      const schema = RESOURCE_SCHEMAS.instrument
      const data = {
        code: 'GBP',
        name: 'British Pound',
        type: InstrumentType.FIAT,
        dimensions: { unit: 'GBP', precision: 2 },
      }
      const values = extractFormValues(schema, data)
      expect(values.unit).toBe('GBP')
      expect(values.precision).toBe('2')
    })

    it('converts arrays to comma-separated strings', () => {
      const schema = RESOURCE_SCHEMAS.account_type
      const data = {
        code: 'CURRENT',
        name: 'Current Account',
        normalBalance: NormalBalance.DEBIT,
        allowedInstruments: ['GBP', 'USD'],
      }
      const values = extractFormValues(schema, data)
      expect(values.allowedInstruments).toBe('GBP, USD')
    })

    it('returns empty strings for missing values', () => {
      const schema = RESOURCE_SCHEMAS.instrument
      const data = { code: 'GBP' }
      const values = extractFormValues(schema, data)
      expect(values.name).toBe('')
      expect(values.unit).toBe('')
    })
  })
})

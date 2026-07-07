import { describe, it, expect } from 'vitest'
import {
  getInstrumentPrecision,
  DEFAULT_DECIMAL_PLACES,
  NON_FIAT_UNITS,
} from './instrument-precision'

describe('getInstrumentPrecision', () => {
  it('returns non-fiat instrument precision', () => {
    expect(getInstrumentPrecision('kWh')).toBe(3)
    expect(getInstrumentPrecision('GPU_HOUR')).toBe(6)
    expect(getInstrumentPrecision('TONNE_CO2E')).toBe(4)
  })

  it('returns fiat currency overrides', () => {
    expect(getInstrumentPrecision('JPY')).toBe(0)
    expect(getInstrumentPrecision('KRW')).toBe(0)
    expect(getInstrumentPrecision('KWD')).toBe(3)
    expect(getInstrumentPrecision('BHD')).toBe(3)
  })

  it('defaults to 2 for standard currencies', () => {
    expect(getInstrumentPrecision('GBP')).toBe(DEFAULT_DECIMAL_PLACES)
    expect(getInstrumentPrecision('USD')).toBe(2)
    expect(getInstrumentPrecision('EUR')).toBe(2)
  })

  it('defaults to 2 for unknown instrument codes', () => {
    expect(getInstrumentPrecision('UNKNOWN')).toBe(2)
    expect(getInstrumentPrecision('')).toBe(2)
  })

  it('keeps NON_FIAT_UNITS precision aligned with getInstrumentPrecision', () => {
    for (const [code, config] of Object.entries(NON_FIAT_UNITS)) {
      expect(getInstrumentPrecision(code)).toBe(config.precision)
    }
  })
})

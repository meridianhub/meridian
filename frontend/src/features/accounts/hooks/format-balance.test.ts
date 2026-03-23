import { describe, it, expect } from 'vitest'
import { formatBalance } from './use-accounts'

describe('formatBalance', () => {
  it('returns undefined for null/undefined input', () => {
    expect(formatBalance(null)).toBeUndefined()
    expect(formatBalance(undefined)).toBeUndefined()
  })

  it('formats ISO currency codes using Intl.NumberFormat', () => {
    const result = formatBalance({ units: 100, nanos: 500_000_000, currencyCode: 'GBP' })
    expect(result).toBeDefined()
    // Intl formatting includes the value; exact format is locale-dependent
    expect(result).toContain('100.50')
  })

  it('formats non-ISO currency codes with code and value', () => {
    const result = formatBalance({ units: 245, nanos: 500_000_000, currencyCode: 'KWH' })
    expect(result).toBeDefined()
    // Intl may format as "KWH 245.50" or catch block returns "245.50 KWH"
    expect(result).toContain('245.50')
    expect(result).toContain('KWH')
  })

  it('formats non-ISO currency codes for zero values', () => {
    const result = formatBalance({ units: 0, nanos: 0, currencyCode: 'KWH' })
    expect(result).toBeDefined()
    expect(result).toContain('0.00')
    expect(result).toContain('KWH')
  })

  it('formats values without currency code as plain decimal', () => {
    const result = formatBalance({ units: 42, nanos: 0 })
    expect(result).toBe('42.00')
  })

  it('handles bigint units', () => {
    const result = formatBalance({ units: BigInt(100), nanos: 0, currencyCode: 'KWH' })
    expect(result).toBeDefined()
    expect(result).toContain('100.00')
    expect(result).toContain('KWH')
  })

  it('handles very large bigint units by falling back to string', () => {
    const bigValue = BigInt(Number.MAX_SAFE_INTEGER) + BigInt(1)
    const result = formatBalance({
      units: bigValue,
      nanos: 0,
      currencyCode: 'GBP',
    })
    expect(result).toContain('GBP')
    expect(result).toContain(bigValue.toString())
  })
})

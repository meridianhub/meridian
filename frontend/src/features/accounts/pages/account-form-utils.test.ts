import { describe, it, expect } from 'vitest'
import { amountToBigInt, bigIntToDisplayAmount, formatCurrencyAmount } from './account-form-utils'

describe('amountToBigInt', () => {
  it('converts a string decimal to BigInt minor units (2 dp default)', () => {
    expect(amountToBigInt('100.00')).toBe(BigInt(10000))
  })

  it('converts integer string to BigInt minor units', () => {
    expect(amountToBigInt('100')).toBe(BigInt(10000))
  })

  it('converts fractional amounts correctly', () => {
    expect(amountToBigInt('1.50')).toBe(BigInt(150))
  })

  it('handles single decimal place', () => {
    expect(amountToBigInt('1.5')).toBe(BigInt(150))
  })

  it('handles zero', () => {
    expect(amountToBigInt('0')).toBe(BigInt(0))
  })

  it('handles large amounts', () => {
    expect(amountToBigInt('1000000.00')).toBe(BigInt(100000000))
  })

  it('throws on negative amount', () => {
    expect(() => amountToBigInt('-1.00')).toThrow('Amount must be positive')
  })

  it('throws on invalid string', () => {
    expect(() => amountToBigInt('abc')).toThrow()
  })

  it('throws on scientific notation (prevents float parsing exploit)', () => {
    expect(() => amountToBigInt('1e5')).toThrow()
  })

  it('handles rounding at the third decimal place', () => {
    // 1.555 with 2dp should round up to 1.56 = BigInt(156)
    expect(amountToBigInt('1.555')).toBe(BigInt(156))
  })

  it('truncates without rounding when next digit < 5', () => {
    // 1.554 with 2dp should stay 1.55 = BigInt(155)
    expect(amountToBigInt('1.554')).toBe(BigInt(155))
  })
})

describe('bigIntToDisplayAmount', () => {
  it('converts BigInt minor units to decimal string (2 dp default)', () => {
    expect(bigIntToDisplayAmount(BigInt(10000))).toBe('100.00')
  })

  it('converts smaller amounts', () => {
    expect(bigIntToDisplayAmount(BigInt(150))).toBe('1.50')
  })

  it('converts zero', () => {
    expect(bigIntToDisplayAmount(BigInt(0))).toBe('0.00')
  })

  it('converts large amounts', () => {
    expect(bigIntToDisplayAmount(BigInt(100000000))).toBe('1000000.00')
  })
})

describe('formatCurrencyAmount', () => {
  it('formats amount with currency prefix', () => {
    expect(formatCurrencyAmount('100.00', 'GBP')).toBe('GBP 100.00')
  })

  it('formats with different currency', () => {
    expect(formatCurrencyAmount('50.00', 'USD')).toBe('USD 50.00')
  })

  it('formats zero', () => {
    expect(formatCurrencyAmount('0.00', 'GBP')).toBe('GBP 0.00')
  })
})

import { describe, it, expect } from 'vitest'
import { amountToBigInt } from './payment-form-utils'

describe('amountToBigInt', () => {
  it('converts whole number amounts', () => {
    expect(amountToBigInt('100')).toBe(10000n)
    expect(amountToBigInt('1')).toBe(100n)
    expect(amountToBigInt('0')).toBe(0n)
  })

  it('converts decimal amounts without float precision loss', () => {
    expect(amountToBigInt('100.50')).toBe(10050n)
    expect(amountToBigInt('0.29')).toBe(29n) // 0.29 * 100 = 28.999... in float
    expect(amountToBigInt('0.01')).toBe(1n)
    expect(amountToBigInt('99.99')).toBe(9999n)
  })

  it('truncates extra decimal places (no rounding for < 5)', () => {
    expect(amountToBigInt('1.234')).toBe(123n)
  })

  it('rounds half-up when third decimal >= 5', () => {
    expect(amountToBigInt('1.235')).toBe(124n)
    expect(amountToBigInt('1.999')).toBe(200n)
  })

  it('handles amounts with only decimal part', () => {
    expect(amountToBigInt('.50')).toBe(50n)
    expect(amountToBigInt('.05')).toBe(5n)
  })

  it('throws for negative amounts', () => {
    expect(() => amountToBigInt('-1.00')).toThrow('Amount must be positive')
  })

  it('throws for empty string', () => {
    expect(() => amountToBigInt('')).toThrow('Invalid amount')
    expect(() => amountToBigInt('   ')).toThrow('Invalid amount')
  })

  it('throws for non-numeric input', () => {
    expect(() => amountToBigInt('abc')).toThrow('Invalid amount')
    expect(() => amountToBigInt('1e5')).toThrow('Invalid amount')
  })

  it('supports custom decimal places', () => {
    expect(amountToBigInt('1.500', 3)).toBe(1500n)
    expect(amountToBigInt('100', 0)).toBe(100n)
  })
})

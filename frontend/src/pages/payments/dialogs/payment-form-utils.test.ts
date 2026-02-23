import { describe, it, expect } from 'vitest'
import { amountToBigInt } from './payment-form-utils'

describe('payment amountToBigInt (IEEE-754 precision regression)', () => {
  it('converts 0.29 correctly (was producing 28 via parseFloat)', () => {
    // The original bug: Math.floor(parseFloat("0.29") * 100) = Math.floor(28.999...) = 28
    expect(amountToBigInt('0.29')).toBe(29n)
  })

  it('converts 0.1 + 0.2 edge case correctly', () => {
    expect(amountToBigInt('0.30')).toBe(30n)
  })

  it('converts whole amounts', () => {
    expect(amountToBigInt('100')).toBe(10000n)
  })

  it('converts standard decimal amounts', () => {
    expect(amountToBigInt('100.50')).toBe(10050n)
  })

  it('rejects negative amounts', () => {
    expect(() => amountToBigInt('-1.00')).toThrow('Amount must be positive')
  })

  it('rejects scientific notation', () => {
    expect(() => amountToBigInt('1e5')).toThrow()
  })

  it('handles rounding at third decimal place', () => {
    expect(amountToBigInt('1.555')).toBe(156n)
  })

  it('truncates without rounding when next digit < 5', () => {
    expect(amountToBigInt('1.554')).toBe(155n)
  })

  it('handles large amounts', () => {
    expect(amountToBigInt('999999.99')).toBe(99999999n)
  })
})

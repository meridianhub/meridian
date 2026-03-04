/**
 * Shared form utilities for account action dialogs.
 * Handles BigInt conversion (API uses minor units as strings/BigInt)
 * and currency amount formatting for display.
 */

/**
 * Converts a decimal amount string (e.g. "100.50") to BigInt minor units
 * using the given number of decimal places (default: 2).
 *
 * Uses string-based parsing to avoid IEEE-754 float precision issues.
 * Throws if the amount is negative, not a valid decimal, or uses scientific notation.
 */
export function amountToBigInt(amount: string, decimalPlaces = 2): bigint {
  const trimmed = amount.trim()
  if (!trimmed) {
    throw new Error(`Invalid amount: ${amount}`)
  }
  if (trimmed.startsWith('-')) {
    throw new Error('Amount must be positive')
  }
  const match = trimmed.match(/^(\d*)(?:\.(\d*))?$/)
  if (!match || (!match[1] && !match[2])) {
    throw new Error(`Invalid amount: ${amount}`)
  }
  const whole = match[1] || '0'
  const fractionRaw = match[2] ?? ''
  const fraction = fractionRaw.slice(0, decimalPlaces).padEnd(decimalPlaces, '0')
  let value = BigInt(`${whole}${fraction}`)
  // Round half-up: check the next digit beyond decimalPlaces
  const nextDigit = fractionRaw[decimalPlaces]
  if (nextDigit && nextDigit >= '5') {
    value += 1n
  }
  return value
}

/**
 * Converts a BigInt in minor units back to a display string with the given
 * number of decimal places (default: 2).
 *
 * E.g. BigInt(10050) → "100.50"
 */
export function bigIntToDisplayAmount(amount: bigint, decimalPlaces = 2): string {
  const multiplier = BigInt(Math.pow(10, decimalPlaces))
  const whole = amount / multiplier
  const fraction = amount % multiplier
  return `${whole.toString()}.${fraction.toString().padStart(decimalPlaces, '0')}`
}

/**
 * Formats an amount string with a currency code prefix for display.
 *
 * E.g. formatCurrencyAmount("100.00", "GBP") → "GBP 100.00"
 */
export function formatCurrencyAmount(amount: string, currency: string): string {
  return `${currency} ${amount}`
}

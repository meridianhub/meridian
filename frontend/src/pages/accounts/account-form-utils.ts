/**
 * Shared form utilities for account action dialogs.
 * Handles BigInt conversion (API uses minor units as strings/BigInt)
 * and currency amount formatting for display.
 */

/**
 * Converts a decimal amount string (e.g. "100.50") to BigInt minor units
 * using the given number of decimal places (default: 2).
 *
 * Throws if the amount is negative or not a valid number.
 */
export function amountToBigInt(amount: string, decimalPlaces = 2): bigint {
  const num = parseFloat(amount)
  if (isNaN(num)) {
    throw new Error(`Invalid amount: ${amount}`)
  }
  if (num < 0) {
    throw new Error('Amount must be positive')
  }
  // Multiply by 10^decimalPlaces to get minor units, round to handle float precision
  const multiplier = Math.pow(10, decimalPlaces)
  return BigInt(Math.round(num * multiplier))
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

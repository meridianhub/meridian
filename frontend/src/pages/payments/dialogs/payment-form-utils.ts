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

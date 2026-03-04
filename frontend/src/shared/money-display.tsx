import { useMemo } from 'react'

// Currencies with non-standard decimal places (ISO 4217)
const CURRENCY_PRECISION: Record<string, number> = {
  JPY: 0,
  KRW: 0,
  VND: 0,
  BHD: 3,
  KWD: 3,
  OMR: 3,
}

// Non-fiat instrument types with display configuration
const NON_FIAT_UNITS: Record<string, { precision: number; suffix: string }> = {
  kWh: { precision: 3, suffix: ' kWh' },
  GPU_HOUR: { precision: 6, suffix: ' GPU-hrs' },
  TONNE_CO2E: { precision: 4, suffix: ' tCO2e' },
}

export interface FormatMoneyOptions {
  precision?: number
  showSign?: boolean
}

/**
 * Format a BigInt amount (in smallest currency unit) to a display string.
 * Uses BigInt arithmetic throughout to avoid precision loss for amounts > 2^53.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function formatMoney(
  amount: bigint | string | null | undefined,
  currency: string,
  options: FormatMoneyOptions = {},
): string {
  if (amount === null || amount === undefined) return '—'

  const bigAmount = typeof amount === 'string' ? BigInt(amount) : amount
  const isNegative = bigAmount < 0n
  const absAmount = isNegative ? -bigAmount : bigAmount

  const prec =
    options.precision ??
    NON_FIAT_UNITS[currency]?.precision ??
    CURRENCY_PRECISION[currency] ??
    2
  const divisor = BigInt(10 ** prec)

  const intPart = absAmount / divisor
  const fracPart = absAmount % divisor

  const sign = isNegative ? '-' : options.showSign && bigAmount > 0n ? '+' : ''

  if (currency in NON_FIAT_UNITS) {
    const { suffix } = NON_FIAT_UNITS[currency]
    return sign + formatDecimal(intPart, fracPart, prec) + suffix
  }

  // Fiat currency: extract symbol via formatToParts to avoid parseFloat precision loss.
  // Using formatToParts(0) gives us the currency symbol and its position without
  // passing the actual (potentially > 2^53) amount through Number conversion.
  const formatter = new Intl.NumberFormat(undefined, {
    style: 'currency',
    currency,
    minimumFractionDigits: prec,
    maximumFractionDigits: prec,
  })

  const parts = formatter.formatToParts(0)
  const currencySymbol = parts.find((p) => p.type === 'currency')?.value ?? ''
  const currencyFirst =
    parts[0]?.type === 'currency' ||
    (parts[0]?.type === 'literal' && parts[1]?.type === 'currency')

  const numericFormatted = formatDecimal(intPart, fracPart, prec)

  if (currencyFirst) {
    return sign + currencySymbol + numericFormatted
  } else {
    return sign + numericFormatted + '\u00a0' + currencySymbol
  }
}

/**
 * Format integer and fractional BigInt parts as a decimal string with thousand separators.
 * Uses Intl.NumberFormat for the integer part to get locale-correct separators.
 */
function formatDecimal(intPart: bigint, fracPart: bigint, precision: number): string {
  const intFormatter = new Intl.NumberFormat(undefined)
  const intStr = intFormatter.format(intPart)

  if (precision === 0) return intStr

  const fracStr = fracPart.toString().padStart(precision, '0')
  return `${intStr}.${fracStr}`
}

export interface MoneyDisplayProps {
  amount: bigint | string | null | undefined
  currency: string
  precision?: number
  showSign?: boolean
}

export function MoneyDisplay({ amount, currency, precision, showSign }: MoneyDisplayProps) {
  const formatted = useMemo(
    () => formatMoney(amount, currency, { precision, showSign }),
    [amount, currency, precision, showSign],
  )

  return <span className="tabular-nums">{formatted}</span>
}

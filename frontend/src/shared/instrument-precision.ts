/**
 * Single source of truth for instrument display/entry precision.
 *
 * Amounts are stored and transmitted as integer minor units. The number of
 * decimal places an instrument uses determines how a decimal input string is
 * scaled to minor units (and back for display). Currencies default to 2 decimal
 * places; several fiat currencies and all non-fiat instruments differ.
 *
 * This module is consumed by both money-display (formatting) and the account
 * action dialogs (deposit / withdraw / lien amount entry) so entry precision
 * always matches display precision.
 */

/** Default decimal places for standard currencies without an explicit override. */
export const DEFAULT_DECIMAL_PLACES = 2

/** Currencies with non-standard decimal places (ISO 4217). */
const CURRENCY_PRECISION: Record<string, number> = {
  JPY: 0,
  KRW: 0,
  VND: 0,
  BHD: 3,
  KWD: 3,
  OMR: 3,
}

/** Non-fiat instrument types with display configuration (precision + unit suffix). */
export const NON_FIAT_UNITS: Record<string, { precision: number; suffix: string }> = {
  kWh: { precision: 3, suffix: ' kWh' },
  GPU_HOUR: { precision: 6, suffix: ' GPU-hrs' },
  TONNE_CO2E: { precision: 4, suffix: ' tCO2e' },
}

/**
 * Returns the number of decimal places used by the given instrument code.
 *
 * Resolution order: non-fiat instrument → fiat currency override → default (2).
 *
 * @example getInstrumentPrecision('kWh')      // 3
 * @example getInstrumentPrecision('GPU_HOUR') // 6
 * @example getInstrumentPrecision('JPY')      // 0
 * @example getInstrumentPrecision('GBP')      // 2 (default)
 */
export function getInstrumentPrecision(instrumentCode: string): number {
  return (
    NON_FIAT_UNITS[instrumentCode]?.precision ??
    CURRENCY_PRECISION[instrumentCode] ??
    DEFAULT_DECIMAL_PLACES
  )
}

/**
 * Shared form utilities for payment action dialogs.
 * Re-exports the string-based BigInt conversion from account-form-utils
 * to avoid IEEE-754 float precision issues in payment amount handling.
 */
export { amountToBigInt, bigIntToDisplayAmount, formatCurrencyAmount } from '../../accounts/account-form-utils'

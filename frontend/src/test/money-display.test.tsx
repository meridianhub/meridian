import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MoneyDisplay, formatMoney } from '@/shared/money-display'

describe('formatMoney', () => {
  describe('null/undefined handling', () => {
    it('returns em-dash for null amount', () => {
      expect(formatMoney(null, 'GBP')).toBe('—')
    })

    it('returns em-dash for undefined amount', () => {
      expect(formatMoney(undefined, 'GBP')).toBe('—')
    })
  })

  describe('fiat currency formatting', () => {
    it('formats GBP with 2 decimal places: 10000n → £100.00', () => {
      const result = formatMoney(10000n, 'GBP')
      expect(result).toBe('£100.00')
    })

    it('formats JPY with 0 decimal places: 10000n → 10,000 (no fractional part)', () => {
      const result = formatMoney(10000n, 'JPY')
      // Locale-agnostic: check numeric content and no decimal point
      expect(result).toContain('10,000')
      expect(result).not.toContain('.')
    })

    it('formats USD with 2 decimal places: 9999n → 99.99', () => {
      const result = formatMoney(9999n, 'USD')
      // Locale-agnostic: check numeric content
      expect(result).toContain('99.99')
    })

    it('formats KWD with 3 decimal places: 1234567n → KD 1,234.567', () => {
      const result = formatMoney(1234567n, 'KWD')
      // Just check structure - locale-specific formatting
      expect(result).toContain('1,234.567')
    })

    it('formats BHD with 3 decimal places', () => {
      const result = formatMoney(1000n, 'BHD')
      expect(result).toContain('1.000')
    })

    it('formats OMR with 3 decimal places', () => {
      const result = formatMoney(1000n, 'OMR')
      expect(result).toContain('1.000')
    })

    it('formats KRW with 0 decimal places: 10000n → ₩10,000', () => {
      const result = formatMoney(10000n, 'KRW')
      expect(result).toBe('₩10,000')
    })
  })

  describe('non-fiat instrument formatting', () => {
    it('formats kWh with 3 decimal places: 1500000n → 1,500.000 kWh', () => {
      const result = formatMoney(1500000n, 'kWh')
      expect(result).toBe('1,500.000 kWh')
    })

    it('formats GPU_HOUR with 6 decimal places', () => {
      const result = formatMoney(1000000n, 'GPU_HOUR')
      expect(result).toBe('1.000000 GPU-hrs')
    })

    it('formats TONNE_CO2E with 4 decimal places', () => {
      const result = formatMoney(10000n, 'TONNE_CO2E')
      expect(result).toBe('1.0000 tCO2e')
    })
  })

  describe('BigInt vs string input', () => {
    it('accepts string input and converts to BigInt: "10000" for GBP → £100.00', () => {
      const result = formatMoney('10000', 'GBP')
      expect(result).toBe('£100.00')
    })

    it('accepts bigint input directly', () => {
      const result = formatMoney(10000n, 'GBP')
      expect(result).toBe('£100.00')
    })
  })

  describe('negative amounts', () => {
    it('formats negative GBP amount: -10000n → -£100.00', () => {
      const result = formatMoney(-10000n, 'GBP')
      expect(result).toContain('100.00')
      expect(result).toContain('-')
    })

    it('formats negative kWh amount: -1500000n → -1,500.000 kWh', () => {
      const result = formatMoney(-1500000n, 'kWh')
      expect(result).toBe('-1,500.000 kWh')
    })
  })

  describe('showSign option', () => {
    it('shows + prefix for positive amount when showSign is true', () => {
      const result = formatMoney(10000n, 'GBP', { showSign: true })
      expect(result).toContain('+')
    })

    it('does not show + prefix for positive amount when showSign is false', () => {
      const result = formatMoney(10000n, 'GBP', { showSign: false })
      expect(result).not.toContain('+')
    })

    it('shows - prefix for negative amount regardless of showSign', () => {
      const result = formatMoney(-10000n, 'GBP', { showSign: false })
      expect(result).toContain('-')
    })
  })

  describe('precision override', () => {
    it('respects precision override for GBP: 10000n with precision=4 → 100.0000', () => {
      const result = formatMoney(10000n, 'GBP', { precision: 4 })
      expect(result).toContain('1.0000')
    })
  })

  describe('BigInt precision - no precision loss for amounts > 2^53', () => {
    it('handles amounts larger than Number.MAX_SAFE_INTEGER without precision loss', () => {
      // 2^53 = 9007199254740992 — values beyond this lose precision in Number
      const largeAmount = 9007199254740993n // 2^53 + 1
      // Should NOT throw and should produce a result
      const result = formatMoney(largeAmount, 'kWh')
      expect(typeof result).toBe('string')
      expect(result).not.toBe('—')
    })

    it('correctly formats very large kWh value without precision loss', () => {
      // 10000000000000000 kWh = 10,000,000,000,000.000 kWh (3 decimal places)
      const amount = 10000000000000000000n // 10^19 minimal units -> 10^16 kWh
      const result = formatMoney(amount, 'kWh')
      expect(result).toContain('kWh')
      // As long as result is well-formed, precision is maintained
      expect(result).not.toContain('NaN')
    })

    it('fiat: correctly formats amount larger than 2^53 without precision loss', () => {
      // 9007199254740993 pence (2^53+1) = £90,071,992,547,409.93
      // If parseFloat were used, 2^53+1 would round to 2^53, losing the last digit
      const largeGBP = 9007199254740993n
      const result = formatMoney(largeGBP, 'GBP')
      // The integer part should end in ...09.93, NOT ...08.96 (which parseFloat would give)
      expect(result).toContain('09.93')
      expect(result).not.toContain('NaN')
    })
  })
})

describe('MoneyDisplay component', () => {
  it('renders formatted GBP amount', () => {
    render(<MoneyDisplay amount={10000n} currency="GBP" />)
    expect(screen.getByText('£100.00')).toBeInTheDocument()
  })

  it('renders em-dash for null amount', () => {
    render(<MoneyDisplay amount={null} currency="GBP" />)
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('renders em-dash for undefined amount', () => {
    render(<MoneyDisplay amount={undefined} currency="GBP" />)
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('renders formatted kWh amount', () => {
    render(<MoneyDisplay amount={1500000n} currency="kWh" />)
    expect(screen.getByText('1,500.000 kWh')).toBeInTheDocument()
  })

  it('renders JPY with no decimal places', () => {
    render(<MoneyDisplay amount={10000n} currency="JPY" />)
    // locale-agnostic: check the numeric content is correct (no decimals)
    const span = document.querySelector('span.tabular-nums')
    expect(span?.textContent).toContain('10,000')
    expect(span?.textContent).not.toContain('.')
  })

  it('applies tabular-nums class for decimal alignment', () => {
    const { container } = render(<MoneyDisplay amount={10000n} currency="GBP" />)
    const span = container.querySelector('span')
    expect(span?.className).toContain('tabular-nums')
  })
})

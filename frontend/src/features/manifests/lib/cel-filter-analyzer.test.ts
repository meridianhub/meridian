import { describe, expect, it } from 'vitest';

import { analyzeFilter, type EventContext } from './cel-filter-analyzer';

describe('analyzeFilter', () => {
  it('returns pass for empty filter', () => {
    const result = analyzeFilter('', { instrumentCode: 'GBP' });
    expect(result.result).toBe('pass');
    expect(result.reason).toBe('No filter applied');
  });

  it('returns pass for whitespace-only filter', () => {
    const result = analyzeFilter('   ', { instrumentCode: 'GBP' });
    expect(result.result).toBe('pass');
  });

  describe('simple equality', () => {
    it('returns pass when field matches context', () => {
      const context: EventContext = { instrumentCode: 'GBP' };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP'",
        context,
      );
      expect(result.result).toBe('pass');
    });

    it('returns fail when field does not match context', () => {
      const context: EventContext = { instrumentCode: 'USD' };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP'",
        context,
      );
      expect(result.result).toBe('fail');
      expect(result.reason).toContain('USD');
    });

    it('handles direction field', () => {
      const context: EventContext = { direction: 'DEBIT' };
      const result = analyzeFilter("event.direction == 'DEBIT'", context);
      expect(result.result).toBe('pass');
    });

    it('handles account_type field', () => {
      const context: EventContext = { accountType: 'SETTLEMENT' };
      const result = analyzeFilter(
        "event.account_type == 'SETTLEMENT'",
        context,
      );
      expect(result.result).toBe('pass');
    });
  });

  describe('simple inequality', () => {
    it('returns pass when field does not equal value', () => {
      const context: EventContext = { instrumentCode: 'USD' };
      const result = analyzeFilter(
        "event.instrument_code != 'GBP'",
        context,
      );
      expect(result.result).toBe('pass');
    });

    it('returns fail when field equals value', () => {
      const context: EventContext = { instrumentCode: 'GBP' };
      const result = analyzeFilter(
        "event.instrument_code != 'GBP'",
        context,
      );
      expect(result.result).toBe('fail');
    });
  });

  describe('has() checks', () => {
    it('returns indeterminate for has() expressions', () => {
      const result = analyzeFilter('has(event.metadata)', {});
      expect(result.result).toBe('indeterminate');
      expect(result.reason).toContain('has()');
    });
  });

  describe('chain_depth references', () => {
    it('returns indeterminate for chain_depth expressions', () => {
      const result = analyzeFilter('chain_depth > 0', {});
      expect(result.result).toBe('indeterminate');
      expect(result.reason).toContain('chain_depth');
    });
  });

  describe('unknown fields', () => {
    it('returns indeterminate for unknown event fields', () => {
      const result = analyzeFilter(
        "event.unknown_field == 'value'",
        {},
      );
      expect(result.result).toBe('indeterminate');
      expect(result.reason).toContain('Unknown field');
    });

    it('returns indeterminate when context value is missing', () => {
      const result = analyzeFilter(
        "event.instrument_code == 'GBP'",
        {},
      );
      expect(result.result).toBe('indeterminate');
      expect(result.reason).toContain('missing');
    });
  });

  describe('compound && expressions', () => {
    it('returns pass when all parts pass', () => {
      const context: EventContext = {
        instrumentCode: 'GBP',
        direction: 'CREDIT',
      };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP' && event.direction == 'CREDIT'",
        context,
      );
      expect(result.result).toBe('pass');
    });

    it('returns fail when any part fails', () => {
      const context: EventContext = {
        instrumentCode: 'GBP',
        direction: 'DEBIT',
      };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP' && event.direction == 'CREDIT'",
        context,
      );
      expect(result.result).toBe('fail');
    });

    it('returns indeterminate when some parts are indeterminate and none fail', () => {
      const context: EventContext = { instrumentCode: 'GBP' };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP' && has(event.metadata)",
        context,
      );
      expect(result.result).toBe('indeterminate');
    });
  });

  describe('compound || expressions', () => {
    it('returns pass when any part passes', () => {
      const context: EventContext = {
        instrumentCode: 'USD',
        direction: 'CREDIT',
      };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP' || event.direction == 'CREDIT'",
        context,
      );
      expect(result.result).toBe('pass');
    });

    it('returns fail when all parts fail', () => {
      const context: EventContext = {
        instrumentCode: 'USD',
        direction: 'DEBIT',
      };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP' || event.direction == 'CREDIT'",
        context,
      );
      expect(result.result).toBe('fail');
    });

    it('returns indeterminate when some parts are indeterminate and none pass', () => {
      const context: EventContext = { instrumentCode: 'USD' };
      const result = analyzeFilter(
        "event.instrument_code == 'GBP' || has(event.metadata)",
        context,
      );
      expect(result.result).toBe('indeterminate');
    });
  });

  describe('complex expressions', () => {
    it('returns indeterminate for unrecognized expressions', () => {
      const result = analyzeFilter(
        'event.amount > 100',
        {},
      );
      expect(result.result).toBe('indeterminate');
      expect(result.reason).toContain('Complex expression');
    });
  });

  describe('double-quoted string literals', () => {
    it('handles double-quoted equality', () => {
      const context: EventContext = { instrumentCode: 'GBP' };
      const result = analyzeFilter(
        'event.instrument_code == "GBP"',
        context,
      );
      expect(result.result).toBe('pass');
    });

    it('handles double-quoted inequality', () => {
      const context: EventContext = { instrumentCode: 'USD' };
      const result = analyzeFilter(
        'event.instrument_code != "GBP"',
        context,
      );
      expect(result.result).toBe('pass');
    });
  });

  describe('unknown field in inequality', () => {
    it('returns indeterminate for unknown field in != expression', () => {
      const result = analyzeFilter(
        "event.unknown_field != 'value'",
        {},
      );
      expect(result.result).toBe('indeterminate');
      expect(result.reason).toContain('Unknown field');
    });
  });

  describe('missing context in inequality', () => {
    it('returns indeterminate when context value missing for != expression', () => {
      const result = analyzeFilter(
        "event.instrument_code != 'GBP'",
        {},
      );
      expect(result.result).toBe('indeterminate');
      expect(result.reason).toContain('missing');
    });
  });

  describe('nested parentheses', () => {
    it('returns indeterminate for parenthesised subexpressions (parens not stripped)', () => {
      // The analyzer does not strip outer parens from sub-parts, so they are treated
      // as complex unrecognised expressions and return indeterminate.
      const context: EventContext = { instrumentCode: 'GBP', direction: 'CREDIT' };
      const result = analyzeFilter(
        "(event.instrument_code == 'GBP') && (event.direction == 'CREDIT')",
        context,
      );
      expect(result.result).toBe('indeterminate');
    });
  });

  describe('has() short-circuit in or expressions', () => {
    it('returns indeterminate when expression contains has() even with a passing branch', () => {
      // has() is detected on the whole expression before compound splitting, so the
      // entire filter is treated as indeterminate regardless of other branches.
      const context: EventContext = { instrumentCode: 'GBP' };
      const result = analyzeFilter(
        "has(event.metadata) || event.instrument_code == 'GBP'",
        context,
      );
      expect(result.result).toBe('indeterminate');
    });
  });
});

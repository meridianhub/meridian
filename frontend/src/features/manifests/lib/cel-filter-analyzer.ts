export type FilterResult = 'pass' | 'fail' | 'indeterminate';

export interface FilterAnalysis {
  result: FilterResult;
  reason: string;
}

export interface EventContext {
  instrumentCode?: string;
  direction?: 'DEBIT' | 'CREDIT';
  accountType?: string;
  [key: string]: string | undefined;
}

const FIELD_MAP: Record<string, keyof EventContext> = {
  'event.instrument_code': 'instrumentCode',
  'event.direction': 'direction',
  'event.account_type': 'accountType',
};

function parseSimpleComparison(
  expr: string,
  context: EventContext,
): FilterAnalysis | null {
  // Match: event.field == 'value' or event.field == "value"
  const eqMatch = expr.match(
    /^(event\.\w+)\s*==\s*['"]([^'"]*)['"]\s*$/,
  );
  if (eqMatch) {
    const [, field, value] = eqMatch;
    const contextKey = FIELD_MAP[field];
    if (!contextKey) {
      return {
        result: 'indeterminate',
        reason: `Unknown field: ${field}`,
      };
    }
    const contextValue = context[contextKey];
    if (contextValue === undefined) {
      return {
        result: 'indeterminate',
        reason: `Context missing value for ${field}`,
      };
    }
    return contextValue === value
      ? { result: 'pass', reason: `${field} equals '${value}'` }
      : {
          result: 'fail',
          reason: `${field} is '${contextValue}', expected '${value}'`,
        };
  }

  // Match: event.field != 'value' or event.field != "value"
  const neqMatch = expr.match(
    /^(event\.\w+)\s*!=\s*['"]([^'"]*)['"]\s*$/,
  );
  if (neqMatch) {
    const [, field, value] = neqMatch;
    const contextKey = FIELD_MAP[field];
    if (!contextKey) {
      return {
        result: 'indeterminate',
        reason: `Unknown field: ${field}`,
      };
    }
    const contextValue = context[contextKey];
    if (contextValue === undefined) {
      return {
        result: 'indeterminate',
        reason: `Context missing value for ${field}`,
      };
    }
    return contextValue !== value
      ? { result: 'pass', reason: `${field} is not '${value}'` }
      : {
          result: 'fail',
          reason: `${field} is '${contextValue}', which equals '${value}'`,
        };
  }

  return null;
}

function splitCompoundParts(
  expr: string,
  operator: '&&' | '||',
): string[] | null {
  // Split on the operator, respecting parentheses depth
  const parts: string[] = [];
  let depth = 0;
  let current = '';
  const op = operator;

  for (let i = 0; i < expr.length; i++) {
    const char = expr[i];
    if (char === '(') {
      depth++;
      current += char;
    } else if (char === ')') {
      depth--;
      current += char;
    } else if (
      depth === 0 &&
      expr.substring(i, i + op.length) === op
    ) {
      parts.push(current.trim());
      current = '';
      i += op.length - 1;
    } else {
      current += char;
    }
  }
  if (current.trim()) {
    parts.push(current.trim());
  }

  return parts.length > 1 ? parts : null;
}

function analyzeExpression(
  expr: string,
  context: EventContext,
): FilterAnalysis {
  const trimmed = expr.trim();

  // has() checks
  if (/\bhas\s*\(/.test(trimmed)) {
    return {
      result: 'indeterminate',
      reason: 'has() checks require runtime evaluation',
    };
  }

  // chain_depth references
  if (/\bchain_depth\b/.test(trimmed)) {
    return {
      result: 'indeterminate',
      reason: 'chain_depth requires runtime evaluation',
    };
  }

  // Simple comparison
  const simple = parseSimpleComparison(trimmed, context);
  if (simple) {
    return simple;
  }

  // Compound && expression
  const andParts = splitCompoundParts(trimmed, '&&');
  if (andParts) {
    const results = andParts.map((part) => analyzeExpression(part, context));
    if (results.every((r) => r.result === 'pass')) {
      return { result: 'pass', reason: 'All conditions pass' };
    }
    if (results.some((r) => r.result === 'fail')) {
      const failing = results.find((r) => r.result === 'fail')!;
      return { result: 'fail', reason: failing.reason };
    }
    return {
      result: 'indeterminate',
      reason: 'Some conditions require runtime evaluation',
    };
  }

  // Compound || expression
  const orParts = splitCompoundParts(trimmed, '||');
  if (orParts) {
    const results = orParts.map((part) => analyzeExpression(part, context));
    if (results.some((r) => r.result === 'pass')) {
      return { result: 'pass', reason: 'At least one condition passes' };
    }
    if (results.every((r) => r.result === 'fail')) {
      return { result: 'fail', reason: 'All conditions fail' };
    }
    return {
      result: 'indeterminate',
      reason: 'Some conditions require runtime evaluation',
    };
  }

  return {
    result: 'indeterminate',
    reason: 'Complex expression requires runtime evaluation',
  };
}

export function analyzeFilter(
  filterExpression: string,
  context: EventContext,
): FilterAnalysis {
  if (!filterExpression || filterExpression.trim() === '') {
    return { result: 'pass', reason: 'No filter applied' };
  }

  return analyzeExpression(filterExpression, context);
}

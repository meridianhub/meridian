export interface SagaFlow {
  name: string
  trigger: string | null
  filter: string | null
  steps: SagaFlowStep[]
}

export interface SagaFlowStep {
  name: string
  lineNumber: number
  serviceCalls: ServiceCall[]
  earlyExit: EarlyExit | null
}

export interface ServiceCall {
  service: string
  method: string
  params: string[]
}

export interface EarlyExit {
  condition: string
  returnStatus: string
}

/**
 * Extract the producing service name from a trigger string.
 * - "event:position-keeping.transaction-captured.v1" → "position-keeping"
 * - "webhook:stripe.payment_intent.succeeded" → "stripe"
 * - "api:/v1/payments/stripe" → null (no service)
 */
export function parseTriggerService(trigger: string | null): string | null {
  if (!trigger) return null
  if (trigger.startsWith('event:') || trigger.startsWith('webhook:')) {
    const rest = trigger.slice(trigger.indexOf(':') + 1)
    const dotIdx = rest.indexOf('.')
    return dotIdx > 0 ? rest.slice(0, dotIdx) : rest
  }
  return null
}

/**
 * Parse a Starlark saga source file into a structured SagaFlow.
 * Uses regex-based static analysis (not execution).
 */
export function parseStarlarkSaga(source: string): SagaFlow {
  const lines = source.split('\n')

  const name = extractSagaName(lines)
  const trigger = extractHeaderField(lines, 'Trigger')
  const filter = extractHeaderField(lines, 'Filter')
  const steps = extractSteps(lines)

  return { name, trigger, filter, steps }
}

function extractSagaName(lines: string[]): string {
  // Try saga(name="...") pattern first
  for (const line of lines) {
    const match = line.match(/saga\(\s*name\s*=\s*"([^"]+)"/)
    if (match) return match[1]
  }

  // Fall back to # Saga: ... header comment
  for (const line of lines) {
    const match = line.match(/^#\s*Saga:\s*(.+)/)
    if (match) return match[1].trim()
  }

  return 'unknown'
}

function extractHeaderField(lines: string[], field: string): string | null {
  for (const line of lines) {
    const match = line.match(new RegExp(`^#\\s*${field}:\\s*(.+)`))
    if (match) return match[1].trim()
  }
  return null
}

/**
 * Extract steps by finding step(name="...") calls and analyzing the code
 * between consecutive step calls.
 */
function extractSteps(lines: string[]): SagaFlowStep[] {
  // Find all step positions
  const stepPositions: { name: string; lineNumber: number; lineIndex: number }[] = []

  for (let i = 0; i < lines.length; i++) {
    // Check dynamic pattern first (step(name="prefix_" + expr))
    const dynamicMatch = lines[i].match(/step\(\s*name\s*=\s*"([^"]+)"\s*\+/)
    if (dynamicMatch) {
      stepPositions.push({
        name: `${dynamicMatch[1]}*`,
        lineNumber: i + 1,
        lineIndex: i,
      })
      continue
    }

    // Static step name
    const staticMatch = lines[i].match(/step\(\s*name\s*=\s*"([^"]+)"/)
    if (staticMatch) {
      stepPositions.push({
        name: staticMatch[1],
        lineNumber: i + 1,
        lineIndex: i,
      })
    }
  }

  // Sort by line index
  stepPositions.sort((a, b) => a.lineIndex - b.lineIndex)

  // Deduplicate dynamic step names that share the same base
  // e.g., get_balance_0, get_balance_1 -> just keep the first with suffix *
  const deduped = deduplicateDynamicSteps(stepPositions)

  // Extract content blocks between steps
  const steps: SagaFlowStep[] = []
  for (let i = 0; i < deduped.length; i++) {
    const start = deduped[i].lineIndex
    const end =
      i + 1 < deduped.length ? deduped[i + 1].lineIndex : lines.length

    const block = lines.slice(start, end)
    const serviceCalls = extractServiceCalls(block)
    const earlyExit = extractEarlyExit(block)

    steps.push({
      name: deduped[i].name,
      lineNumber: deduped[i].lineNumber,
      serviceCalls,
      earlyExit,
    })
  }

  return steps
}

/**
 * Deduplicate dynamic steps that are inside for loops.
 * Steps with names like "get_balance_" + str(count) produce
 * step(name="get_balance_0"), step(name="get_balance_1"), etc.
 * We collapse them into a single step with name "get_balance_*".
 */
function deduplicateDynamicSteps(
  steps: { name: string; lineNumber: number; lineIndex: number }[],
): { name: string; lineNumber: number; lineIndex: number }[] {
  // Dynamic steps already have * suffix from extraction
  // Just remove duplicates with same base at same line
  const seen = new Set<number>()
  return steps.filter((s) => {
    if (seen.has(s.lineIndex)) return false
    seen.add(s.lineIndex)
    return true
  })
}

const SERVICE_CALL_RE =
  /(\w+)\.(\w+)\(/g

function extractServiceCalls(block: string[]): ServiceCall[] {
  const calls: ServiceCall[] = []
  const seen = new Set<string>()

  // Known built-in names that are NOT service modules
  const builtins = new Set([
    'input_data',
    'ctx',
    'str',
    'len',
    'Decimal',
    'account',
    'metadata',
    'account_type',
    'structuring',
    'instrument_code',
    'position',
    'balance',
    'p',
  ])

  for (const line of block) {
    const trimmed = line.trim()
    // Skip comments and step declarations
    if (trimmed.startsWith('#') || trimmed.startsWith('step(')) continue

    SERVICE_CALL_RE.lastIndex = 0
    let match
    while ((match = SERVICE_CALL_RE.exec(trimmed)) !== null) {
      const [, service, method] = match
      const key = `${service}.${method}`

      // Filter out non-service patterns
      if (builtins.has(service)) continue
      // Skip attribute access patterns that aren't method calls with args
      // e.g., account.metadata, ctx.amount
      if (seen.has(key)) continue

      seen.add(key)
      const params = extractParams(trimmed, match.index + match[0].length)
      calls.push({ service, method, params })
    }
  }

  return calls
}

/**
 * Extract parameter names from a function call starting after the opening paren.
 */
function extractParams(line: string, startAfterParen: number): string[] {
  // Find the matching closing paren
  let depth = 1
  let i = startAfterParen
  while (i < line.length && depth > 0) {
    if (line[i] === '(') depth++
    if (line[i] === ')') depth--
    i++
  }

  const argsStr = line.slice(startAfterParen, i - 1)
  if (!argsStr.trim()) return []

  // Extract keyword argument names (Starlark uses keyword args)
  const params: string[] = []
  const kwargRe = /(\w+)\s*=/g
  let m
  while ((m = kwargRe.exec(argsStr)) !== null) {
    params.push(m[1])
  }

  return params
}

function extractEarlyExit(block: string[]): EarlyExit | null {
  // Look for pattern: if <condition>: return { "status": "..." }
  // or multi-line: if <condition>:\n    return {"status": "..."}
  for (let i = 0; i < block.length; i++) {
    const line = block[i].trim()

    // Single-line: if condition: return "STATUS"
    const singleLine = line.match(
      /^if\s+(.+?):\s*$/,
    )

    if (singleLine) {
      const condition = singleLine[1]
      // Look ahead for return with status
      for (let j = i + 1; j < block.length && j < i + 10; j++) {
        const retLine = block[j].trim()
        if (retLine.startsWith('return')) {
          const status = extractReturnStatus(block, j)
          if (status) {
            return { condition, returnStatus: status }
          }
          break
        }
        // Stop if we hit something that isn't part of the if body
        if (
          retLine !== '' &&
          !retLine.startsWith('"') &&
          !retLine.startsWith("'") &&
          !retLine.startsWith('}') &&
          !retLine.startsWith('#')
        ) {
          break
        }
      }
    }
  }

  return null
}

/**
 * Extract the status value from a return statement that returns a dict with "status" key.
 * Handles both single-line and multi-line return dicts.
 */
function extractReturnStatus(lines: string[], returnLineIdx: number): string | null {
  // Collect lines starting from the return statement until we find the status
  let combined = ''
  for (let i = returnLineIdx; i < lines.length && i < returnLineIdx + 10; i++) {
    combined += ' ' + lines[i].trim()
    // Check for status in what we have so far
    const statusMatch = combined.match(/["']status["']\s*:\s*["']([^"']+)["']/)
    if (statusMatch) return statusMatch[1]
    // Stop if we've closed the dict
    if (combined.includes('}')) break
  }
  return null
}

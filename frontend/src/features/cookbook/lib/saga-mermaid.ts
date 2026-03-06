import type { SagaFlow } from './star-parser'

/**
 * Generate a Mermaid flowchart TD markup from a parsed SagaFlow.
 */
export function generateMermaidMarkup(flow: SagaFlow): string {
  const lines: string[] = ['flowchart TD']

  if (flow.steps.length === 0) {
    lines.push(`    START(["${escapeMermaid(flow.name)}"]) --> END(["COMPLETED"])`)
    return lines.join('\n')
  }

  // Start node
  const startLabel = flow.trigger
    ? `${escapeMermaid(flow.name)}\\n${escapeMermaid(flow.trigger)}`
    : escapeMermaid(flow.name)
  lines.push(`    START(["${startLabel}"]) --> S1`)

  // Steps
  for (let i = 0; i < flow.steps.length; i++) {
    const step = flow.steps[i]
    const nodeId = `S${i + 1}`
    const nextNodeId = i + 1 < flow.steps.length ? `S${i + 2}` : 'END'

    // Build step label: step name + service calls
    const callLabels = step.serviceCalls.map(
      (c) => `${c.service}.${c.method}`,
    )
    const label = [escapeMermaid(step.name), ...callLabels.map(escapeMermaid)].join(
      '\\n',
    )

    lines.push(`    ${nodeId}["${label}"]`)

    if (step.earlyExit) {
      const decisionId = `D${i + 1}`
      lines.push(`    ${nodeId} --> ${decisionId}`)
      lines.push(
        `    ${decisionId}{"${escapeMermaid(step.earlyExit.condition)}"}`,
      )
      lines.push(
        `    ${decisionId} -->|Yes| EXIT_${i + 1}(["${escapeMermaid(step.earlyExit.returnStatus)}"])`,
      )
      lines.push(`    ${decisionId} -->|No| ${nextNodeId}`)
    } else {
      lines.push(`    ${nodeId} --> ${nextNodeId}`)
    }
  }

  // End node
  lines.push('    END(["COMPLETED"])')

  return lines.join('\n')
}

function escapeMermaid(text: string): string {
  return text
    .replace(/"/g, '#quot;')
    .replace(/[[\]{}()]/g, (ch) => `#${ch.charCodeAt(0)};`)
}

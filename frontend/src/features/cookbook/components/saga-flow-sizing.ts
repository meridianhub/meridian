/** Estimate diamond dimensions based on label length. The diamond clip-path
 *  only exposes ~50% of width/height for text, so we scale up for longer labels. */
export function estimateDecisionSize(label: string): { width: number; height: number } {
  const charWidth = 6 // approximate px per character at text-[10px]
  const textWidth = label.length * charWidth
  // Diamond usable area is ~50% of dimensions, add padding
  const width = Math.max(120, Math.min(220, textWidth * 2 + 40))
  const height = Math.max(80, Math.round(width * 0.7))
  return { width, height }
}

/** Estimate start node width based on the longer of name or trigger text. */
export function estimateStartNodeWidth(label: string, trigger: string | null): number {
  const charWidth = 6
  const labelWidth = label.length * charWidth + 32 // px-4 padding
  const triggerWidth = trigger ? trigger.length * charWidth + 16 : 0
  return Math.max(160, Math.min(300, Math.max(labelWidth, triggerWidth)))
}

const DEFAULT_NODE_DIMENSIONS: Record<string, { width: number; height: number }> = {
  sagaStart: { width: 160, height: 50 },
  sagaStep: { width: 200, height: 60 },
  sagaDecision: { width: 120, height: 80 },
  sagaExit: { width: 120, height: 36 },
  sagaEnd: { width: 140, height: 44 },
}

/** Get node dimensions, using dynamic sizing for decision and start nodes. */
export function getNodeDimensions(
  type: string | undefined,
  label: string,
  trigger: string | null,
): { width: number; height: number } {
  if (type === 'sagaDecision') {
    return estimateDecisionSize(label)
  }
  if (type === 'sagaStart') {
    const width = estimateStartNodeWidth(label, trigger)
    return { width, height: trigger ? 56 : 44 }
  }
  return DEFAULT_NODE_DIMENSIONS[type ?? 'sagaStep'] ?? { width: 200, height: 60 }
}

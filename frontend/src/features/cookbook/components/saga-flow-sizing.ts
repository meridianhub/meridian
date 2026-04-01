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

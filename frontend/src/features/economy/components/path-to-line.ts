/**
 * Maps a validation error path (e.g. "instruments[0].code") to a line number
 * in the YAML source by walking the parsed YAML structure positions.
 */
export function pathToLine(source: string, path: string): number | null {
  if (!path) return null

  const lines = source.split('\n')

  // Parse path segments: "instruments[0].code" -> ["instruments", "0", "code"]
  const segments = path
    .replace(/\[(\d+)\]/g, '.$1')
    .split('.')
    .filter(Boolean)

  if (segments.length === 0) return null

  // Walk through YAML lines to find the target path
  // This is a simple heuristic that works for typical manifest YAML
  let currentIndent = -1
  let segmentIndex = 0
  let arrayCounter = -1
  let lastMatchLine = 0

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trimStart()
    if (!trimmed || trimmed.startsWith('#')) continue

    const indent = line.length - trimmed.length
    const segment = segments[segmentIndex]

    if (segmentIndex === 0 || indent > currentIndent) {
      // Check if this is a numeric index (array element)
      if (/^\d+$/.test(segment)) {
        if (trimmed.startsWith('- ') || trimmed === '-') {
          arrayCounter++
          if (arrayCounter === parseInt(segment, 10)) {
            lastMatchLine = i
            segmentIndex++
            currentIndent = indent
            arrayCounter = -1
            if (segmentIndex >= segments.length) return i + 1 // 1-indexed
            continue
          }
        }
        continue
      }

      // Check for key match
      const keyMatch = trimmed.match(/^-?\s*(\w[\w-]*)/)
      if (keyMatch && keyMatch[1] === segment) {
        lastMatchLine = i
        segmentIndex++
        currentIndent = indent
        arrayCounter = -1
        if (segmentIndex >= segments.length) return i + 1 // 1-indexed
      }
    }
  }

  // If we matched at least the first segment, return last match
  if (segmentIndex > 0) return lastMatchLine + 1
  return null
}

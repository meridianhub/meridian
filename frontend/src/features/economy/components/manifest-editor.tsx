import { useCallback, useEffect, useMemo, useRef } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'
import { linter, lintGutter, type Diagnostic } from '@codemirror/lint'
import { load as yamlLoad } from 'js-yaml'
import type { ValidationError } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

interface ManifestEditorProps {
  value: string
  onChange: (value: string) => void
  validationErrors?: ValidationError[]
  className?: string
}

/**
 * Maps a validation error path (e.g. "instruments[0].code") to a line number
 * in the YAML source by walking the parsed YAML structure positions.
 */
function pathToLine(source: string, path: string): number | null {
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

function createDiagnostics(
  source: string,
  errors: ValidationError[],
): Diagnostic[] {
  if (!source || errors.length === 0) return []

  return errors
    .map((error) => {
      const lineNum = pathToLine(source, error.path)
      if (lineNum === null) return null

      const lines = source.split('\n')
      const lineIndex = lineNum - 1
      if (lineIndex < 0 || lineIndex >= lines.length) return null

      // Calculate character offsets
      let from = 0
      for (let i = 0; i < lineIndex; i++) {
        from += lines[i].length + 1 // +1 for newline
      }
      const to = from + lines[lineIndex].length

      return {
        from,
        to,
        severity: error.severity === 'ERROR' ? 'error' : ('warning' as const),
        message: error.message,
      }
    })
    .filter((d): d is Diagnostic => d !== null)
}

export function ManifestEditor({
  value,
  onChange,
  validationErrors = [],
  className,
}: ManifestEditorProps) {
  const diagnosticsRef = useRef<Diagnostic[]>([])

  // Update diagnostics when validation errors change
  useEffect(() => {
    diagnosticsRef.current = createDiagnostics(value, validationErrors)
  }, [value, validationErrors])

  const extensions = useMemo(() => {
    const linterExtension = linter(() => diagnosticsRef.current)
    return [yaml(), lintGutter(), linterExtension]
  }, [])

  const handleChange = useCallback(
    (val: string) => {
      onChange(val)
    },
    [onChange],
  )

  return (
    <CodeMirror
      data-testid="codemirror-editor"
      value={value}
      onChange={handleChange}
      extensions={extensions}
      className={className}
      basicSetup={{
        lineNumbers: true,
        foldGutter: true,
        highlightActiveLine: true,
        bracketMatching: true,
      }}
    />
  )
}

export { pathToLine }

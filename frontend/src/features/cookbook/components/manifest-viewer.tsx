import { useCallback, useEffect, useRef, useState } from 'react'
import { EditorView } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import { basicSetup } from 'codemirror'
import { Copy, Check } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface ManifestViewerProps {
  content: string
  className?: string
}

export function ManifestViewer({ content, className }: ManifestViewerProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const copyTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!editorRef.current) return

    const view = new EditorView({
      state: EditorState.create({
        doc: content,
        extensions: [
          basicSetup,
          EditorView.editable.of(false),
          EditorState.readOnly.of(true),
        ],
      }),
      parent: editorRef.current,
    })

    viewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
    }
  }, [content])

  useEffect(() => {
    return () => {
      if (copyTimeoutRef.current) clearTimeout(copyTimeoutRef.current)
    }
  }, [])

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(content)
      if (copyTimeoutRef.current) clearTimeout(copyTimeoutRef.current)
      setCopied(true)
      copyTimeoutRef.current = setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard access denied (non-HTTPS or permission denied)
    }
  }, [content])

  return (
    <div data-testid="manifest-viewer" className={cn('relative', className)}>
      <Button
        variant="ghost"
        size="sm"
        className="absolute right-2 top-2 z-10 h-7 w-7 p-0"
        onClick={handleCopy}
        aria-label="Copy manifest"
      >
        {copied ? (
          <Check className="h-3.5 w-3.5 text-green-600" />
        ) : (
          <Copy className="h-3.5 w-3.5" />
        )}
      </Button>
      <div
        ref={editorRef}
        className="min-h-[200px] rounded border border-input bg-background text-sm"
      />
    </div>
  )
}

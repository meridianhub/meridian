import { useState } from 'react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

interface PreviewSourceTabsProps {
  preview: React.ReactNode
  source: string
  sourceLabel?: string
  className?: string
}

export function PreviewSourceTabs({ preview, source, sourceLabel = 'Source', className }: PreviewSourceTabsProps) {
  const [copied, setCopied] = useState(false)

  const handleCopy = () => {
    navigator.clipboard.writeText(source).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }).catch(() => {
      // Silently fail in restricted contexts
    })
  }

  return (
    <Tabs defaultValue="preview" className={className}>
      <TabsList>
        <TabsTrigger value="preview">Preview</TabsTrigger>
        <TabsTrigger value="source">{sourceLabel}</TabsTrigger>
      </TabsList>

      <TabsContent value="preview" className="mt-4">
        {preview}
      </TabsContent>

      <TabsContent value="source" className="mt-4">
        <div className="relative">
          <button
            type="button"
            onClick={handleCopy}
            className="absolute right-2 top-2 z-10 rounded border bg-background px-2 py-1 text-xs text-muted-foreground hover:bg-accent"
          >
            {copied ? 'Copied' : 'Copy'}
          </button>
          <pre className="overflow-auto rounded-lg border bg-muted/50 p-4 text-xs leading-relaxed">
            <code>{source}</code>
          </pre>
        </div>
      </TabsContent>
    </Tabs>
  )
}

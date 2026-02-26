import { useEffect, useRef, useState } from 'react'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Copy, Check, ExternalLink } from 'lucide-react'
import { useTenantContext } from '@/contexts/tenant-context'
import { McpToolsSection } from './mcp-tools-section'

const MCP_SERVER_URL =
  import.meta.env.VITE_MCP_SERVER_URL ??
  import.meta.env.VITE_API_BASE_URL ??
  'http://localhost:8091'

function buildClaudeDesktopConfig(serverUrl: string): string {
  const config = {
    mcpServers: {
      meridian: {
        command: 'npx',
        args: ['-y', 'mcp-remote', `${serverUrl}/sse`],
      },
    },
  }
  return JSON.stringify(config, null, 2)
}

function CopyButton({ text, label }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const resetTimerRef = useRef<number | null>(null)

  useEffect(() => {
    return () => {
      if (resetTimerRef.current !== null) {
        window.clearTimeout(resetTimerRef.current)
      }
    }
  }, [])

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      if (resetTimerRef.current !== null) {
        window.clearTimeout(resetTimerRef.current)
      }
      resetTimerRef.current = window.setTimeout(() => setCopied(false), 2000)
    } catch {
      setCopied(false)
    }
  }

  return (
    <Button
      variant="outline"
      size="sm"
      onClick={handleCopy}
      aria-label={label ?? 'Copy to clipboard'}
    >
      {copied ? (
        <>
          <Check className="mr-1.5 size-3.5" />
          Copied!
        </>
      ) : (
        <>
          <Copy className="mr-1.5 size-3.5" />
          Copy
        </>
      )}
    </Button>
  )
}

export function McpConfigPage() {
  const { tenantSlug } = useTenantContext()

  const sseUrl = `${MCP_SERVER_URL}/sse`
  const oauthUrl = `${MCP_SERVER_URL}/oauth/authorize`
  const claudeDesktopConfig = buildClaudeDesktopConfig(MCP_SERVER_URL)

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">MCP Configuration</h1>
          <p className="mt-2 text-muted-foreground">
            Connect AI assistants to Meridian via the Model Context Protocol (MCP)
          </p>
        </div>
        {tenantSlug && (
          <Badge variant="secondary" className="mt-1">
            Tenant: {tenantSlug}
          </Badge>
        )}
      </div>

      {/* Server Connection */}
      <Card className="p-6 space-y-4">
        <div>
          <h2 className="text-lg font-semibold">Server Connection</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            SSE endpoint for connecting MCP clients to Meridian
          </p>
        </div>
        <div className="flex items-center gap-3">
          <code
            data-testid="sse-url"
            className="flex-1 rounded-md bg-muted px-3 py-2 font-mono text-sm"
          >
            {sseUrl}
          </code>
          <CopyButton text={sseUrl} label="Copy SSE URL" />
        </div>
      </Card>

      {/* Claude Desktop Configuration */}
      <Card className="p-6 space-y-4">
        <div className="flex items-start justify-between">
          <div>
            <h2 className="text-lg font-semibold">Claude Desktop Configuration</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Add this configuration to your Claude Desktop{' '}
              <code className="text-xs">claude_desktop_config.json</code>
            </p>
          </div>
          <CopyButton text={claudeDesktopConfig} label="Copy Claude Desktop config" />
        </div>
        <pre
          data-testid="claude-desktop-config"
          className="overflow-x-auto rounded-md bg-muted p-4 font-mono text-sm"
        >
          {claudeDesktopConfig}
        </pre>
      </Card>

      {/* OAuth Authorization */}
      <Card className="p-6 space-y-4">
        <div>
          <h2 className="text-lg font-semibold">OAuth Authorization</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Browser-based MCP clients authenticate via OAuth. Use this URL to initiate the
            authorization flow.
          </p>
        </div>
        <div className="flex items-center gap-3">
          <code
            data-testid="oauth-url"
            className="flex-1 rounded-md bg-muted px-3 py-2 font-mono text-sm"
          >
            {oauthUrl}
          </code>
          <CopyButton text={oauthUrl} label="Copy OAuth URL" />
        </div>
      </Card>

      {/* Documentation */}
      <Card className="p-6 space-y-4">
        <div>
          <h2 className="text-lg font-semibold">Documentation</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Complete setup guide, authentication options, and tool reference
          </p>
        </div>
        <a
          data-testid="readme-link"
          href="https://github.com/meridianhub/meridian/blob/develop/services/mcp-server/README.md"
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1.5 text-sm font-medium text-primary hover:underline"
        >
          MCP Server README
          <ExternalLink className="size-3.5" />
        </a>
      </Card>

      {/* MCP Tools */}
      <Card className="p-6">
        <McpToolsSection />
      </Card>
    </div>
  )
}

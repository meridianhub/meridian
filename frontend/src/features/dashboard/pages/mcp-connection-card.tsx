import { useEffect, useRef, useState } from 'react'
import { Bot, Check, Copy } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useAuth } from '@/contexts/auth-context'
import { useTenantContext } from '@/contexts/tenant-context'
import { buildMcpTenantUrl } from '@/api/config'

const MCP_BASE_URL =
  import.meta.env.VITE_MCP_SERVER_URL ??
  import.meta.env.VITE_API_BASE_URL ??
  'http://localhost:8091'

function buildClaudeCodeConfig(serverUrl: string): string {
  return JSON.stringify(
    {
      mcpServers: {
        meridian: {
          type: 'streamable-http',
          url: `${serverUrl}/mcp`,
        },
      },
    },
    null,
    2,
  )
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<number | null>(null)

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current)
    }
  }, [])

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      if (timerRef.current !== null) window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(() => setCopied(false), 2000)
    } catch {
      setCopied(false)
    }
  }

  return (
    <Button variant="outline" size="sm" onClick={handleCopy} aria-label={label}>
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

export function McpConnectionCard() {
  const { tenantSlug } = useTenantContext()
  const { accessToken } = useAuth()

  if (!tenantSlug) return null

  const serverUrl = buildMcpTenantUrl(MCP_BASE_URL, tenantSlug)
  const mcpUrl = `${serverUrl}/mcp`
  const claudeCodeConfig = buildClaudeCodeConfig(serverUrl)

  const tokenPreview = accessToken
    ? accessToken.length > 18
      ? `${accessToken.slice(0, 12)}...${accessToken.slice(-6)}`
      : `${accessToken.slice(0, 4)}...`
    : null

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Bot className="h-4 w-4" />
          AI Connection
          <Badge variant="secondary" className="ml-auto text-xs font-normal">
            MCP
          </Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* MCP URL */}
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">Server URL</p>
          <div className="flex items-center gap-2">
            <code
              data-testid="mcp-card-url"
              className="flex-1 truncate rounded-md bg-muted px-2.5 py-1.5 font-mono text-xs"
            >
              {mcpUrl}
            </code>
            <CopyButton text={mcpUrl} label="Copy MCP server URL" />
          </div>
        </div>

        {/* Auth token preview */}
        {tokenPreview && (
          <div className="space-y-1.5">
            <p className="text-xs font-medium text-muted-foreground">Auth Token</p>
            <div className="flex items-center gap-2">
              <code
                data-testid="mcp-card-token-preview"
                className="flex-1 rounded-md bg-muted px-2.5 py-1.5 font-mono text-xs"
              >
                {tokenPreview}
              </code>
              <CopyButton text={accessToken!} label="Copy auth token" />
            </div>
          </div>
        )}

        {/* Claude Code / .mcp.json config */}
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <p className="text-xs font-medium text-muted-foreground">
              Claude Code <span className="font-normal opacity-70">(.mcp.json)</span>
            </p>
            <CopyButton text={claudeCodeConfig} label="Copy Claude Code config" />
          </div>
          <pre
            data-testid="mcp-card-claude-config"
            className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs leading-relaxed"
          >
            {claudeCodeConfig}
          </pre>
        </div>

        {/* Brief instructions */}
        <p className="text-xs text-muted-foreground">
          Add the config above to your{' '}
          <code className="rounded bg-muted px-1 py-0.5 font-mono">.mcp.json</code> or Claude
          Desktop config to connect AI assistants to this tenant.
        </p>
      </CardContent>
    </Card>
  )
}

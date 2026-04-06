interface ConsentInfo {
  client_name: string
  redirect_uri: string
  scopes: string[]
  is_dynamic_client: boolean
}

interface ConsentCardProps {
  consentInfo: ConsentInfo
  onApprove: () => void
  onDeny: () => void
  loading: boolean
}

export function ConsentCard({ consentInfo, onApprove, onDeny, loading }: ConsentCardProps) {
  return (
    <div className="w-full max-w-md rounded-lg border border-border bg-card p-6 shadow-sm space-y-5">
      <div>
        <h1 className="text-xl font-semibold">Authorize Access</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          An application is requesting access to your Meridian account.
        </p>
      </div>

      <div className="rounded-md border border-border bg-muted/40 p-4 space-y-3 text-sm">
        <div>
          <span className="font-medium">Application</span>
          <p className="text-muted-foreground mt-0.5">
            {consentInfo.client_name}
            {consentInfo.is_dynamic_client && (
              <span className="ml-2 inline-flex items-center rounded-full bg-warning/20 px-2 py-0.5 text-xs font-medium text-warning-foreground">
                Dynamic client
              </span>
            )}
          </p>
        </div>
        <div>
          <span className="font-medium">Redirect URI</span>
          <p className="text-muted-foreground mt-0.5 break-all font-mono text-xs">
            {consentInfo.redirect_uri}
          </p>
        </div>
        {consentInfo.scopes.length > 0 && (
          <div>
            <span className="font-medium">Requested scopes</span>
            <ul className="mt-1 space-y-1">
              {consentInfo.scopes.map((scope) => (
                <li key={scope} className="flex items-center gap-2 text-muted-foreground">
                  <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground/60 shrink-0" />
                  <span className="font-mono text-xs">{scope}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      <div className="flex gap-3">
        <button
          onClick={onApprove}
          disabled={loading}
          className="flex-1 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {loading ? 'Processing...' : 'Approve'}
        </button>
        <button
          onClick={onDeny}
          disabled={loading}
          className="flex-1 rounded-md border border-border bg-background px-4 py-2 text-sm font-medium text-foreground hover:bg-muted disabled:opacity-50"
        >
          Deny
        </button>
      </div>
    </div>
  )
}

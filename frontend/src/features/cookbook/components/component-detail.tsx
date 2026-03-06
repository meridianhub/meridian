import { Link } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { CookbookItem, ComponentMeta } from '../hooks/use-cookbook'

interface ComponentDetailProps {
  item: CookbookItem
}

export function ComponentDetail({ item }: ComponentDetailProps) {
  const meta = item.meta as ComponentMeta | undefined

  return (
    <div className="space-y-6">
      {/* Configurable Props */}
      {meta?.configurable_props && meta.configurable_props.length > 0 && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">Configurable Props</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="rounded border">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b bg-muted/50">
                    <th className="px-3 py-2 text-left font-medium">Prop</th>
                    <th className="px-3 py-2 text-left font-medium">Source</th>
                  </tr>
                </thead>
                <tbody>
                  {meta.configurable_props.map((prop) => (
                    <tr key={prop} className="border-b last:border-0">
                      <td className="px-3 py-2 font-mono text-xs">{prop}</td>
                      <td className="px-3 py-2 text-muted-foreground">component.json</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Feature Module Context */}
      {(meta?.feature_module || meta?.tenant_configurable != null || (meta?.used_by && meta.used_by.length > 0)) && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">Usage Context</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {meta?.feature_module && (
              <div className="flex items-center gap-2 text-sm">
                <span className="text-muted-foreground">Feature module:</span>
                <Badge variant="outline">{meta.feature_module}</Badge>
              </div>
            )}
            {meta?.tenant_configurable != null && (
              <div className="flex items-center gap-2 text-sm">
                <span className="text-muted-foreground">Tenant configurable:</span>
                <Badge variant={meta.tenant_configurable ? 'default' : 'secondary'}>
                  {meta.tenant_configurable ? 'Yes' : 'No'}
                </Badge>
              </div>
            )}
            {meta?.used_by && meta.used_by.length > 0 && (
              <div className="flex items-center gap-2 text-sm">
                <span className="text-muted-foreground">Used by:</span>
                <div className="flex flex-wrap gap-1.5">
                  {meta.used_by.map((name) => (
                    <Badge key={name} variant="secondary" className="text-xs">
                      {name}
                    </Badge>
                  ))}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* Registry Dependencies */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base">Registry Dependencies</CardTitle>
        </CardHeader>
        <CardContent>
          {item.registryDependencies && item.registryDependencies.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {item.registryDependencies.map((dep) => (
                <Link key={dep} to={`/cookbook/${encodeURIComponent(dep)}`}>
                  <Badge variant="outline" className="cursor-pointer hover:bg-accent">
                    {dep}
                  </Badge>
                </Link>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No registry dependencies.</p>
          )}
        </CardContent>
      </Card>

      {/* Source Files */}
      {item.files && item.files.length > 0 && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">Source Files</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-1">
              {item.files.map((file) => (
                <li key={file.path} className="flex items-center gap-2 text-sm">
                  <span className="font-mono text-xs text-muted-foreground">{file.path}</span>
                  {file.type && (
                    <Badge variant="secondary" className="text-xs">
                      {file.type}
                    </Badge>
                  )}
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {/* Preview Placeholder */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base">Live Preview</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col items-center justify-center rounded-lg border border-dashed py-12 text-center">
            <p className="text-sm font-medium text-muted-foreground">Preview not available</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Live component rendering requires a sandboxed runtime environment.
            </p>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

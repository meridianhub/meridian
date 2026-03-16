import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import type { OperationalGatewayConfig } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

export function GatewayPanel({ gateway }: { gateway?: OperationalGatewayConfig }) {
  if (!gateway) {
    return (
      <div data-testid="gateway-empty" className="py-8 text-center text-muted-foreground text-sm">
        No operational gateway configured in this manifest.
      </div>
    )
  }

  const { providerConnections, instructionRoutes, inboundRoutes } = gateway

  return (
    <div className="space-y-6" data-testid="gateway-panel">
      <Accordion type="multiple" defaultValue={['provider-connections', 'instruction-routes']}>
        {/* Provider Connections */}
        <AccordionItem value="provider-connections">
          <AccordionTrigger>
            <span className="flex items-center gap-2">
              Provider Connections
              <Badge variant="secondary" className="text-xs">{providerConnections.length}</Badge>
            </span>
          </AccordionTrigger>
          <AccordionContent>
            {providerConnections.length === 0 ? (
              <p className="text-sm text-muted-foreground">No provider connections configured.</p>
            ) : (
              <div className="space-y-2">
                {providerConnections.map((pc) => (
                  <Card key={pc.connectionId}>
                    <CardContent className="px-4 py-3 space-y-1">
                      <div className="flex items-center justify-between">
                        <span className="text-sm font-medium">{pc.providerName}</span>
                        <div className="flex items-center gap-2">
                          <Badge variant="outline" className="font-mono text-xs">{pc.providerType}</Badge>
                          <Badge variant="secondary" className="font-mono text-xs">{pc.connectionId}</Badge>
                        </div>
                      </div>
                      <p className="text-xs text-muted-foreground font-mono">{pc.baseUrl}</p>
                      <div className="flex gap-2 flex-wrap">
                        {pc.retryPolicy && (
                          <Badge variant="outline" className="text-xs">
                            Retry: {pc.retryPolicy.maxAttempts} attempts
                          </Badge>
                        )}
                        {pc.rateLimit && (
                          <Badge variant="outline" className="text-xs">
                            Rate: {pc.rateLimit.requestsPerSecond}/s
                          </Badge>
                        )}
                      </div>
                    </CardContent>
                  </Card>
                ))}
              </div>
            )}
          </AccordionContent>
        </AccordionItem>

        {/* Instruction Routes */}
        <AccordionItem value="instruction-routes">
          <AccordionTrigger>
            <span className="flex items-center gap-2">
              Instruction Routes
              <Badge variant="secondary" className="text-xs">{instructionRoutes.length}</Badge>
            </span>
          </AccordionTrigger>
          <AccordionContent>
            {instructionRoutes.length === 0 ? (
              <p className="text-sm text-muted-foreground">No instruction routes configured.</p>
            ) : (
              <div className="space-y-2">
                {instructionRoutes.map((ir) => (
                  <Card key={ir.instructionType}>
                    <CardContent className="px-4 py-3">
                      <div className="flex items-center justify-between">
                        <span className="text-sm font-mono font-medium">{ir.instructionType}</span>
                        <div className="flex items-center gap-2">
                          <Badge variant="outline" className="font-mono text-xs">{ir.connectionId}</Badge>
                          {ir.fallbackConnectionId && (
                            <Badge variant="secondary" className="font-mono text-xs">
                              fallback: {ir.fallbackConnectionId}
                            </Badge>
                          )}
                        </div>
                      </div>
                    </CardContent>
                  </Card>
                ))}
              </div>
            )}
          </AccordionContent>
        </AccordionItem>

        {/* Inbound Routes */}
        <AccordionItem value="inbound-routes">
          <AccordionTrigger>
            <span className="flex items-center gap-2">
              Inbound Routes
              <Badge variant="secondary" className="text-xs">{inboundRoutes.length}</Badge>
            </span>
          </AccordionTrigger>
          <AccordionContent>
            {inboundRoutes.length === 0 ? (
              <p className="text-sm text-muted-foreground">No inbound routes configured.</p>
            ) : (
              <div className="space-y-2">
                {inboundRoutes.map((ib, i) => (
                  <Card key={`${ib.externalType}-${i}`}>
                    <CardContent className="px-4 py-3">
                      <span className="text-sm font-mono">{ib.externalType}</span>
                    </CardContent>
                  </Card>
                ))}
              </div>
            )}
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </div>
  )
}

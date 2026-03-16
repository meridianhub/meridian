import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

interface ResourceSectionProps<T> {
  title: string
  items: T[]
  renderItem: (item: T, index: number) => React.ReactNode
  getKey: (item: T, index: number) => string
}

function ResourceSection<T>({ title, items, renderItem, getKey }: ResourceSectionProps<T>) {
  if (items.length === 0) return null

  return (
    <AccordionItem value={title}>
      <AccordionTrigger>
        <span className="flex items-center gap-2">
          {title}
          <Badge variant="secondary" className="text-xs">{items.length}</Badge>
        </span>
      </AccordionTrigger>
      <AccordionContent>
        <div className="space-y-2">
          {items.map((item, i) => (
            <Card key={getKey(item, i)}>
              <CardContent className="px-4 py-3">
                {renderItem(item, i)}
              </CardContent>
            </Card>
          ))}
        </div>
      </AccordionContent>
    </AccordionItem>
  )
}

function renderCodeName(item: { code?: string; name?: string }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-sm font-medium">{item.name || item.code}</span>
      {item.code && <Badge variant="outline" className="font-mono">{item.code}</Badge>}
    </div>
  )
}

export function ResourcesPanel({ manifest }: { manifest: Manifest }) {
  const instruments = manifest.instruments ?? []
  const accountTypes = manifest.accountTypes ?? []
  const valuationRules = manifest.valuationRules ?? []
  const sagas = manifest.sagas ?? []
  const paymentRails = manifest.paymentRails ?? []
  const partyTypes = manifest.partyTypes ?? []
  const mappings = manifest.mappings ?? []
  const seedData = manifest.seedData
  const gateway = manifest.operationalGateway
  const providerConnections = gateway?.providerConnections ?? []
  const instructionRoutes = gateway?.instructionRoutes ?? []
  const inboundRoutes = gateway?.inboundRoutes ?? []

  const seedDataKeys = seedData ? Object.keys(seedData) : []

  const hasAny =
    instruments.length > 0 ||
    accountTypes.length > 0 ||
    valuationRules.length > 0 ||
    sagas.length > 0 ||
    paymentRails.length > 0 ||
    partyTypes.length > 0 ||
    mappings.length > 0 ||
    seedDataKeys.length > 0 ||
    providerConnections.length > 0 ||
    instructionRoutes.length > 0 ||
    inboundRoutes.length > 0

  if (!hasAny) {
    return (
      <div className="py-8 text-center text-muted-foreground text-sm">
        No resources defined in this manifest.
      </div>
    )
  }

  return (
    <Accordion type="multiple" defaultValue={['Instruments', 'Account Types']}>
      <ResourceSection
        title="Instruments"
        items={instruments}
        getKey={(inst) => inst.code}
        renderItem={(inst) => renderCodeName(inst)}
      />

      <ResourceSection
        title="Account Types"
        items={accountTypes}
        getKey={(at) => at.code}
        renderItem={(at) => (
          <div className="space-y-1">
            {renderCodeName(at)}
            {at.allowedInstruments.length > 0 && (
              <p className="text-xs text-muted-foreground">
                Allowed: {at.allowedInstruments.join(', ')}
              </p>
            )}
          </div>
        )}
      />

      <ResourceSection
        title="Valuation Rules"
        items={valuationRules}
        getKey={(vr, i) => `${vr.fromInstrument}-${vr.toInstrument}-${i}`}
        renderItem={(vr) => (
          <div className="flex items-center justify-between">
            <span className="text-sm font-mono">
              {vr.fromInstrument} &rarr; {vr.toInstrument}
            </span>
            <Badge variant="outline">{vr.source || 'fixed'}</Badge>
          </div>
        )}
      />

      <ResourceSection
        title="Sagas"
        items={sagas}
        getKey={(s) => s.name}
        renderItem={(s) => (
          <div className="flex items-center justify-between">
            <span className="text-sm font-mono font-medium">{s.name}</span>
            <Badge variant="secondary">{s.trigger.split(':')[0]}</Badge>
          </div>
        )}
      />

      <ResourceSection
        title="Payment Rails"
        items={paymentRails}
        getKey={(pr) => pr.provider + pr.accountId}
        renderItem={(pr) => (
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium">{pr.provider}</span>
            <Badge variant="outline" className="font-mono">{pr.accountId}</Badge>
          </div>
        )}
      />

      <ResourceSection
        title="Party Types"
        items={partyTypes}
        getKey={(pt) => pt.id || pt.partyType}
        renderItem={(pt) => (
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium">{pt.partyType}</span>
          </div>
        )}
      />

      <ResourceSection
        title="Mappings"
        items={mappings}
        getKey={(m) => m.id || m.name}
        renderItem={(m) => (
          <div className="space-y-0.5">
            <div className="flex items-center justify-between">
              <span className="text-sm font-mono font-medium">{m.name}</span>
              {m.targetRpc && <Badge variant="secondary">{m.targetRpc}</Badge>}
            </div>
            {m.targetService && (
              <p className="text-xs text-muted-foreground font-mono">{m.targetService}</p>
            )}
          </div>
        )}
      />

      <ResourceSection
        title="Provider Connections"
        items={providerConnections}
        getKey={(pc) => pc.connectionId}
        renderItem={(pc) => (
          <div className="space-y-0.5">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">{pc.providerName}</span>
              <Badge variant="outline" className="font-mono">{pc.connectionId}</Badge>
            </div>
            <p className="text-xs text-muted-foreground font-mono">{pc.baseUrl}</p>
          </div>
        )}
      />

      <ResourceSection
        title="Instruction Routes"
        items={instructionRoutes}
        getKey={(ir) => ir.instructionType}
        renderItem={(ir) => (
          <div className="flex items-center justify-between">
            <span className="text-sm font-mono">{ir.instructionType}</span>
            <Badge variant="outline" className="font-mono">{ir.connectionId}</Badge>
          </div>
        )}
      />

      <ResourceSection
        title="Inbound Routes"
        items={inboundRoutes}
        getKey={(ib, i) => `${ib.externalType}-${i}`}
        renderItem={(ib) => (
          <div className="flex items-center justify-between">
            <span className="text-sm font-mono">{ib.externalType}</span>
          </div>
        )}
      />

      <ResourceSection
        title="Seed Data"
        items={seedDataKeys}
        getKey={(key) => key}
        renderItem={(key) => (
          <div className="flex items-center justify-between">
            <span className="text-sm font-mono">{key}</span>
          </div>
        )}
      />
    </Accordion>
  )
}

import { useEffect, useState } from 'react'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { ChevronRight } from 'lucide-react'

export interface HandlerParameter {
  name: string
  type: string
  required: boolean
  enumValues: string[]
}

export interface Handler {
  name: string
  description: string
  params: HandlerParameter[]
}

export interface ServiceSchema {
  serviceName: string
  handlers: Handler[]
}

export interface HandlerSchemaResponse {
  services: ServiceSchema[]
}

export interface HandlerReferenceProps {
  filter?: string
  onInsert: (template: string) => void
  className?: string
}

export function HandlerReference({ filter = '', onInsert, className }: HandlerReferenceProps) {
  const [schema, setSchema] = useState<HandlerSchemaResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Fetch handler schema on mount
  useEffect(() => {
    const fetchSchema = async () => {
      try {
        setLoading(true)
        // TODO: Replace with actual API call via Connect-ES
        // For now, use mock data for testing
        const mockResponse: HandlerSchemaResponse = {
          services: [
            {
              serviceName: 'position_keeping',
              handlers: [
                {
                  name: 'initiate_log',
                  description: 'Initiates a position log entry',
                  params: [
                    {
                      name: 'amount',
                      type: 'Decimal',
                      required: true,
                      enumValues: [],
                    },
                    {
                      name: 'direction',
                      type: 'enum',
                      required: true,
                      enumValues: ['DEBIT', 'CREDIT'],
                    },
                  ],
                },
                {
                  name: 'finalize_log',
                  description: 'Finalizes a position log entry',
                  params: [
                    {
                      name: 'log_id',
                      type: 'string',
                      required: true,
                      enumValues: [],
                    },
                  ],
                },
              ],
            },
            {
              serviceName: 'current_account',
              handlers: [
                {
                  name: 'debit',
                  description: 'Debits an account',
                  params: [
                    {
                      name: 'account_id',
                      type: 'string',
                      required: true,
                      enumValues: [],
                    },
                    {
                      name: 'amount',
                      type: 'Decimal',
                      required: true,
                      enumValues: [],
                    },
                  ],
                },
              ],
            },
          ],
        }
        setSchema(mockResponse)
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load handler schema')
      } finally {
        setLoading(false)
      }
    }

    fetchSchema()
  }, [])

  const generateTemplate = (serviceName: string, handler: Handler): string => {
    const params = handler.params
      .map((p) => {
        const paramValue = p.type === 'enum' ? `"${p.enumValues[0] || ''}"` : '""'
        return `${p.name}=${paramValue}`
      })
      .join(', ')

    return params ? `${serviceName}.${handler.name}(${params})` : `${serviceName}.${handler.name}()`
  }

  const handleInsert = (serviceName: string, handler: Handler) => {
    const template = generateTemplate(serviceName, handler)
    onInsert(template)
  }

  const filterLowerCase = filter.toLowerCase()

  const filteredServices = schema?.services
    .map((service) => ({
      ...service,
      handlers: service.handlers.filter((handler) => {
        const serviceLowerCase = service.serviceName.toLowerCase()
        const handlerLowerCase = handler.name.toLowerCase()
        return (
          serviceLowerCase.includes(filterLowerCase) ||
          handlerLowerCase.includes(filterLowerCase)
        )
      }),
    }))
    .filter((service) => service.handlers.length > 0)

  if (loading) {
    return (
      <div
        data-testid="handler-reference"
        className={cn('flex items-center justify-center p-4 text-muted-foreground', className)}
      >
        Loading handlers...
      </div>
    )
  }

  if (error) {
    return (
      <div
        data-testid="handler-reference"
        className={cn('rounded border border-destructive/30 bg-destructive/5 p-4 text-destructive', className)}
      >
        Error: {error}
      </div>
    )
  }

  if (!filteredServices || filteredServices.length === 0) {
    return (
      <div
        data-testid="handler-reference"
        className={cn('flex items-center justify-center p-4 text-muted-foreground', className)}
      >
        No handlers found
      </div>
    )
  }

  return (
    <div
      data-testid="handler-reference"
      className={cn('flex flex-col gap-2', className)}
    >
      <Accordion type="multiple" defaultValue={filteredServices.map((_, i) => `service-${i}`)}>
        {filteredServices.map((service, serviceIndex) => (
          <AccordionItem key={serviceIndex} value={`service-${serviceIndex}`}>
            <AccordionTrigger className="py-2">
              <span className="font-medium">{service.serviceName}</span>
              <span className="ml-2 text-xs text-muted-foreground">
                {service.handlers.length}
              </span>
            </AccordionTrigger>
            <AccordionContent className="pt-2">
              <div className="space-y-3">
                {service.handlers.map((handler, handlerIndex) => (
                  <div
                    key={handlerIndex}
                    className="rounded border border-border bg-muted/50 p-3"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex-1">
                        <h4 className="font-medium text-sm">{handler.name}</h4>
                        {handler.description && (
                          <p className="text-xs text-muted-foreground mt-1">
                            {handler.description}
                          </p>
                        )}
                        {handler.params.length > 0 && (
                          <ul className="mt-2 space-y-1">
                            {handler.params.map((param, paramIndex) => (
                              <li key={paramIndex} className="text-xs text-muted-foreground ml-2">
                                <span className="font-mono">
                                  {param.name}
                                  {param.required && <span className="text-destructive">*</span>}
                                </span>
                                <span className="ml-1">({param.type})</span>
                                {param.enumValues.length > 0 && (
                                  <span className="ml-1">
                                    {param.enumValues.map((v) => `${v}`).join(' | ')}
                                  </span>
                                )}
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        onClick={() => handleInsert(service.serviceName, handler)}
                        className="shrink-0"
                        aria-label={`Insert ${handler.name}`}
                      >
                        <ChevronRight className="w-4 h-4" />
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            </AccordionContent>
          </AccordionItem>
        ))}
      </Accordion>
    </div>
  )
}

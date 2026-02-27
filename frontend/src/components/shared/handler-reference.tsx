import { useQuery } from '@tanstack/react-query'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { ChevronRight } from 'lucide-react'
import { useApiClients } from '@/api/context'

/**
 * Represents a parameter for a Starlark handler.
 */
export interface HandlerParameter {
  /** The parameter name */
  name: string
  /** The parameter type (e.g., 'Decimal', 'string', 'enum') */
  type: string
  /** Whether the parameter is required */
  required: boolean
  /** Enum values if the parameter is an enum type */
  enumValues: string[]
}

/**
 * Represents a single Starlark handler with its metadata and parameters.
 */
export interface Handler {
  /** The handler name (e.g., 'initiate_log') */
  name: string
  /** A brief description of what the handler does */
  description: string
  /** The parameters this handler accepts */
  params: HandlerParameter[]
}

/**
 * Represents a service that provides Starlark handlers.
 */
export interface ServiceSchema {
  /** The service name (e.g., 'position_keeping') */
  serviceName: string
  /** The handlers provided by this service */
  handlers: Handler[]
}

/**
 * The response structure from the handler schema API.
 */
export interface HandlerSchemaResponse {
  /** The list of services with their handlers */
  services: ServiceSchema[]
}

/**
 * Props for the HandlerReference component.
 */
export interface HandlerReferenceProps {
  /** Filter string to search handlers and services (case-insensitive) */
  filter?: string
  /** Callback invoked when user clicks insert button with Starlark call template */
  onInsert: (template: string) => void
  /** Optional CSS class names to apply to the root container */
  className?: string
}

/**
 * HandlerReference component displays available Starlark handlers organized by service.
 *
 * Features:
 * - Loads handler schema from the DescribeHandlers API
 * - Displays handlers grouped by service using accordion
 * - Filters handlers by service name or handler name (case-insensitive)
 * - Shows parameter types, required status, and enum values
 * - Generates Starlark call templates with proper parameter syntax
 *
 * @param props Component props
 * @returns React component displaying handler reference
 */
export function HandlerReference({ filter = '', onInsert, className }: HandlerReferenceProps) {
  const clients = useApiClients()

  const { data: schema, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['handlers', 'schema'],
    queryFn: async (): Promise<HandlerSchemaResponse> => {
      const response = await clients.sagaRegistry.describeHandlers({})
      return {
        services: response.services.map((s) => ({
          serviceName: s.name,
          handlers: s.handlers.map((h) => ({
            name: h.name,
            description: h.description,
            params: h.parameters.map((p) => ({
              name: p.name,
              type: p.type,
              required: p.required,
              enumValues: p.enumValues ?? [],
            })),
          })),
        })),
      }
    },
    staleTime: 5 * 60 * 1000, // Cache for 5 minutes
  })

  /**
   * Generates a Starlark call template for a handler with its parameters.
   * @param serviceName The service name (e.g., 'position_keeping')
   * @param handler The handler definition with parameters
   * @returns A Starlark function call template (e.g., 'service.handler(param1="", param2="")')
   */
  const generateTemplate = (serviceName: string, handler: Handler): string => {
    const params = handler.params
      .map((p) => {
        const paramValue = p.type === 'enum' ? `"${p.enumValues[0] || ''}"` : '""'
        return `${p.name}=${paramValue}`
      })
      .join(', ')

    return params ? `${serviceName}.${handler.name}(${params})` : `${serviceName}.${handler.name}()`
  }

  /**
   * Handles the insert button click by generating and passing the template to onInsert callback.
   * @param serviceName The service name
   * @param handler The handler to insert
   */
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

  if (isLoading) {
    return (
      <div
        data-testid="handler-reference"
        className={cn('flex items-center justify-center p-4 text-muted-foreground', className)}
      >
        Loading handlers...
      </div>
    )
  }

  if (isError) {
    return (
      <div
        data-testid="handler-reference"
        className={cn('rounded border border-destructive/30 bg-destructive/5 p-4 text-destructive', className)}
      >
        <p>Error: {error instanceof Error ? error.message : 'Failed to load handler schema'}</p>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => void refetch()}
          className="mt-2"
        >
          Retry
        </Button>
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

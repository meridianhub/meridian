import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Badge } from '@/components/ui/badge'

interface McpTool {
  name: string
  description: string
}

interface McpResource {
  uri: string
  description: string
}

const READ_TOOLS: McpTool[] = [
  { name: 'meridian_economy_structure', description: 'Returns a hierarchical summary of the tenant economy: instruments, account types, valuation rules, sagas, and payment rails.' },
  { name: 'meridian_instruments_list', description: 'Lists instrument definitions registered for the tenant. Supports optional status filter.' },
  { name: 'meridian_instrument_describe', description: 'Returns full details for a specific instrument definition.' },
  { name: 'meridian_sagas_list', description: 'Lists saga workflow definitions registered for the tenant. Supports optional status filter.' },
  { name: 'meridian_saga_describe', description: 'Returns full details for a specific saga definition including its Starlark script.' },
  { name: 'meridian_handlers_describe', description: "Returns the tenant's available saga triggers and account type policies from the current manifest." },
  { name: 'meridian_market_data_query', description: 'Lists market data sets or queries observations for a specific dataset.' },
  { name: 'meridian_manifest_history', description: 'Query manifest version history for the current tenant.' },
  { name: 'meridian_causation_tree', description: 'Fetch the full parent→child saga causation tree for a given root saga ID.' },
  { name: 'meridian_positions_query', description: 'Query financial position logs with optional account filtering.' },
  { name: 'meridian_postings_query', description: 'Query ledger postings with optional date range and account filtering.' },
  { name: 'meridian_saga_executions', description: 'Query saga definitions and their execution status.' },
  { name: 'meridian_reconciliation_status', description: 'Query reconciliation cycle status and variance mismatches.' },
]

const SIMULATE_TOOLS: McpTool[] = [
  { name: 'meridian_cel_validate', description: 'Compile and validate a CEL expression. Returns result, return type, and cost estimate.' },
  { name: 'meridian_cel_evaluate', description: 'Evaluate a CEL expression against a named environment with optional variable bindings.' },
  { name: 'meridian_starlark_validate', description: 'Validate a Starlark saga script for syntax errors.' },
  { name: 'meridian_saga_simulate', description: 'Dry-run a Starlark saga script to trace its execution steps without performing real operations.' },
  { name: 'meridian_valuation_simulate', description: 'Simulate a valuation dry-run to convert an input quantity to a valued amount.' },
  { name: 'meridian_manifest_validate', description: 'Validate a manifest YAML/JSON without applying it.' },
  { name: 'meridian_manifest_diff', description: 'Compare two tenant manifests and return a structured change summary.' },
  { name: 'meridian_manifest_plan', description: 'Dry-run a manifest apply and store the result for later application.' },
]

const WRITE_TOOLS: McpTool[] = [
  { name: 'meridian_manifest_apply', description: 'Apply a manifest that has been previously planned. Requires a valid plan hash from meridian_manifest_plan.' },
]

const RESOURCES: McpResource[] = [
  { uri: 'meridian://tenant/manifest/current', description: 'The current active manifest for the authenticated tenant.' },
  { uri: 'meridian://tenant/economy', description: 'Economy structure snapshot including instruments, sagas, and account types.' },
]

function ToolList({ tools }: { tools: McpTool[] }) {
  return (
    <ul className="space-y-3">
      {tools.map((tool) => (
        <li key={tool.name} className="flex flex-col gap-0.5">
          <span className="font-mono text-sm font-medium">{tool.name}</span>
          <span className="text-sm text-muted-foreground">{tool.description}</span>
        </li>
      ))}
    </ul>
  )
}

export function McpToolsSection() {
  const totalTools = READ_TOOLS.length + SIMULATE_TOOLS.length + WRITE_TOOLS.length

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-semibold">Available Capabilities</h2>
        <Badge variant="secondary">{totalTools} tools</Badge>
        <Badge variant="secondary">{RESOURCES.length} resources</Badge>
      </div>

      <Accordion type="multiple" className="w-full">
        <AccordionItem value="read-tools">
          <AccordionTrigger>
            <span className="flex items-center gap-2">
              Read Tools
              <Badge variant="outline">{READ_TOOLS.length}</Badge>
            </span>
          </AccordionTrigger>
          <AccordionContent>
            <ToolList tools={READ_TOOLS} />
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="simulate-tools">
          <AccordionTrigger>
            <span className="flex items-center gap-2">
              Simulate Tools
              <Badge variant="outline">{SIMULATE_TOOLS.length}</Badge>
            </span>
          </AccordionTrigger>
          <AccordionContent>
            <ToolList tools={SIMULATE_TOOLS} />
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="write-tools">
          <AccordionTrigger>
            <span className="flex items-center gap-2">
              Write Tools
              <Badge variant="outline">{WRITE_TOOLS.length}</Badge>
            </span>
          </AccordionTrigger>
          <AccordionContent>
            <ToolList tools={WRITE_TOOLS} />
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="resources">
          <AccordionTrigger>
            <span className="flex items-center gap-2">
              Resources
              <Badge variant="outline">{RESOURCES.length}</Badge>
            </span>
          </AccordionTrigger>
          <AccordionContent>
            <ul className="space-y-3">
              {RESOURCES.map((resource) => (
                <li key={resource.uri} className="flex flex-col gap-0.5">
                  <span className="font-mono text-sm font-medium">{resource.uri}</span>
                  <span className="text-sm text-muted-foreground">{resource.description}</span>
                </li>
              ))}
            </ul>
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </div>
  )
}

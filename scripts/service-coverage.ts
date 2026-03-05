/**
 * Service Coverage Mapping Script
 *
 * Scans proto definitions and frontend feature code to generate a coverage
 * report showing which gRPC RPCs have corresponding frontend client calls.
 *
 * Usage: npx tsx scripts/service-coverage.ts
 */

import * as fs from 'node:fs'
import * as path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(__dirname, '..')
const PROTO_DIR = path.join(ROOT, 'api/proto')
const FEATURES_DIR = path.join(ROOT, 'frontend/src/features')
const CLIENTS_FILE = path.join(ROOT, 'frontend/src/api/clients.ts')

// --- Types ---

interface RpcDefinition {
  service: string
  method: string
  protoFile: string
}

interface ServiceCoverage {
  service: string
  protoFile: string
  rpcs: {
    method: string
    covered: boolean
    calledFrom: string[]
  }[]
}

// --- Glob helper (recursive file search) ---

function globFiles(dir: string, pattern: RegExp): string[] {
  const results: string[] = []
  if (!fs.existsSync(dir)) return results

  function walk(current: string) {
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const fullPath = path.join(current, entry.name)
      if (entry.isDirectory()) {
        walk(fullPath)
      } else if (pattern.test(entry.name)) {
        results.push(fullPath)
      }
    }
  }
  walk(dir)
  return results
}

// --- Parse proto files for service and RPC definitions ---

function parseProtoFiles(): RpcDefinition[] {
  const protoFiles = globFiles(PROTO_DIR, /\.proto$/)
  const rpcs: RpcDefinition[] = []

  const serviceRegex = /^\s*service\s+(\w+)\s*\{/
  const rpcRegex = /^\s*rpc\s+(\w+)\s*\(/

  for (const file of protoFiles) {
    const content = fs.readFileSync(file, 'utf-8')
    const lines = content.split('\n')
    let currentService: string | null = null
    let braceDepth = 0

    for (const line of lines) {
      const serviceMatch = line.match(serviceRegex)
      if (serviceMatch) {
        currentService = serviceMatch[1]
        braceDepth = 0
      }

      if (currentService) {
        braceDepth += (line.match(/\{/g) || []).length
        braceDepth -= (line.match(/\}/g) || []).length

        const rpcMatch = line.match(rpcRegex)
        if (rpcMatch) {
          rpcs.push({
            service: currentService,
            method: rpcMatch[1],
            protoFile: path.relative(ROOT, file),
          })
        }

        if (braceDepth <= 0) {
          currentService = null
        }
      }
    }
  }

  return rpcs
}

// --- Parse clients.ts to build client property -> proto service mapping ---

function parseClientMappings(): Map<string, string> {
  const mapping = new Map<string, string>()
  if (!fs.existsSync(CLIENTS_FILE)) return mapping

  const content = fs.readFileSync(CLIENTS_FILE, 'utf-8')
  // Match: propertyName: createClient(ServiceName, transport)
  const regex = /(\w+):\s*createClient\(\s*(\w+)\s*,/g
  let match: RegExpExecArray | null
  while ((match = regex.exec(content)) !== null) {
    mapping.set(match[1], match[2])
  }
  return mapping
}

// --- Convert PascalCase RPC name to camelCase (proto -> Connect-RPC convention) ---

function toCamelCase(name: string): string {
  return name.charAt(0).toLowerCase() + name.slice(1)
}

// --- Scan frontend feature files for client method calls ---

function scanFeatureFiles(
  clientMappings: Map<string, string>,
): Map<string, Map<string, string[]>> {
  // service -> method -> [file paths]
  const calls = new Map<string, Map<string, string[]>>()
  const featureFiles = globFiles(FEATURES_DIR, /\.(ts|tsx)$/)

  const clientPropertyNames = [...clientMappings.keys()]

  for (const file of featureFiles) {
    const content = fs.readFileSync(file, 'utf-8')
    const relPath = path.relative(ROOT, file)

    // Extract destructured aliases from useApiClients() calls in this file
    const aliases = extractUseApiClientsAliases(content)

    for (const propName of clientPropertyNames) {
      const serviceName = clientMappings.get(propName)!

      // Build match roots: always match `clients.propName`, and also
      // standalone `alias` only when destructured from useApiClients()
      const roots: string[] = [`clients.${propName}`]
      for (const alias of aliases.get(propName) ?? []) {
        roots.push(alias)
      }

      for (const root of roots) {
        const pattern = new RegExp(
          `\\b${escapeRegex(root)}\\b\\.(\\w+)\\s*\\(`,
          'g',
        )
        let m: RegExpExecArray | null
        while ((m = pattern.exec(content)) !== null) {
          const methodName = m[1]
          if (!calls.has(serviceName)) {
            calls.set(serviceName, new Map())
          }
          const methods = calls.get(serviceName)!
          if (!methods.has(methodName)) {
            methods.set(methodName, [])
          }
          const files = methods.get(methodName)!
          if (!files.includes(relPath)) {
            files.push(relPath)
          }
        }
      }
    }
  }

  return calls
}

function escapeRegex(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function extractUseApiClientsAliases(content: string): Map<string, string[]> {
  const out = new Map<string, string[]>()
  const destructureRe = /const\s*\{([^}]+)\}\s*=\s*useApiClients\(\s*\)/g
  let m: RegExpExecArray | null
  while ((m = destructureRe.exec(content)) !== null) {
    for (const raw of m[1].split(',')) {
      const part = raw.trim()
      if (!part) continue
      const segments = part.split(':').map((s) => s.trim())
      const prop = segments[0]
      const alias = segments[1] || prop
      if (!out.has(prop)) out.set(prop, [])
      out.get(prop)!.push(alias)
    }
  }
  return out
}

// --- Build coverage report ---

function buildCoverageReport(
  rpcs: RpcDefinition[],
  clientMappings: Map<string, string>,
  frontendCalls: Map<string, Map<string, string[]>>,
): ServiceCoverage[] {
  // Group RPCs by service
  const serviceMap = new Map<string, RpcDefinition[]>()
  for (const rpc of rpcs) {
    if (!serviceMap.has(rpc.service)) {
      serviceMap.set(rpc.service, [])
    }
    serviceMap.get(rpc.service)!.push(rpc)
  }

  // Only include services that have a client mapping (i.e., are wired up in the frontend)
  const registeredServices = new Set(clientMappings.values())

  const report: ServiceCoverage[] = []

  for (const [service, serviceRpcs] of serviceMap) {
    const serviceCalls = frontendCalls.get(service)

    const rpcCoverage = serviceRpcs.map((rpc) => {
      const camelMethod = toCamelCase(rpc.method)
      const calledFrom = serviceCalls?.get(camelMethod) ?? []
      return {
        method: rpc.method,
        covered: calledFrom.length > 0,
        calledFrom,
      }
    })

    report.push({
      service,
      protoFile: serviceRpcs[0].protoFile,
      rpcs: rpcCoverage,
    })
  }

  // Sort: registered services first, then alphabetically
  report.sort((a, b) => {
    const aReg = registeredServices.has(a.service) ? 0 : 1
    const bReg = registeredServices.has(b.service) ? 0 : 1
    if (aReg !== bReg) return aReg - bReg
    return a.service.localeCompare(b.service)
  })

  return report
}

// --- Render markdown ---

function renderMarkdown(
  report: ServiceCoverage[],
  clientMappings: Map<string, string>,
): string {
  const registeredServices = new Set(clientMappings.values())
  const lines: string[] = []

  lines.push('# Service Coverage Report')
  lines.push('')
  lines.push(`Generated: ${new Date().toISOString().split('T')[0]}`)
  lines.push('')

  // Summary
  const registeredReport = report.filter((s) => registeredServices.has(s.service))
  const unregisteredReport = report.filter((s) => !registeredServices.has(s.service))

  const totalRpcs = registeredReport.reduce((sum, s) => sum + s.rpcs.length, 0)
  const coveredRpcs = registeredReport.reduce(
    (sum, s) => sum + s.rpcs.filter((r) => r.covered).length,
    0,
  )
  const coveragePct = totalRpcs > 0 ? ((coveredRpcs / totalRpcs) * 100).toFixed(1) : '0.0'

  lines.push('## Summary')
  lines.push('')
  lines.push(`| Metric | Value |`)
  lines.push(`|--------|-------|`)
  lines.push(`| Registered services (in clients.ts) | ${registeredReport.length} |`)
  lines.push(`| Unregistered services (no frontend client) | ${unregisteredReport.length} |`)
  lines.push(`| Total RPCs (registered services) | ${totalRpcs} |`)
  lines.push(`| Covered RPCs | ${coveredRpcs} |`)
  lines.push(`| Coverage | ${coveragePct}% |`)
  lines.push('')

  // Registered services detail
  lines.push('## Registered Services')
  lines.push('')

  for (const svc of registeredReport) {
    const covered = svc.rpcs.filter((r) => r.covered).length
    const total = svc.rpcs.length
    const pct = total > 0 ? ((covered / total) * 100).toFixed(0) : '0'
    const status = covered === total ? 'FULL' : covered > 0 ? 'PARTIAL' : 'MISSING'
    const icon = status === 'FULL' ? '[x]' : status === 'PARTIAL' ? '[-]' : '[ ]'

    lines.push(`### ${icon} ${svc.service} (${covered}/${total} - ${pct}%)`)
    lines.push('')
    lines.push(`Proto: \`${svc.protoFile}\``)
    lines.push('')
    lines.push('| RPC | Status | Called From |')
    lines.push('|-----|--------|------------|')

    for (const rpc of svc.rpcs) {
      const rpcStatus = rpc.covered ? 'covered' : 'missing'
      const sources = rpc.calledFrom.length > 0
        ? rpc.calledFrom.map((f) => `\`${f}\``).join(', ')
        : '-'
      lines.push(`| ${rpc.method} | ${rpcStatus} | ${sources} |`)
    }
    lines.push('')
  }

  // Unregistered services
  if (unregisteredReport.length > 0) {
    lines.push('## Unregistered Services (no frontend client)')
    lines.push('')
    lines.push('These services exist in proto definitions but have no client in `frontend/src/api/clients.ts`.')
    lines.push('')
    lines.push('| Service | Proto File | RPCs |')
    lines.push('|---------|-----------|------|')
    for (const svc of unregisteredReport) {
      lines.push(`| ${svc.service} | \`${svc.protoFile}\` | ${svc.rpcs.length} |`)
    }
    lines.push('')
  }

  return lines.join('\n')
}

// --- Main ---

function main() {
  const rpcs = parseProtoFiles()
  const clientMappings = parseClientMappings()
  const frontendCalls = scanFeatureFiles(clientMappings)
  const report = buildCoverageReport(rpcs, clientMappings, frontendCalls)
  const markdown = renderMarkdown(report, clientMappings)

  process.stdout.write(markdown + '\n')
}

main()

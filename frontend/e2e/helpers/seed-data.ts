import { type APIRequestContext } from '@playwright/test'

const BASE_URL = process.env.VITE_API_BASE_URL || 'http://localhost:8090'

/**
 * Create a gateway mapping via REST API.
 * Returns the created mapping (including its server-assigned ID).
 * Treats 409 (AlreadyExists) as success for idempotency.
 */
export async function createMapping(
  request: APIRequestContext,
  mapping: {
    name: string
    targetService: string
    targetRpc: string
    version?: number
    inboundValidationCel?: string
    outboundValidationCel?: string
  },
) {
  const resp = await request.post(`${BASE_URL}/v1/mappings`, {
    data: {
      name: mapping.name,
      target_service: mapping.targetService,
      target_rpc: mapping.targetRpc,
      version: mapping.version ?? 1,
      inbound_validation_cel: mapping.inboundValidationCel ?? '',
      outbound_validation_cel: mapping.outboundValidationCel ?? '',
    },
    headers: { 'x-tenant-id': 'dev_tenant' },
  })

  if (resp.status() === 409) {
    return null // already exists, idempotent
  }

  if (!resp.ok()) {
    throw new Error(`createMapping ${mapping.name} failed: ${resp.status()} ${await resp.text()}`)
  }

  return resp.json()
}

/**
 * Create a tenant via REST API.
 * Treats 409 (AlreadyExists) as success for idempotency.
 */
export async function createTenant(
  request: APIRequestContext,
  tenant: {
    tenantId: string
    displayName: string
    settlementAsset: string
    slug: string
  },
) {
  const resp = await request.post(`${BASE_URL}/v1/tenants`, {
    data: {
      tenantId: tenant.tenantId,
      displayName: tenant.displayName,
      settlementAsset: tenant.settlementAsset,
      slug: tenant.slug,
    },
  })

  if (resp.status() === 409) {
    return null // already exists, idempotent
  }

  if (!resp.ok()) {
    throw new Error(`createTenant ${tenant.tenantId} failed: ${resp.status()} ${await resp.text()}`)
  }

  return resp.json()
}

import { setupServer } from 'msw/node'
import { http, HttpResponse } from 'msw'

/**
 * MSW handlers for Connect-ES service endpoints.
 *
 * Connect-ES in JSON mode uses HTTP POST requests with the path pattern:
 *   /<package>.<ServiceName>/<MethodName>
 *
 * The wildcard handlers here catch all methods for each service, allowing
 * individual tests to override with specific handlers using server.use().
 */
export const handlers = [
  // CurrentAccountService - account lifecycle and operations
  http.post('*/meridian.current_account.v1.CurrentAccountService/*', () => {
    return HttpResponse.json({})
  }),

  // TenantService - platform tenant management
  http.post('*/meridian.tenant.v1.TenantService/*', () => {
    return HttpResponse.json({})
  }),

  // PaymentOrderService - payment order lifecycle
  http.post('*/meridian.payment_order.v1.PaymentOrderService/*', () => {
    return HttpResponse.json({})
  }),

  // SagaRegistryService - saga definition management
  http.post('*/meridian.saga.v1.SagaRegistryService/*', () => {
    return HttpResponse.json({})
  }),

  // SagaAdminService - saga execution management
  http.post('*/meridian.saga.v1.SagaAdminService/*', () => {
    return HttpResponse.json({})
  }),

  // HealthService - service health checks
  http.post('*/meridian.common.v1.HealthService/*', () => {
    return HttpResponse.json({ status: 'SERVING' })
  }),

  // AuthService - authentication
  http.post('*/meridian.control_plane.v1.AuthService/*', () => {
    return HttpResponse.json({})
  }),

  // FinancialAccountingService - booking logs and ledger postings
  http.post('*/meridian.financial_accounting.v1.FinancialAccountingService/*', () => {
    return HttpResponse.json({})
  }),

  // AuditService - audit log queries (stub: returns 501 until RPC is implemented)
  http.post('*/meridian.audit.v1.AuditService/*', () => {
    return HttpResponse.json({}, { status: 501 })
  }),

  // PositionKeepingService - financial position logs and balances
  http.post('*/meridian.position_keeping.v1.PositionKeepingService/*', () => {
    return HttpResponse.json({})
  }),

  // PartyService - party management
  http.post('*/meridian.party.v1.PartyService/*', () => {
    return HttpResponse.json({})
  }),

  // FinancialAccountingService - ledger entries
  http.post('*/meridian.financial_accounting.v1.FinancialAccountingService/*', () => {
    return HttpResponse.json({})
  }),

  // AccountReconciliationService - reconciliation
  http.post('*/meridian.reconciliation.v1.AccountReconciliationService/*', () => {
    return HttpResponse.json({})
  }),

  // ReferenceDataService - instruments and reference data
  http.post('*/meridian.reference_data.v1.ReferenceDataService/*', () => {
    return HttpResponse.json({})
  }),

  // AccountTypeRegistryService - account type definitions
  http.post('*/meridian.reference_data.v1.AccountTypeRegistryService/*', () => {
    return HttpResponse.json({})
  }),

  // NodeService - reference data nodes
  http.post('*/meridian.reference_data.v1.NodeService/*', () => {
    return HttpResponse.json({})
  }),

  // InternalAccountService - internal accounts
  http.post('*/meridian.internal_account.v1.InternalAccountService/*', () => {
    return HttpResponse.json({})
  }),

  // MarketInformationService - market data
  http.post('*/meridian.market_information.v1.MarketInformationService/*', () => {
    return HttpResponse.json({})
  }),

  // REST auth refresh endpoint used by AuthContext
  http.post('/api/auth/refresh', () => {
    return HttpResponse.json({}, { status: 401 })
  }),

  // Auth providers discovery endpoint - returns 404 by default (no external providers configured)
  http.get('/api/auth/providers', () => {
    return HttpResponse.json({}, { status: 404 })
  }),
]

export const server = setupServer(...handlers)

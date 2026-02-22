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

  // AuditService - audit log queries (stub: returns 501 until RPC is implemented)
  http.post('*/meridian.audit.v1.AuditService/*', () => {
    return HttpResponse.json({}, { status: 501 })
  }),

  // REST auth refresh endpoint used by AuthContext
  http.post('/api/auth/refresh', () => {
    return HttpResponse.json({}, { status: 401 })
  }),
]

export const server = setupServer(...handlers)

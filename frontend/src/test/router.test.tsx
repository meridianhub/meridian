import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  getRoutes,
  wrapRouteWithErrorBoundary,
  createRouteHandler,
  getRouteHandlers,
} from '@/lib/router'

// Mock api modules so router can import page components without generated proto files
vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({})),
}))

// Mock component that throws an error
function ErrorComponent() {
  throw new Error('Route error')
}

// Mock component that renders successfully
function SafeComponent() {
  return <div>Safe Route Content</div>
}

describe('Router Integration', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  describe('getRoutes', () => {
    it('returns route configuration', () => {
      const routes = getRoutes()
      expect(routes).toBeDefined()
      expect(Array.isArray(routes)).toBe(true)
    })

    it('includes dashboard route', () => {
      const routes = getRoutes()
      const dashboardRoute = routes.find((r) => r.path === '/')
      expect(dashboardRoute).toBeDefined()
      expect(dashboardRoute?.name).toBe('Dashboard')
    })
  })

  describe('wrapRouteWithErrorBoundary', () => {
    it('wraps route component with error boundary for error handling', () => {
      render(
        wrapRouteWithErrorBoundary(ErrorComponent)({} as React.ComponentProps<any>)
      )

      expect(screen.getByText('Something went wrong')).toBeInTheDocument()
    })

    it('allows safe route to render normally', () => {
      render(
        wrapRouteWithErrorBoundary(SafeComponent)({} as React.ComponentProps<any>)
      )

      expect(screen.getByText('Safe Route Content')).toBeInTheDocument()
    })
  })

  describe('createRouteHandler', () => {
    it('creates route handler with wrapped component', () => {
      const route = {
        path: '/test',
        name: 'Test',
        component: SafeComponent,
      }

      const handler = createRouteHandler(route)

      expect(handler.path).toBe('/test')
      expect(handler.name).toBe('Test')
      expect(handler.component).toBeDefined()
    })

    it('route handler component includes error boundary', () => {
      const route = {
        path: '/test',
        name: 'Test',
        component: ErrorComponent,
      }

      const handler = createRouteHandler(route)
      render(handler.component({} as React.ComponentProps<any>))

      expect(screen.getByText('Something went wrong')).toBeInTheDocument()
    })
  })

  describe('getRouteHandlers', () => {
    it('returns all routes wrapped with error boundary', () => {
      const handlers = getRouteHandlers()

      expect(handlers).toBeDefined()
      expect(Array.isArray(handlers)).toBe(true)
      expect(handlers.length).toBeGreaterThan(0)
    })

    it('all handlers have required properties', () => {
      const handlers = getRouteHandlers()

      handlers.forEach((handler) => {
        expect(handler).toHaveProperty('path')
        expect(handler).toHaveProperty('name')
        expect(handler).toHaveProperty('component')
        expect(typeof handler.path).toBe('string')
        expect(typeof handler.name).toBe('string')
        expect(typeof handler.component).toBe('function')
      })
    })
  })
})

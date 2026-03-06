import '@testing-library/jest-dom/vitest'
import { afterAll, afterEach, beforeAll, expect } from 'vitest'
import { cleanup } from '@testing-library/react'
import { toHaveNoViolations } from 'vitest-axe/matchers'
import { server } from './msw-handlers'

expect.extend({ toHaveNoViolations })

// Polyfill ResizeObserver for cmdk and other components that use it in jsdom
global.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// Polyfill scrollIntoView for cmdk keyboard navigation in jsdom
Element.prototype.scrollIntoView = function () {}

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
  sessionStorage.clear()
})
afterAll(() => server.close())

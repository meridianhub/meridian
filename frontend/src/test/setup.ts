import '@testing-library/jest-dom/vitest'
import { afterAll, afterEach, beforeAll, expect } from 'vitest'
import { cleanup } from '@testing-library/react'
import { toHaveNoViolations } from 'vitest-axe/matchers'
import { server } from './msw-handlers'

expect.extend({ toHaveNoViolations })

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
})
afterAll(() => server.close())

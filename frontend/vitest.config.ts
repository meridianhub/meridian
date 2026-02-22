import { defineConfig, type Plugin } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

/**
 * Vite plugin that redirects all imports from the src/api/gen/ directory
 * (which contains generated protobuf files that are not committed to the repo)
 * to a stub module. This prevents test failures when buf generate has not been run.
 */
function genStubPlugin(): Plugin {
  const genDir = path.resolve(__dirname, './src/api/gen')
  const stubContents = `
    export const CurrentAccountService = { typeName: 'stub', methods: [] }
    export const PaymentOrderService = { typeName: 'stub', methods: [] }
    export const FinancialAccountingService = { typeName: 'stub', methods: [] }
    export const PositionKeepingService = { typeName: 'stub', methods: [] }
    export const AccountReconciliationService = { typeName: 'stub', methods: [] }
    export const PartyService = { typeName: 'stub', methods: [] }
    export const TenantService = { typeName: 'stub', methods: [] }
    export const SagaRegistryService = { typeName: 'stub', methods: [] }
    export const SagaAdminService = { typeName: 'stub', methods: [] }
    export const ReferenceDataService = { typeName: 'stub', methods: [] }
    export const AccountTypeRegistryService = { typeName: 'stub', methods: [] }
    export const NodeService = { typeName: 'stub', methods: [] }
    export const InternalBankAccountService = { typeName: 'stub', methods: [] }
    export const MarketInformationService = { typeName: 'stub', methods: [] }
    export const ForecastingService = { typeName: 'stub', methods: [] }
    export const TransactionStatus = { UNSPECIFIED: 0, PENDING: 1, POSTED: 2, FAILED: 3, CANCELLED: 4, REVERSED: 5 }
    export const PostingDirection = { UNSPECIFIED: 0, DEBIT: 1, CREDIT: 2 }
    export const Currency = { UNSPECIFIED: 0, GBP: 1, USD: 2, EUR: 3 }
    export default {}
  `
  return {
    name: 'gen-stub',
    resolveId(source, importer) {
      // Handle @/api/gen/* imports (before alias expansion)
      if (source.startsWith('@/api/gen')) {
        return '\0gen-stub'
      }
      // Handle relative ./gen/* or ../gen/* imports from within src/api/
      if (importer && (source.startsWith('./gen/') || source.startsWith('../gen/'))) {
        const resolved = path.resolve(path.dirname(importer), source)
        if (resolved.startsWith(genDir)) {
          return '\0gen-stub'
        }
      }
      // Handle already-resolved absolute paths within src/api/gen/
      if (typeof source === 'string' && source.startsWith(genDir)) {
        return '\0gen-stub'
      }
      return null
    },
    load(id) {
      if (id === '\0gen-stub') {
        return stubContents
      }
      return null
    },
  }
}

export default defineConfig({
  plugins: [genStubPlugin(), react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    css: true,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html'],
      exclude: ['node_modules/', 'src/api/gen/', 'src/test/'],
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
})

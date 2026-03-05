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
    export const InternalAccountService = { typeName: 'stub', methods: [] }
    export const MarketInformationService = { typeName: 'stub', methods: [] }
    export const MappingService = { typeName: 'stub', methods: [] }
    export const ForecastingService = { typeName: 'stub', methods: [] }
    export const ManifestHistoryService = { typeName: 'stub', methods: [] }
    export const ApplyManifestService = { typeName: 'stub', methods: [] }
    export const AccountStatus = { UNSPECIFIED: 0, ACTIVE: 1, FROZEN: 2, CLOSED: 3 }
    export const TransactionStatus = { UNSPECIFIED: 0, PENDING: 1, POSTED: 2, FAILED: 3, CANCELLED: 4, REVERSED: 5 }
    export const PostingDirection = { UNSPECIFIED: 0, DEBIT: 1, CREDIT: 2 }
    export const Currency = { UNSPECIFIED: 0, GBP: 1, USD: 2, EUR: 3 }
    export const SagaStatus = { UNSPECIFIED: 0, DRAFT: 1, ACTIVE: 2, DEPRECATED: 3 }
    export const ErrorCategory = { UNSPECIFIED: 0, SYNTAX: 1, UNDEFINED_HANDLER: 2, TYPE_MISMATCH: 3, RUNTIME: 4, TIMEOUT: 5 }
    export const TenantStatus = { UNSPECIFIED: 0, ACTIVE: 1, SUSPENDED: 2, DEPROVISIONED: 3, PROVISIONING: 4, PROVISIONING_FAILED: 5, PROVISIONING_PENDING: 6 }
    export const ServiceProvisioningStatus_Status = { UNSPECIFIED: 0, PENDING: 1, IN_PROGRESS: 2, COMPLETED: 3, FAILED: 4 }
    export const InstrumentStatus = { UNSPECIFIED: 0, DRAFT: 1, ACTIVE: 2, DEPRECATED: 3 }
    export const Dimension = { UNSPECIFIED: 0, CURRENCY: 1, ENERGY: 2, MASS: 3, VOLUME: 4, TIME: 5, COMPUTE: 6, CARBON: 7, DATA: 8, COUNT: 9 }
    export const BehaviorClass = { UNSPECIFIED: 0, CUSTOMER: 1, CLEARING: 2, NOSTRO: 3, VOSTRO: 4, HOLDING: 5, SUSPENSE: 6, REVENUE: 7, EXPENSE: 8, INVENTORY: 9 }
    export const ApplyManifestStatus = { UNSPECIFIED: 0, DRY_RUN: 1, APPLIED: 2, VALIDATION_FAILED: 3, FAILED: 4 }
    export const ApplyStatus = { UNSPECIFIED: 0, APPLIED: 1, FAILED: 2, ROLLED_BACK: 3 }
    export const ManifestSchema = { typeName: 'meridian.control_plane.v1.Manifest', fields: [] }
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
    // Exclude Playwright E2E tests - they are run separately via `npm run e2e`
    exclude: ['node_modules/**', 'e2e/**'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html', 'json-summary'],
      exclude: ['node_modules/', 'src/api/gen/', 'src/test/', 'e2e/'],
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
})

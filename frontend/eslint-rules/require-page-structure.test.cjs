const { RuleTester } = require('eslint')
const rule = require('./require-page-structure.cjs')

const ruleTester = new RuleTester({
  languageOptions: {
    ecmaVersion: 2022,
    sourceType: 'module',
  },
})

ruleTester.run('require-page-structure', rule, {
  valid: [
    {
      code: `
        import { PageShell } from '@/shared/page-shell'
        import { PageHeader } from '@/shared/page-header'
        export default function AccountsPage() { return null }
      `,
      filename: '/app/src/features/accounts/pages/index.tsx',
    },
    {
      code: `
        import { PageShell, PageHeader } from '@/shared'
        export default function AccountsPage() { return null }
      `,
      filename: '/app/src/features/accounts/pages/index.tsx',
    },
    {
      // Non-feature file should be ignored
      code: `export default function App() { return null }`,
      filename: '/app/src/components/App.tsx',
    },
    {
      // Dashboard page is excluded
      code: `export default function Dashboard() { return null }`,
      filename: '/app/src/features/dashboard/pages/index.tsx',
    },
    {
      // Test file is excluded
      code: `import { render } from '@testing-library/react'`,
      filename: '/app/src/features/accounts/pages/index.test.tsx',
    },
    {
      // Dialog file is excluded
      code: `export function CreateDialog() { return null }`,
      filename: '/app/src/features/accounts/pages/create-dialog.tsx',
    },
    {
      // Economy page is excluded
      code: `export default function EconomyPage() { return null }`,
      filename: '/app/src/features/economy/pages/economy-overview-page.tsx',
    },
    {
      // .ts utility files are excluded
      code: `export const COLUMNS = ['name', 'status']`,
      filename: '/app/src/features/accounts/pages/types.ts',
    },
    {
      // Tab component files are excluded
      code: `export function OverviewTab() { return null }`,
      filename: '/app/src/features/parties/pages/tabs/overview-tab.tsx',
    },
  ],
  invalid: [
    {
      code: `
        import { DataTable } from '@/shared/data-table'
        export default function AccountsPage() { return null }
      `,
      filename: '/app/src/features/accounts/pages/index.tsx',
      errors: [
        { message: 'Feature pages must import PageShell from @/shared' },
        { message: 'Feature pages must import PageHeader from @/shared' },
      ],
    },
    {
      code: `
        import { PageShell } from '@/shared/page-shell'
        export default function AccountsPage() { return null }
      `,
      filename: '/app/src/features/accounts/pages/index.tsx',
      errors: [{ message: 'Feature pages must import PageHeader from @/shared' }],
    },
    {
      code: `
        import { PageHeader } from '@/shared/page-header'
        export default function AccountsPage() { return null }
      `,
      filename: '/app/src/features/accounts/pages/index.tsx',
      errors: [{ message: 'Feature pages must import PageShell from @/shared' }],
    },
  ],
})

console.log('All tests passed!')

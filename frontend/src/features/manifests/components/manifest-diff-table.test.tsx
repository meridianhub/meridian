import { describe, it, expect } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ManifestDiffTable, type ManifestDiffTableProps } from './manifest-diff-table'

const mockActions = [
  {
    resourceType: 'instrument',
    resourceCode: 'GBP',
    action: 'CREATE',
    description: 'New instrument added',
    breaking: false,
  },
  {
    resourceType: 'instrument',
    resourceCode: 'USD',
    action: 'NO_CHANGE',
    description: '',
    breaking: false,
  },
  {
    resourceType: 'account_type',
    resourceCode: 'SAVINGS',
    action: 'UPDATE',
    description: 'Updated allowed instruments',
    breaking: false,
  },
  {
    resourceType: 'saga',
    resourceCode: 'settle_payment',
    action: 'DELETE',
    description: 'Saga removed',
    breaking: true,
  },
] as unknown as ManifestDiffTableProps['actions']

const mockSummary = {
  totalActions: 4,
  creates: 1,
  updates: 1,
  deletes: 1,
  noChanges: 1,
  hasBreakingChanges: true,
}

function renderComponent(props: Partial<ManifestDiffTableProps> = {}) {
  return renderWithProviders(
    <ManifestDiffTable actions={mockActions} summary={mockSummary} {...props} />,
    { initialToken: createTenantUserToken() },
  )
}

describe('ManifestDiffTable', () => {
  it('renders all diff rows', () => {
    renderComponent()

    expect(screen.getByTestId('diff-row-GBP')).toBeInTheDocument()
    expect(screen.getByTestId('diff-row-USD')).toBeInTheDocument()
    expect(screen.getByTestId('diff-row-SAVINGS')).toBeInTheDocument()
    expect(screen.getByTestId('diff-row-settle_payment')).toBeInTheDocument()
  })

  it('renders summary statistics', () => {
    renderComponent()

    const summary = screen.getByTestId('diff-summary')
    expect(within(summary).getByText(/Total:/)).toBeInTheDocument()
    expect(within(summary).getByText('+1 added')).toBeInTheDocument()
    expect(within(summary).getByText('~1 modified')).toBeInTheDocument()
    expect(within(summary).getByText('-1 removed')).toBeInTheDocument()
    expect(within(summary).getByText('1 unchanged')).toBeInTheDocument()
  })

  it('shows breaking changes warning', () => {
    renderComponent()

    expect(screen.getByTestId('breaking-warning')).toBeInTheDocument()
    expect(screen.getByText('Breaking changes detected')).toBeInTheDocument()
  })

  it('does not show breaking warning when no breaking changes', () => {
    renderComponent({
      summary: { ...mockSummary, hasBreakingChanges: false },
    })

    expect(screen.queryByTestId('breaking-warning')).not.toBeInTheDocument()
  })

  it('renders change type badges in table rows', () => {
    renderComponent()

    const gbpRow = screen.getByTestId('diff-row-GBP')
    expect(within(gbpRow).getByText('Added')).toBeInTheDocument()

    const savingsRow = screen.getByTestId('diff-row-SAVINGS')
    expect(within(savingsRow).getByText('Modified')).toBeInTheDocument()

    const sagaRow = screen.getByTestId('diff-row-settle_payment')
    expect(within(sagaRow).getByText('Removed')).toBeInTheDocument()

    const usdRow = screen.getByTestId('diff-row-USD')
    expect(within(usdRow).getByText('Unchanged')).toBeInTheDocument()
  })

  it('renders resource codes', () => {
    renderComponent()

    expect(screen.getByText('GBP')).toBeInTheDocument()
    expect(screen.getByText('USD')).toBeInTheDocument()
    expect(screen.getByText('SAVINGS')).toBeInTheDocument()
    expect(screen.getByText('settle_payment')).toBeInTheDocument()
  })

  it('marks breaking changes with Yes', () => {
    renderComponent()

    const sagaRow = screen.getByTestId('diff-row-settle_payment')
    expect(within(sagaRow).getByText('Yes')).toBeInTheDocument()
  })

  it('shows empty state when no actions', () => {
    renderComponent({ actions: [] as unknown as ManifestDiffTableProps['actions'] })

    expect(screen.getByTestId('empty-state')).toBeInTheDocument()
  })

  it('filters by resource type', async () => {
    renderComponent()
    const user = userEvent.setup()

    await user.selectOptions(screen.getByTestId('resource-type-filter'), 'saga')

    expect(screen.getByTestId('diff-row-settle_payment')).toBeInTheDocument()
    expect(screen.queryByTestId('diff-row-GBP')).not.toBeInTheDocument()
    expect(screen.queryByTestId('diff-row-SAVINGS')).not.toBeInTheDocument()
  })

  it('filters by change type', async () => {
    renderComponent()
    const user = userEvent.setup()

    await user.selectOptions(screen.getByTestId('change-type-filter'), 'CREATE')

    expect(screen.getByTestId('diff-row-GBP')).toBeInTheDocument()
    expect(screen.queryByTestId('diff-row-USD')).not.toBeInTheDocument()
    expect(screen.queryByTestId('diff-row-SAVINGS')).not.toBeInTheDocument()
  })

  it('renders without summary', () => {
    renderComponent({ summary: undefined })

    expect(screen.queryByTestId('diff-summary')).not.toBeInTheDocument()
    expect(screen.getByTestId('manifest-diff-table')).toBeInTheDocument()
  })
})

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EventChainPanel } from './event-chain-panel'
import type { EventChain, EventHop } from '../lib/transitive-closure'

function makeHop(overrides: Partial<EventHop> = {}): EventHop {
  return {
    depth: 1,
    trigger: {
      channel: 'position-keeping.transaction-captured.v1',
      instrumentCode: 'GBP',
      accountId: null,
      direction: null,
    },
    saga: 'process-payment',
    filterExpression: 'event.instrumentCode == "GBP"',
    filterResult: 'pass',
    filterReason: 'Instrument matches literal',
    producedEvents: [
      {
        channel: 'position-keeping.transaction-captured.v1',
        instrumentCode: 'USD',
        accountId: 'fees',
        direction: 'DEBIT',
      },
    ],
    ...overrides,
  }
}

function makeChain(overrides: Partial<EventChain> = {}): EventChain {
  return {
    hops: [makeHop()],
    terminationReason: 'no_matching_sagas',
    maxDepthUsed: 1,
    ...overrides,
  }
}

describe('EventChainPanel', () => {
  it('renders multi-hop chain with depth indicators', () => {
    const chain = makeChain({
      hops: [
        makeHop({ depth: 1, saga: 'saga-a' }),
        makeHop({ depth: 2, saga: 'saga-b' }),
      ],
      maxDepthUsed: 2,
    })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP Instrument" />)

    expect(screen.getByText('Event chain from GBP Instrument')).toBeDefined()
    expect(screen.getByText('Depth: 2')).toBeDefined()
    expect(screen.getByText('Hop 1')).toBeDefined()
    expect(screen.getByText('Hop 2')).toBeDefined()
  })

  it('renders accordion items that expand on click', async () => {
    const user = userEvent.setup()
    const chain = makeChain()

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    // Accordion content should not be visible initially
    expect(screen.queryByText('Trigger:')).toBeNull()

    // Click the accordion trigger to expand
    const trigger = screen.getByText('process-payment')
    await user.click(trigger.closest('[data-slot="accordion-trigger"]')!)

    expect(screen.getByText('Trigger:')).toBeDefined()
    expect(screen.getByText('Reason:')).toBeDefined()
  })

  it('shows pass filter badge with green styling', () => {
    const chain = makeChain({
      hops: [makeHop({ filterResult: 'pass' })],
    })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    const badge = screen.getByTestId('filter-badge-pass')
    expect(badge.textContent).toBe('pass')
    expect(badge.className).toContain('bg-emerald-100')
  })

  it('shows fail filter badge with red styling', () => {
    const chain = makeChain({
      hops: [makeHop({ filterResult: 'fail' })],
    })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    const badge = screen.getByTestId('filter-badge-fail')
    expect(badge.textContent).toBe('fail')
    expect(badge.className).toContain('bg-red-100')
  })

  it('shows indeterminate filter badge with amber styling', () => {
    const chain = makeChain({
      hops: [makeHop({ filterResult: 'indeterminate' })],
    })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    const badge = screen.getByTestId('filter-badge-indeterminate')
    expect(badge.textContent).toBe('indeterminate')
    expect(badge.className).toContain('bg-amber-100')
  })

  it('displays termination reason for filter_rejection', () => {
    const chain = makeChain({ terminationReason: 'filter_rejection' })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    const reason = screen.getByTestId('termination-reason')
    expect(reason.textContent).toContain('all sagas filtered out')
  })

  it('displays termination reason for chain_depth_limit', () => {
    const chain = makeChain({ terminationReason: 'chain_depth_limit' })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    const reason = screen.getByTestId('termination-reason')
    expect(reason.textContent).toContain('maximum depth reached')
  })

  it('displays termination reason for no_matching_sagas', () => {
    const chain = makeChain({ terminationReason: 'no_matching_sagas' })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    const reason = screen.getByTestId('termination-reason')
    expect(reason.textContent).toContain('no matching sagas found')
  })

  it('fires onSagaClick when saga name is clicked', async () => {
    const user = userEvent.setup()
    const onSagaClick = vi.fn()
    const chain = makeChain({
      hops: [makeHop({ saga: 'my-saga' })],
    })

    render(
      <EventChainPanel
        chain={chain}
        startNodeLabel="GBP"
        onSagaClick={onSagaClick}
      />,
    )

    await user.click(screen.getByTestId('saga-link-my-saga'))
    expect(onSagaClick).toHaveBeenCalledWith('my-saga')
  })

  it('renders empty chain with message', () => {
    const chain = makeChain({ hops: [], maxDepthUsed: 0 })

    render(<EventChainPanel chain={chain} startNodeLabel="kWh" />)

    expect(screen.getByText('No event chain from kWh.')).toBeDefined()
  })

  it('shows produced events in expanded accordion', async () => {
    const user = userEvent.setup()
    const chain = makeChain({
      hops: [
        makeHop({
          producedEvents: [
            {
              channel: 'position-keeping.transaction-captured.v1',
              instrumentCode: 'USD',
              accountId: null,
              direction: 'CREDIT',
            },
          ],
        }),
      ],
    })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    // Expand accordion
    const trigger = screen.getByText('process-payment')
    await user.click(trigger.closest('[data-slot="accordion-trigger"]')!)

    expect(screen.getByText('Produced events:')).toBeDefined()
    expect(screen.getByText(/\[USD\].*\(CREDIT\)/)).toBeDefined()
  })

  it('shows saga diagram toggle button', async () => {
    const user = userEvent.setup()
    const chain = makeChain({
      hops: [makeHop({ saga: 'test-saga' })],
    })

    render(<EventChainPanel chain={chain} startNodeLabel="GBP" />)

    // Expand accordion first
    const trigger = screen.getByText('test-saga')
    await user.click(trigger.closest('[data-slot="accordion-trigger"]')!)

    const toggleBtn = screen.getByTestId('saga-diagram-toggle-test-saga')
    expect(toggleBtn.textContent).toBe('Show saga flow')

    await user.click(toggleBtn)
    expect(screen.getByTestId('saga-diagram-test-saga')).toBeDefined()
    expect(toggleBtn.textContent).toBe('Hide saga flow')
  })
})

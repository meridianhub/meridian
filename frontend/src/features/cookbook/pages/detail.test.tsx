import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { CookbookDetailPage } from './detail'
import type { CookbookItem, PatternMeta } from '../hooks/use-cookbook'

// Mock CodeMirror (jsdom doesn't support it)
vi.mock('codemirror', () => ({ basicSetup: [] }))
vi.mock('@codemirror/view', () => ({
  EditorView: class MockEditorView {
    static editable = { of: vi.fn(() => ({})) }
    static updateListener = { of: vi.fn(() => ({})) }
    dom: HTMLElement
    state: { doc: { toString: () => string } }
    dispatch = vi.fn()
    constructor(config: { doc?: string; extensions?: unknown[]; parent?: HTMLElement }) {
      this.dom = document.createElement('div')
      this.dom.className = 'cm-editor'
      this.state = { doc: { toString: () => config.doc ?? '' } }
      if (config.parent) config.parent.appendChild(this.dom)
    }
    destroy() {}
  },
}))
vi.mock('@codemirror/state', () => ({
  Compartment: class { of = vi.fn(() => ({})); reconfigure = vi.fn(() => ({})) },
  EditorState: { create: vi.fn(() => ({})), readOnly: { of: vi.fn(() => ({})) } },
  Transaction: { userEvent: 'user-event' },
}))
vi.mock('@codemirror/lang-python', () => ({ python: vi.fn(() => ({})) }))
vi.mock('@codemirror/lint', () => ({
  linter: vi.fn(() => ({})),
  lintGutter: vi.fn(() => ({})),
}))

const mockUseCookbook = vi.fn<() => { items: CookbookItem[]; isLoading: boolean }>()
vi.mock('../hooks/use-cookbook', () => ({
  useCookbook: () => mockUseCookbook(),
}))

const mockUsePatternFiles = vi.fn<() => { starlarkContent: string | null; manifestContent: string | null; isLoading: boolean }>()
vi.mock('../hooks/use-pattern-files', () => ({
  usePatternFiles: () => mockUsePatternFiles(),
}))

function renderDetail(name: string) {
  return render(
    <MemoryRouter initialEntries={[`/cookbook/${name}`]}>
      <Routes>
        <Route path="/cookbook/:name" element={<CookbookDetailPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

const patternItem: CookbookItem = {
  name: 'fiat-current-account',
  type: 'registry:pattern',
  title: 'Fiat Current Account',
  description: 'Standard fiat current account pattern for retail banking.',
  categories: ['banking', 'retail'],
  meta: {
    complexity: 3,
    design_pattern: 'Double-Entry Ledger',
    industries: ['banking'],
    composes_with: ['overdraft-facility'],
    extends: [],
    conflicts_with: ['crypto-wallet'],
    provides: { instruments: ['GBP'], sagas: ['deposit'] },
    requires: { instruments: ['GBP'] },
  } satisfies PatternMeta,
}

const uiItem: CookbookItem = {
  name: 'transaction-table',
  type: 'registry:ui',
  title: 'Transaction Table',
  description: 'Reusable transaction data table.',
}

describe('CookbookDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseCookbook.mockReturnValue({ items: [patternItem, uiItem], isLoading: false })
    mockUsePatternFiles.mockReturnValue({ starlarkContent: null, manifestContent: null, isLoading: false })
  })

  it('shows loading skeleton while catalogue is loading', () => {
    mockUseCookbook.mockReturnValue({ items: [], isLoading: true })
    renderDetail('fiat-current-account')
    expect(screen.getByTestId('detail-skeleton')).toBeInTheDocument()
  })

  it('shows not-found message for unknown pattern', () => {
    renderDetail('nonexistent')
    expect(screen.getByText(/not found/i)).toBeInTheDocument()
  })

  it('renders pattern title and description', () => {
    renderDetail('fiat-current-account')
    expect(screen.getByRole('heading', { name: 'Fiat Current Account' })).toBeInTheDocument()
    expect(screen.getByText(/Standard fiat current account/)).toBeInTheDocument()
  })

  it('renders type badge for pattern', () => {
    renderDetail('fiat-current-account')
    expect(screen.getByText('Pattern')).toBeInTheDocument()
  })

  it('renders complexity indicator', () => {
    renderDetail('fiat-current-account')
    expect(screen.getByText(/Complexity: 3/)).toBeInTheDocument()
  })

  it('renders categories as badges', () => {
    renderDetail('fiat-current-account')
    expect(screen.getByText('retail')).toBeInTheDocument()
    // 'banking' appears in both categories and industries
    expect(screen.getAllByText('banking').length).toBeGreaterThanOrEqual(1)
  })

  it('renders tabs for pattern type', () => {
    renderDetail('fiat-current-account')
    expect(screen.getByRole('tab', { name: 'Manifest' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Starlark' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Composition' })).toBeInTheDocument()
  })

  it('shows placeholder for UI component type', () => {
    renderDetail('transaction-table')
    expect(screen.getByText(/UI component preview/)).toBeInTheDocument()
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
  })

  it('renders breadcrumb navigation', () => {
    renderDetail('fiat-current-account')
    const breadcrumb = screen.getByLabelText('Breadcrumb')
    expect(breadcrumb).toBeInTheDocument()
    expect(screen.getByText('Cookbook')).toBeInTheDocument()
    // Title appears in both breadcrumb and heading
    expect(screen.getAllByText('Fiat Current Account').length).toBe(2)
  })

  it('shows no manifest message when file not found', () => {
    renderDetail('fiat-current-account')
    expect(screen.getByText(/No manifest file found/)).toBeInTheDocument()
  })

  it('renders manifest viewer when content available', () => {
    mockUsePatternFiles.mockReturnValue({
      starlarkContent: null,
      manifestContent: 'name: test\ntype: registry:pattern',
      isLoading: false,
    })
    renderDetail('fiat-current-account')
    expect(screen.getByTestId('manifest-viewer')).toBeInTheDocument()
  })
})

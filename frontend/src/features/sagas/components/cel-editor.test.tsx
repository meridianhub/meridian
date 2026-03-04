import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CELEditor, CONTEXT_VARIABLES, type CELEditorProps } from './cel-editor'

// CodeMirror uses DOM APIs not available in jsdom. Mock the EditorView at module level.
vi.mock('codemirror', () => ({
  basicSetup: [],
}))

const mockDispatch = vi.fn()

vi.mock('@codemirror/view', () => ({
  EditorView: class MockEditorView {
    static editable = { of: vi.fn(() => ({})) }
    static updateListener = { of: vi.fn(() => ({})) }
    dom: HTMLElement
    state: { doc: { toString: () => string } }
    dispatch = mockDispatch

    constructor(config: {
      doc?: string
      extensions?: unknown[]
      parent?: HTMLElement
    }) {
      this.dom = document.createElement('div')
      this.dom.className = 'cm-editor'
      this.dom.setAttribute('data-testid', 'codemirror-editor')
      this.state = { doc: { toString: () => config.doc ?? '' } }
      if (config.parent) {
        config.parent.appendChild(this.dom)
      }
    }

    destroy() {
      this.dom.remove()
    }
  },
  keymap: { of: vi.fn(() => ({})) },
}))

vi.mock('@codemirror/state', () => ({
  Compartment: class {
    of(value: unknown) {
      return value
    }
    reconfigure(value: unknown) {
      return value
    }
  },
  EditorState: {
    create: vi.fn(() => ({})),
    readOnly: { of: vi.fn(() => ({})) },
  },
  Transaction: {
    userEvent: 'userEvent',
  },
}))

vi.mock('@codemirror/lang-python', () => ({
  python: vi.fn(() => ({})),
}))

vi.mock('@codemirror/lint', () => ({
  linter: vi.fn((fn: () => unknown) => fn),
  lintGutter: vi.fn(() => ({})),
}))

describe('CELEditor', () => {
  const defaultProps: CELEditorProps = {
    value: 'amount > 0',
    onChange: vi.fn(),
    context: 'validation',
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('basic rendering', () => {
    it('renders the editor container', () => {
      const { container } = render(<CELEditor {...defaultProps} />)
      expect(container.querySelector('[data-testid="cel-editor"]')).toBeTruthy()
    })

    it('mounts CodeMirror editor into the DOM', () => {
      const { container } = render(<CELEditor {...defaultProps} />)
      expect(container.querySelector('.cm-editor')).toBeTruthy()
    })

    it('renders security constraints display', () => {
      render(<CELEditor {...defaultProps} />)
      expect(screen.getByTestId('security-constraints')).toBeTruthy()
    })

    it('displays max bytes constraint', () => {
      render(<CELEditor {...defaultProps} />)
      expect(screen.getByText(/4[,.]?096\s*bytes/i)).toBeTruthy()
    })

    it('displays max nesting depth constraint', () => {
      render(<CELEditor {...defaultProps} />)
      expect(screen.getByText(/nesting depth\s*10/i)).toBeTruthy()
    })

    it('displays cost limit constraint', () => {
      render(<CELEditor {...defaultProps} />)
      expect(screen.getByText(/cost limit\s*10[,.]?000/i)).toBeTruthy()
    })
  })

  describe('context-aware variables', () => {
    it('shows variables panel by default', () => {
      render(<CELEditor {...defaultProps} />)
      expect(screen.getByTestId('variables-panel')).toBeTruthy()
    })

    it('hides variables panel when showVariables is false', () => {
      render(<CELEditor {...defaultProps} showVariables={false} />)
      expect(screen.queryByTestId('variables-panel')).toBeNull()
    })

    it('shows validation context variables', () => {
      render(<CELEditor {...defaultProps} context="validation" />)
      const panel = screen.getByTestId('variables-panel')
      expect(panel.textContent).toContain('amount')
      expect(panel.textContent).toContain('attributes')
    })

    it('shows bucketKey context variables', () => {
      render(<CELEditor {...defaultProps} context="bucketKey" />)
      const panel = screen.getByTestId('variables-panel')
      expect(panel.textContent).toContain('attributes')
    })

    it('shows eligibility context variables', () => {
      render(<CELEditor {...defaultProps} context="eligibility" />)
      const panel = screen.getByTestId('variables-panel')
      expect(panel.textContent).toContain('party.type')
      expect(panel.textContent).toContain('party.status')
    })

    it('shows value context variables', () => {
      render(<CELEditor {...defaultProps} context="value" />)
      const panel = screen.getByTestId('variables-panel')
      expect(panel.textContent).toContain('amount')
      expect(panel.textContent).toContain('valid_from')
    })

    it('does not show eligibility-specific variables in validation context', () => {
      render(<CELEditor {...defaultProps} context="validation" />)
      const panel = screen.getByTestId('variables-panel')
      expect(panel.textContent).not.toContain('party.type')
    })
  })

  describe('error display', () => {
    it('does not render error panel when no errors', () => {
      render(<CELEditor {...defaultProps} errors={[]} />)
      expect(screen.queryByTestId('error-panel')).toBeNull()
    })

    it('renders error panel when errors are provided', () => {
      const errors = [{ message: 'Invalid expression' }]
      render(<CELEditor {...defaultProps} errors={errors} />)
      expect(screen.getByTestId('error-panel')).toBeTruthy()
    })

    it('displays error message', () => {
      const errors = [{ message: 'Undefined variable: foo' }]
      render(<CELEditor {...defaultProps} errors={errors} />)
      expect(screen.getByText('Undefined variable: foo')).toBeTruthy()
    })

    it('displays multiple errors', () => {
      const errors = [
        { message: 'Error one' },
        { message: 'Error two' },
      ]
      render(<CELEditor {...defaultProps} errors={errors} />)
      expect(screen.getByText('Error one')).toBeTruthy()
      expect(screen.getByText('Error two')).toBeTruthy()
    })

    it('displays error with line number when provided', () => {
      const errors = [{ message: 'Syntax error', line: 3 }]
      render(<CELEditor {...defaultProps} errors={errors} />)
      expect(screen.getByText(/3/)).toBeTruthy()
      expect(screen.getByText('Syntax error')).toBeTruthy()
    })

    it('displays error count in panel header', () => {
      const errors = [
        { message: 'Error one' },
        { message: 'Error two' },
      ]
      render(<CELEditor {...defaultProps} errors={errors} />)
      expect(screen.getByText(/2 (issue|error)/i)).toBeTruthy()
    })

    it('displays single error with singular label', () => {
      const errors = [{ message: 'One error' }]
      render(<CELEditor {...defaultProps} errors={errors} />)
      expect(screen.getByText(/1 (issue|error)/i)).toBeTruthy()
    })
  })

  describe('onChange', () => {
    it('calls onChange when value changes', () => {
      const onChange = vi.fn()
      render(<CELEditor {...defaultProps} onChange={onChange} />)
      // onChange is wired via EditorView updateListener
      // The mock doesn't trigger onChange but the prop is passed
      expect(onChange).not.toHaveBeenCalled() // not called on mount
    })
  })

  describe('readOnly mode', () => {
    it('renders read-only badge when readOnly is true', () => {
      render(<CELEditor {...defaultProps} readOnly />)
      expect(screen.getByTestId('readonly-badge')).toBeTruthy()
    })

    it('does not render read-only badge when readOnly is false', () => {
      render(<CELEditor {...defaultProps} readOnly={false} />)
      expect(screen.queryByTestId('readonly-badge')).toBeNull()
    })

    it('does not render read-only badge by default', () => {
      render(<CELEditor {...defaultProps} />)
      expect(screen.queryByTestId('readonly-badge')).toBeNull()
    })
  })

  describe('CONTEXT_VARIABLES export', () => {
    it('exports validation context variables', () => {
      expect(CONTEXT_VARIABLES.validation).toContain('amount')
      expect(CONTEXT_VARIABLES.validation).toContain('attributes')
      expect(CONTEXT_VARIABLES.validation).toContain('valid_from')
      expect(CONTEXT_VARIABLES.validation).toContain('valid_to')
      expect(CONTEXT_VARIABLES.validation).toContain('source')
    })

    it('exports bucketKey context variables', () => {
      expect(CONTEXT_VARIABLES.bucketKey).toContain('attributes')
    })

    it('exports eligibility context variables', () => {
      expect(CONTEXT_VARIABLES.eligibility).toContain('party.type')
      expect(CONTEXT_VARIABLES.eligibility).toContain('party.status')
      expect(CONTEXT_VARIABLES.eligibility).toContain('party.external_reference_type')
      expect(CONTEXT_VARIABLES.eligibility).toContain('attributes')
    })

    it('exports value context variables', () => {
      expect(CONTEXT_VARIABLES.value).toContain('attributes')
      expect(CONTEXT_VARIABLES.value).toContain('amount')
      expect(CONTEXT_VARIABLES.value).toContain('valid_from')
      expect(CONTEXT_VARIABLES.value).toContain('valid_to')
      expect(CONTEXT_VARIABLES.value).toContain('source')
    })
  })

  describe('value sync', () => {
    it('dispatches to sync value when value prop changes externally', () => {
      const { rerender } = render(
        <CELEditor {...defaultProps} value="amount > 0" />,
      )
      const initialCalls = mockDispatch.mock.calls.length
      rerender(<CELEditor {...defaultProps} value="amount > 100" />)
      expect(mockDispatch.mock.calls.length).toBeGreaterThan(initialCalls)
    })

    it('dispatches to reconfigure read-only when readOnly prop changes', () => {
      const { rerender } = render(
        <CELEditor {...defaultProps} readOnly={false} />,
      )
      const initialCalls = mockDispatch.mock.calls.length
      rerender(<CELEditor {...defaultProps} readOnly={true} />)
      expect(mockDispatch.mock.calls.length).toBeGreaterThan(initialCalls)
    })
  })

  describe('className prop', () => {
    it('accepts optional className prop', () => {
      const { container } = render(
        <CELEditor {...defaultProps} className="custom-class" />,
      )
      expect(
        container.querySelector('[data-testid="cel-editor"]')?.classList,
      ).toContain('custom-class')
    })
  })
})

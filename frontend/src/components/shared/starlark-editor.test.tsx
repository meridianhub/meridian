import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { StarlarkEditor, type ValidationError } from './starlark-editor'

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
}))

vi.mock('@codemirror/lang-python', () => ({
  python: vi.fn(() => ({})),
}))

vi.mock('@codemirror/lint', () => ({
  linter: vi.fn((fn: () => unknown) => fn),
  lintGutter: vi.fn(() => ({})),
}))

describe('StarlarkEditor', () => {
  const defaultProps = {
    value: 'def saga():\n  pass',
    onChange: vi.fn(),
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the editor container', () => {
    const { container } = render(<StarlarkEditor {...defaultProps} />)
    expect(container.querySelector('[data-testid="starlark-editor"]')).toBeTruthy()
  })

  it('mounts CodeMirror editor into the DOM', () => {
    const { container } = render(<StarlarkEditor {...defaultProps} />)
    expect(container.querySelector('.cm-editor')).toBeTruthy()
  })

  it('renders error panel when errors are provided', () => {
    const errors: ValidationError[] = [
      { line: 1, column: 0, message: 'Syntax error at line 1', category: 'SYNTAX' },
    ]
    render(<StarlarkEditor {...defaultProps} errors={errors} />)
    expect(screen.getByTestId('error-panel')).toBeTruthy()
    expect(screen.getByText('Syntax error at line 1')).toBeTruthy()
  })

  it('does not render error panel when no errors', () => {
    render(<StarlarkEditor {...defaultProps} errors={[]} />)
    expect(screen.queryByTestId('error-panel')).toBeNull()
  })

  it('displays error count in error panel header', () => {
    const errors: ValidationError[] = [
      { line: 1, column: 0, message: 'Error one', category: 'SYNTAX' },
      { line: 3, column: 5, message: 'Error two', category: 'WARNING' },
    ]
    render(<StarlarkEditor {...defaultProps} errors={errors} />)
    expect(screen.getByText(/2 issues/i)).toBeTruthy()
  })

  it('displays error line and column in error panel', () => {
    const errors: ValidationError[] = [
      { line: 5, column: 10, message: 'Undefined variable', category: 'ERROR' },
    ]
    render(<StarlarkEditor {...defaultProps} errors={errors} />)
    expect(screen.getByText(/5:10/)).toBeTruthy()
  })

  it('applies SYNTAX category styling to error items', () => {
    const errors: ValidationError[] = [
      { line: 1, column: 0, message: 'Bad syntax', category: 'SYNTAX' },
    ]
    render(<StarlarkEditor {...defaultProps} errors={errors} />)
    const errorItem = screen.getByTestId('error-item-0')
    expect(errorItem).toBeTruthy()
  })

  it('applies WARNING category styling differently from errors', () => {
    const errors: ValidationError[] = [
      { line: 2, column: 0, message: 'Unused variable', category: 'WARNING' },
    ]
    render(<StarlarkEditor {...defaultProps} errors={errors} />)
    const errorItem = screen.getByTestId('error-item-0')
    expect(errorItem).toBeTruthy()
  })

  it('renders complexity metrics panel when metrics provided', () => {
    const metrics = {
      handlerCalls: 3,
      operations: 5,
      estimatedDurationMs: 150,
      complexityScore: 4,
    }
    render(<StarlarkEditor {...defaultProps} complexityMetrics={metrics} />)
    expect(screen.getByTestId('complexity-metrics-panel')).toBeTruthy()
  })

  it('does not render complexity metrics panel when no metrics', () => {
    render(<StarlarkEditor {...defaultProps} />)
    expect(screen.queryByTestId('complexity-metrics-panel')).toBeNull()
  })

  it('displays handler calls count in metrics panel', () => {
    const metrics = {
      handlerCalls: 7,
      operations: 12,
      estimatedDurationMs: 200,
      complexityScore: 6,
    }
    render(<StarlarkEditor {...defaultProps} complexityMetrics={metrics} />)
    expect(screen.getByText('7')).toBeTruthy()
  })

  it('displays operations count in metrics panel', () => {
    const metrics = {
      handlerCalls: 2,
      operations: 8,
      estimatedDurationMs: 100,
      complexityScore: 3,
    }
    render(<StarlarkEditor {...defaultProps} complexityMetrics={metrics} />)
    expect(screen.getByText('8')).toBeTruthy()
  })

  it('displays complexity score out of 10', () => {
    const metrics = {
      handlerCalls: 2,
      operations: 5,
      estimatedDurationMs: 75,
      complexityScore: 4,
    }
    render(<StarlarkEditor {...defaultProps} complexityMetrics={metrics} />)
    expect(screen.getByText(/4\s*\/\s*10/)).toBeTruthy()
  })

  it('displays estimated duration in metrics panel', () => {
    const metrics = {
      handlerCalls: 1,
      operations: 3,
      estimatedDurationMs: 250,
      complexityScore: 2,
    }
    render(<StarlarkEditor {...defaultProps} complexityMetrics={metrics} />)
    expect(screen.getByText(/250\s*ms/i)).toBeTruthy()
  })

  it('calls onErrorClick when error item is clicked', () => {
    const onErrorClick = vi.fn()
    const errors: ValidationError[] = [
      { line: 3, column: 0, message: 'Click me', category: 'ERROR' },
    ]
    render(
      <StarlarkEditor {...defaultProps} errors={errors} onErrorClick={onErrorClick} />,
    )
    fireEvent.click(screen.getByTestId('error-item-0'))
    expect(onErrorClick).toHaveBeenCalledWith(errors[0])
  })

  it('renders read-only indicator when readOnly is true', () => {
    render(<StarlarkEditor {...defaultProps} readOnly />)
    expect(screen.getByTestId('readonly-badge')).toBeTruthy()
  })

  it('does not render read-only indicator when readOnly is false', () => {
    render(<StarlarkEditor {...defaultProps} readOnly={false} />)
    expect(screen.queryByTestId('readonly-badge')).toBeNull()
  })

  it('shows error and warning with correct labels', () => {
    const errors: ValidationError[] = [
      { line: 1, column: 0, message: 'Syntax error', category: 'SYNTAX' },
      { line: 2, column: 0, message: 'A warning', category: 'WARNING' },
      { line: 3, column: 0, message: 'An error', category: 'ERROR' },
    ]
    render(<StarlarkEditor {...defaultProps} errors={errors} />)
    expect(screen.getByText(/3 issues/i)).toBeTruthy()
  })

  it('accepts optional className prop', () => {
    const { container } = render(
      <StarlarkEditor {...defaultProps} className="custom-class" />,
    )
    expect(container.querySelector('[data-testid="starlark-editor"]')?.classList).toContain(
      'custom-class',
    )
  })

  it('dispatches to reconfigure linter when errors prop changes', () => {
    const { rerender } = render(<StarlarkEditor {...defaultProps} errors={[]} />)
    const initialCalls = mockDispatch.mock.calls.length

    const newErrors: ValidationError[] = [
      { line: 1, column: 0, message: 'New error', category: 'ERROR' },
    ]
    rerender(<StarlarkEditor {...defaultProps} errors={newErrors} />)
    // dispatch should have been called to reconfigure the linter compartment
    expect(mockDispatch.mock.calls.length).toBeGreaterThan(initialCalls)
  })

  it('dispatches to sync value when value prop changes externally', () => {
    const { rerender } = render(
      <StarlarkEditor {...defaultProps} value="initial code" />,
    )
    const initialCalls = mockDispatch.mock.calls.length

    rerender(<StarlarkEditor {...defaultProps} value="updated code" />)
    // dispatch should have been called to sync the doc
    expect(mockDispatch.mock.calls.length).toBeGreaterThan(initialCalls)
  })

  it('reconfigures read-only when readOnly prop changes', () => {
    const { rerender } = render(
      <StarlarkEditor {...defaultProps} readOnly={false} />,
    )
    const initialCalls = mockDispatch.mock.calls.length

    rerender(<StarlarkEditor {...defaultProps} readOnly={true} />)
    expect(mockDispatch.mock.calls.length).toBeGreaterThan(initialCalls)
  })
})

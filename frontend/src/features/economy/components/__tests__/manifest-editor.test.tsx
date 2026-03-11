import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ManifestEditor } from '../manifest-editor'

// Mock CodeMirror since jsdom doesn't support the canvas/DOM APIs it needs
vi.mock('@uiw/react-codemirror', () => {
  const React = require('react')
  return {
    __esModule: true,
    default: React.forwardRef(function MockCodeMirror(
      props: {
        value?: string
        onChange?: (value: string) => void
        'data-testid'?: string
      },
      _ref: React.Ref<unknown>,
    ) {
      return (
        <textarea
          data-testid={props['data-testid'] ?? 'codemirror-editor'}
          value={props.value}
          onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
            props.onChange?.(e.target.value)
          }
        />
      )
    }),
  }
})

vi.mock('@codemirror/lang-yaml', () => ({
  yaml: () => [],
}))

vi.mock('@codemirror/lang-python', () => ({
  python: () => [],
}))

vi.mock('@codemirror/lint', () => ({
  linter: () => [],
  lintGutter: () => [],
}))

describe('ManifestEditor', () => {
  const defaultProps = {
    value: 'instruments:\n  - code: GBP\n',
    onChange: vi.fn(),
    validationErrors: [] as Array<{
      severity: string
      path: string
      code: string
      message: string
      suggestion: string
    }>,
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the CodeMirror editor with the provided value', () => {
    render(<ManifestEditor {...defaultProps} />)
    const editor = screen.getByTestId('codemirror-editor')
    expect(editor).toBeInTheDocument()
    expect(editor).toHaveValue('instruments:\n  - code: GBP\n')
  })

  it('calls onChange when editor content changes', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ManifestEditor {...defaultProps} onChange={onChange} />)
    const editor = screen.getByTestId('codemirror-editor')
    await user.clear(editor)
    await user.type(editor, 'new content')
    expect(onChange).toHaveBeenCalled()
  })

  it('renders with empty value', () => {
    render(<ManifestEditor {...defaultProps} value="" />)
    const editor = screen.getByTestId('codemirror-editor')
    expect(editor).toHaveValue('')
  })
})

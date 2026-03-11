import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ValidationPanel } from '../validation-panel'

function makeError(overrides: {
  path?: string
  message?: string
  code?: string
  severity?: string
  suggestion?: string
}) {
  return {
    $typeName: 'meridian.control_plane.v1.ValidationError' as const,
    severity: 'ERROR',
    path: '',
    code: '',
    message: '',
    suggestion: '',
    ...overrides,
  }
}

describe('ValidationPanel', () => {
  it('returns null when there are no errors or warnings', () => {
    const { container } = render(
      <ValidationPanel
        errors={[]}
        warnings={[]}
        onLineClick={vi.fn()}
        onSuggestionApply={vi.fn()}
      />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('displays errors with alert-circle icon', () => {
    render(
      <ValidationPanel
        errors={[makeError({ path: 'instruments[0].code', message: 'Code is required' })]}
        warnings={[]}
        onLineClick={vi.fn()}
        onSuggestionApply={vi.fn()}
      />,
    )
    expect(screen.getByText('Code is required')).toBeInTheDocument()
    expect(screen.getByText('instruments[0].code')).toBeInTheDocument()
  })

  it('displays warnings with alert-triangle icon', () => {
    render(
      <ValidationPanel
        errors={[]}
        warnings={[makeError({ severity: 'WARNING', path: 'sagas[0]', message: 'Unused saga' })]}
        onLineClick={vi.fn()}
        onSuggestionApply={vi.fn()}
      />,
    )
    expect(screen.getByText('Unused saga')).toBeInTheDocument()
  })

  it('sorts errors before warnings', () => {
    render(
      <ValidationPanel
        errors={[makeError({ path: 'b', message: 'Error B' })]}
        warnings={[makeError({ severity: 'WARNING', path: 'a', message: 'Warning A' })]}
        onLineClick={vi.fn()}
        onSuggestionApply={vi.fn()}
      />,
    )
    const items = screen.getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('Error B')
    expect(items[1]).toHaveTextContent('Warning A')
  })

  it('sorts items alphabetically by path within same severity', () => {
    render(
      <ValidationPanel
        errors={[
          makeError({ path: 'instruments[1].code', message: 'Second' }),
          makeError({ path: 'accounts[0].type', message: 'First' }),
        ]}
        warnings={[]}
        onLineClick={vi.fn()}
        onSuggestionApply={vi.fn()}
      />,
    )
    const items = screen.getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('First')
    expect(items[1]).toHaveTextContent('Second')
  })

  it('calls onLineClick when path is clicked', async () => {
    const user = userEvent.setup()
    const onLineClick = vi.fn()
    render(
      <ValidationPanel
        errors={[makeError({ path: 'instruments[0].code', message: 'Bad code' })]}
        warnings={[]}
        onLineClick={onLineClick}
        onSuggestionApply={vi.fn()}
      />,
    )
    await user.click(screen.getByText('instruments[0].code'))
    expect(onLineClick).toHaveBeenCalledWith('instruments[0].code')
  })

  it('shows suggestion with apply button when present', async () => {
    const user = userEvent.setup()
    const onSuggestionApply = vi.fn()
    render(
      <ValidationPanel
        errors={[
          makeError({
            path: 'instruments[0].code',
            message: 'Unknown instrument',
            suggestion: 'GBP',
          }),
        ]}
        warnings={[]}
        onLineClick={vi.fn()}
        onSuggestionApply={onSuggestionApply}
      />,
    )
    expect(screen.getByText(/GBP/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /apply/i }))
    expect(onSuggestionApply).toHaveBeenCalledWith('instruments[0].code', 'GBP')
  })

  it('does not show suggestion section when suggestion is empty', () => {
    render(
      <ValidationPanel
        errors={[makeError({ path: 'instruments[0].code', message: 'Bad code', suggestion: '' })]}
        warnings={[]}
        onLineClick={vi.fn()}
        onSuggestionApply={vi.fn()}
      />,
    )
    expect(screen.queryByRole('button', { name: /apply/i })).not.toBeInTheDocument()
  })
})

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { KeyValueEditor } from './key-value-editor'

function renderEditor(
  value: Record<string, string> = {},
  onChange = vi.fn(),
) {
  return render(<KeyValueEditor value={value} onChange={onChange} />)
}

describe('KeyValueEditor', () => {
  it('renders add attribute button', () => {
    renderEditor()
    expect(screen.getByRole('button', { name: /add attribute/i })).toBeInTheDocument()
  })

  it('adds a new row when Add Attribute clicked', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    expect(screen.getByLabelText(/attribute key 1/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/attribute value 1/i)).toBeInTheDocument()
  })

  it('removes a row when Remove clicked', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    await user.click(screen.getByRole('button', { name: /remove attribute 1/i }))
    expect(screen.queryByLabelText(/attribute key 1/i)).not.toBeInTheDocument()
  })

  it('calls onChange with key-value record when key typed', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor({}, onChange)
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    await user.type(screen.getByLabelText(/attribute key 1/i), 'region')
    // Last call should include { region: '' }
    const lastCall = onChange.mock.calls[onChange.mock.calls.length - 1][0] as Record<string, string>
    expect(lastCall).toMatchObject({ region: '' })
  })

  it('calls onChange with key-value record when value typed', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor({}, onChange)
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    await user.type(screen.getByLabelText(/attribute key 1/i), 'k')
    await user.type(screen.getByLabelText(/attribute value 1/i), 'v')
    const lastCall = onChange.mock.calls[onChange.mock.calls.length - 1][0] as Record<string, string>
    expect(lastCall).toMatchObject({ k: 'v' })
  })

  it('shows duplicate key warning for repeated keys', async () => {
    const user = userEvent.setup()
    renderEditor()
    // Add two rows and give them the same key
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    await user.type(screen.getByLabelText(/attribute key 1/i), 'dup')
    await user.type(screen.getByLabelText(/attribute key 2/i), 'dup')
    expect(screen.getAllByText(/duplicate key/i).length).toBeGreaterThan(0)
  })

  it('renders pre-populated key-value pairs from value prop', () => {
    renderEditor({ name: 'Europe', code: 'EU' })
    expect(screen.getByDisplayValue('name')).toBeInTheDocument()
    expect(screen.getByDisplayValue('Europe')).toBeInTheDocument()
    expect(screen.getByDisplayValue('code')).toBeInTheDocument()
    expect(screen.getByDisplayValue('EU')).toBeInTheDocument()
  })

  it('excludes rows with empty keys from onChange output', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor({}, onChange)
    await user.click(screen.getByRole('button', { name: /add attribute/i }))
    // Don't type a key - value only
    await user.type(screen.getByLabelText(/attribute value 1/i), 'orphan')
    const lastCall = onChange.mock.calls[onChange.mock.calls.length - 1][0] as Record<string, string>
    expect(Object.keys(lastCall)).toHaveLength(0)
  })
})

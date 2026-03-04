import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ThemePreviewPanel } from './theme-preview-panel'

describe('ThemePreviewPanel', () => {
  beforeEach(() => {
    // Reset any inline styles applied by the panel
    document.documentElement.style.cssText = ''
    document.body.style.fontFamily = ''
  })

  it('renders the toggle button', () => {
    render(<ThemePreviewPanel />)
    expect(screen.getByRole('button', { name: /open theme panel/i })).toBeInTheDocument()
  })

  it('opens the panel when toggle button is clicked', async () => {
    const user = userEvent.setup()
    render(<ThemePreviewPanel />)

    const toggle = screen.getByRole('button', { name: /open theme panel/i })
    await user.click(toggle)

    expect(screen.getByText('Theme Preview')).toBeVisible()
    expect(screen.getByText('Colors')).toBeVisible()
    expect(screen.getByText('Font Family')).toBeVisible()
  })

  it('closes the panel with the X button', async () => {
    const user = userEvent.setup()
    render(<ThemePreviewPanel />)

    // Open first
    await user.click(screen.getByRole('button', { name: /open theme panel/i }))
    expect(screen.getByText('Theme Preview')).toBeVisible()

    // Close with X
    await user.click(screen.getByRole('button', { name: /close/i }))
    expect(screen.getByRole('button', { name: /open theme panel/i })).toBeInTheDocument()
  })

  it('renders color labels', async () => {
    const user = userEvent.setup()
    render(<ThemePreviewPanel />)

    await user.click(screen.getByRole('button', { name: /open theme panel/i }))

    expect(screen.getByText('Primary')).toBeVisible()
    expect(screen.getByText('Background')).toBeVisible()
    expect(screen.getByText('Foreground')).toBeVisible()
    expect(screen.getByText('Border')).toBeVisible()
  })

  it('renders font family selector', async () => {
    const user = userEvent.setup()
    render(<ThemePreviewPanel />)

    await user.click(screen.getByRole('button', { name: /open theme panel/i }))

    const fontSelect = screen.getByRole('combobox', { name: /font family/i })
    expect(fontSelect).toBeInTheDocument()
  })

  it('shows reset button and temporary notice when overrides exist', async () => {
    const user = userEvent.setup()
    render(<ThemePreviewPanel />)

    await user.click(screen.getByRole('button', { name: /open theme panel/i }))

    // Change font to trigger override state
    const fontSelect = screen.getByRole('combobox', { name: /font family/i })
    await user.selectOptions(fontSelect, 'Inter, sans-serif')

    expect(screen.getByTitle('Reset to defaults')).toBeInTheDocument()
    expect(screen.getByText(/overrides are temporary/i)).toBeVisible()
  })

  it('applies CSS variable override when color input changes', async () => {
    const user = userEvent.setup()
    render(<ThemePreviewPanel />)

    await user.click(screen.getByRole('button', { name: /open theme panel/i }))

    // Get all text inputs with the oklch placeholder — first one is for --primary
    const inputs = screen.getAllByPlaceholderText('oklch(...)')
    const primaryInput = inputs[0]
    await user.clear(primaryInput)
    await user.type(primaryInput, '#ff0000')

    expect(document.documentElement.style.getPropertyValue('--primary')).toBe('#ff0000')
  })

  it('resets all overrides when reset button is clicked', async () => {
    const user = userEvent.setup()
    render(<ThemePreviewPanel />)

    await user.click(screen.getByRole('button', { name: /open theme panel/i }))

    // Apply a font override
    const fontSelect = screen.getByRole('combobox', { name: /font family/i })
    await user.selectOptions(fontSelect, 'Inter, sans-serif')
    expect(screen.getByTitle('Reset to defaults')).toBeInTheDocument()

    // Reset
    await user.click(screen.getByTitle('Reset to defaults'))

    expect(document.body.style.fontFamily).toBe('')
    expect(screen.queryByTitle('Reset to defaults')).not.toBeInTheDocument()
  })
})

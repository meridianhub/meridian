import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PreviewSourceTabs } from './preview-source-tabs'

describe('PreviewSourceTabs', () => {
  it('renders preview tab by default', () => {
    render(
      <PreviewSourceTabs
        preview={<div data-testid="preview-content">Hello</div>}
        source="const x = 1"
      />,
    )
    expect(screen.getByTestId('preview-content')).toBeInTheDocument()
  })

  it('renders both tab triggers', () => {
    render(
      <PreviewSourceTabs
        preview={<div>Preview</div>}
        source="code"
      />,
    )
    expect(screen.getByRole('tab', { name: 'Preview' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Source' })).toBeInTheDocument()
  })

  it('uses custom sourceLabel', () => {
    render(
      <PreviewSourceTabs
        preview={<div>Preview</div>}
        source="code"
        sourceLabel="Mermaid"
      />,
    )
    expect(screen.getByRole('tab', { name: 'Mermaid' })).toBeInTheDocument()
  })

  it('switches to source tab and shows code', async () => {
    const user = userEvent.setup()
    render(
      <PreviewSourceTabs
        preview={<div>Preview Content</div>}
        source="const hello = 'world'"
      />,
    )

    await user.click(screen.getByRole('tab', { name: 'Source' }))
    expect(screen.getByText("const hello = 'world'")).toBeInTheDocument()
  })

  it('shows copy button on source tab', async () => {
    const user = userEvent.setup()
    render(
      <PreviewSourceTabs
        preview={<div>Preview</div>}
        source="copy me"
      />,
    )

    await user.click(screen.getByRole('tab', { name: 'Source' }))
    expect(screen.getByText('Copy')).toBeInTheDocument()
  })

  it('shows Copied state after clicking copy button', async () => {
    // Set up clipboard mock before render
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      writable: true,
      configurable: true,
    })

    const user = userEvent.setup()
    render(
      <PreviewSourceTabs
        preview={<div>Preview</div>}
        source="data"
      />,
    )

    await user.click(screen.getByRole('tab', { name: 'Source' }))
    expect(await screen.findByText('data')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Copy'))

    expect(await screen.findByText('Copied')).toBeInTheDocument()
  })
})

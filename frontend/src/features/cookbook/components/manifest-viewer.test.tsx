import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ManifestViewer } from './manifest-viewer'

vi.mock('codemirror', () => ({ basicSetup: [] }))
vi.mock('@codemirror/view', () => ({
  EditorView: class MockEditorView {
    static editable = { of: vi.fn(() => ({})) }
    dom: HTMLElement
    state: { doc: { toString: () => string } }
    dispatch = vi.fn()
    constructor(config: { state?: unknown; parent?: HTMLElement }) {
      this.dom = document.createElement('div')
      this.dom.className = 'cm-editor'
      this.dom.textContent = 'yaml content'
      this.state = { doc: { toString: () => 'yaml content' } }
      if (config.parent) config.parent.appendChild(this.dom)
    }
    destroy() {}
  },
}))
vi.mock('@codemirror/state', () => ({
  EditorState: { create: vi.fn(() => ({})), readOnly: { of: vi.fn(() => ({})) } },
}))

describe('ManifestViewer', () => {
  it('renders the viewer container', () => {
    render(<ManifestViewer content="name: test" />)
    expect(screen.getByTestId('manifest-viewer')).toBeInTheDocument()
  })

  it('renders the copy button', () => {
    render(<ManifestViewer content="name: test" />)
    expect(screen.getByRole('button', { name: /copy manifest/i })).toBeInTheDocument()
  })

  it('copies content to clipboard on button click', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })

    render(<ManifestViewer content="name: test" />)
    fireEvent.click(screen.getByRole('button', { name: /copy manifest/i }))

    expect(writeText).toHaveBeenCalledWith('name: test')
  })
})

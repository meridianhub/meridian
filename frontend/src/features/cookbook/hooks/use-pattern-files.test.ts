import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { usePatternFiles } from './use-pattern-files'
import type { CookbookItem } from './use-cookbook'

function makeItem(overrides: Partial<CookbookItem> = {}): CookbookItem {
  return {
    name: 'test-pattern',
    type: 'registry:pattern',
    title: 'Test Pattern',
    files: [
      { path: 'patterns/test/saga.star', content: 'def execute():\n  pass' },
      { path: 'patterns/test/manifest.yaml', content: 'name: test\ntype: registry:pattern' },
    ],
    ...overrides,
  }
}

describe('usePatternFiles', () => {
  it('returns empty state when no item provided', () => {
    const { result } = renderHook(() => usePatternFiles(undefined))
    expect(result.current.starlarkFiles).toEqual([])
    expect(result.current.manifestContent).toBeNull()
    expect(result.current.isLoading).toBe(false)
  })

  it('returns empty state for UI component items', () => {
    const item = makeItem({ type: 'registry:ui' })
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.starlarkFiles).toEqual([])
    expect(result.current.manifestContent).toBeNull()
  })

  it('extracts starlark and manifest content from bundled files', () => {
    const item = makeItem()
    const { result } = renderHook(() => usePatternFiles(item))

    expect(result.current.starlarkFiles).toEqual([
      { name: 'saga.star', content: 'def execute():\n  pass' },
    ])
    expect(result.current.manifestContent).toBe('name: test\ntype: registry:pattern')
    expect(result.current.isLoading).toBe(false)
  })

  it('handles multiple starlark files', () => {
    const item = makeItem({
      files: [
        { path: 'patterns/test/billing.star', content: 'def billing(): pass' },
        { path: 'patterns/test/onboarding.star', content: 'def onboard(): pass' },
        { path: 'patterns/test/manifest.yaml', content: 'name: test' },
      ],
    })
    const { result } = renderHook(() => usePatternFiles(item))

    expect(result.current.starlarkFiles).toHaveLength(2)
    expect(result.current.starlarkFiles[0].name).toBe('billing.star')
    expect(result.current.starlarkFiles[1].name).toBe('onboarding.star')
  })

  it('returns null manifest when no yaml file present', () => {
    const item = makeItem({
      files: [{ path: 'patterns/test/saga.star', content: 'def execute(): pass' }],
    })
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.manifestContent).toBeNull()
  })

  it('rejects HTML content as invalid (SPA fallback protection)', () => {
    const item = makeItem({
      files: [
        { path: 'patterns/test/saga.star', content: '<!DOCTYPE html><html></html>' },
        { path: 'patterns/test/manifest.yaml', content: '<html><body>not yaml</body></html>' },
      ],
    })
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.starlarkFiles).toEqual([])
    expect(result.current.manifestContent).toBeNull()
  })

  it('handles files with no content', () => {
    const item = makeItem({
      files: [
        { path: 'patterns/test/saga.star' },
        { path: 'patterns/test/manifest.yaml' },
      ],
    })
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.starlarkFiles).toEqual([])
    expect(result.current.manifestContent).toBeNull()
  })

  it('sets hasSagas true when starlark files are present', () => {
    const item = makeItem()
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.hasSagas).toBe(true)
  })

  it('sets hasSagas true when manifest YAML contains sagas array', () => {
    const item = makeItem({
      files: [
        { path: 'patterns/test/manifest.yaml', content: 'name: test\nsagas:\n  - name: deposit\n    file: deposit.star' },
      ],
    })
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.hasSagas).toBe(true)
  })

  it('sets hasSagas false when no starlark files and no sagas in manifest', () => {
    const item = makeItem({
      files: [
        { path: 'patterns/test/manifest.yaml', content: 'name: test\ntype: registry:pattern' },
      ],
    })
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.hasSagas).toBe(false)
  })

  it('sets hasSagas false when manifest has empty sagas array', () => {
    const item = makeItem({
      files: [
        { path: 'patterns/test/manifest.yaml', content: 'name: test\nsagas: []' },
      ],
    })
    const { result } = renderHook(() => usePatternFiles(item))
    expect(result.current.hasSagas).toBe(false)
  })

  it('sets hasSagas false for undefined item', () => {
    const { result } = renderHook(() => usePatternFiles(undefined))
    expect(result.current.hasSagas).toBe(false)
  })
})

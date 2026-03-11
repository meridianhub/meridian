import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { act } from '@testing-library/react'

// Must reset store between tests
beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  localStorage.clear()
})

// Import after clearing localStorage
async function getDraftStore() {
  // Reset zustand persist state between tests by reimporting
  const { useDraftStore } = await import('./draft-manager')
  return useDraftStore
}

describe('draft-manager: DraftChange types', () => {
  it('exports useDraftStore', async () => {
    const { useDraftStore } = await import('./draft-manager')
    expect(useDraftStore).toBeDefined()
  })

  it('exports mergeDraftChanges', async () => {
    const { mergeDraftChanges } = await import('./draft-manager')
    expect(mergeDraftChanges).toBeDefined()
  })
})

describe('draft-manager: store operations', () => {
  beforeEach(() => {
    // Reset module between tests to clear in-memory store
    vi.resetModules()
  })

  it('starts with empty changes and null baseVersion', async () => {
    const useDraftStore = await getDraftStore()
    const state = useDraftStore.getState()
    expect(state.changes).toEqual([])
    expect(state.baseVersion).toBeNull()
  })

  it('addChange generates id and createdAt', async () => {
    const useDraftStore = await getDraftStore()
    act(() => {
      useDraftStore.getState().addChange({
        type: 'add_saga',
        description: 'Add payment saga',
        patch: { sagas: [{ name: 'pay' }] },
      })
    })
    const { changes } = useDraftStore.getState()
    expect(changes).toHaveLength(1)
    expect(changes[0].id).toBeDefined()
    expect(typeof changes[0].id).toBe('string')
    expect(changes[0].createdAt).toBeDefined()
    expect(typeof changes[0].createdAt).toBe('number')
    expect(changes[0].type).toBe('add_saga')
    expect(changes[0].description).toBe('Add payment saga')
  })

  it('removeChange removes by id', async () => {
    const useDraftStore = await getDraftStore()
    act(() => {
      useDraftStore.getState().addChange({
        type: 'override_default',
        description: 'Override default',
        patch: {},
      })
    })
    const { changes } = useDraftStore.getState()
    const id = changes[0].id
    act(() => {
      useDraftStore.getState().removeChange(id)
    })
    expect(useDraftStore.getState().changes).toHaveLength(0)
  })

  it('clearAll removes all changes', async () => {
    const useDraftStore = await getDraftStore()
    act(() => {
      useDraftStore.getState().addChange({ type: 'add_saga', description: 'A', patch: {} })
      useDraftStore.getState().addChange({ type: 'add_instrument', description: 'B', patch: {} })
    })
    expect(useDraftStore.getState().changes).toHaveLength(2)
    act(() => {
      useDraftStore.getState().clearAll()
    })
    expect(useDraftStore.getState().changes).toHaveLength(0)
  })

  it('setBaseVersion stores the version', async () => {
    const useDraftStore = await getDraftStore()
    act(() => {
      useDraftStore.getState().setBaseVersion('2.5')
    })
    expect(useDraftStore.getState().baseVersion).toBe('2.5')
  })
})

describe('mergeDraftChanges', () => {
  it('returns base manifest when no changes', async () => {
    const { mergeDraftChanges } = await import('./draft-manager')
    const base = { version: '1.0', metadata: { name: 'Test' }, instruments: [] }
    const result = mergeDraftChanges(base, [])
    expect(result).toEqual(base)
  })

  it('merges patch fields into the base manifest', async () => {
    const { mergeDraftChanges } = await import('./draft-manager')
    const base = { version: '1.0', instruments: [], sagas: [] }
    const changes = [
      {
        id: 'c1',
        type: 'add_saga' as const,
        description: 'Add saga',
        patch: { sagas: [{ name: 'process_payment' }] },
        createdAt: Date.now(),
      },
    ]
    const result = mergeDraftChanges(base, changes)
    expect(result.sagas).toEqual([{ name: 'process_payment' }])
    // Original not mutated
    expect(base.sagas).toEqual([])
  })

  it('applies multiple patches in order (last wins)', async () => {
    const { mergeDraftChanges } = await import('./draft-manager')
    const base = { version: '1.0', metadata: { name: 'Original' } }
    const changes = [
      {
        id: 'c1',
        type: 'override_default' as const,
        description: 'First',
        patch: { metadata: { name: 'First Override' } },
        createdAt: 1000,
      },
      {
        id: 'c2',
        type: 'override_default' as const,
        description: 'Second',
        patch: { metadata: { name: 'Second Override' } },
        createdAt: 2000,
      },
    ]
    const result = mergeDraftChanges(base, changes)
    expect(result.metadata).toEqual({ name: 'Second Override' })
  })
})

// Required for top-level vi.resetModules() usage
import { vi } from 'vitest'

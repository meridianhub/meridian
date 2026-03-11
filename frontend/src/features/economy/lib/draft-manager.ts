import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export interface DraftChange {
  id: string
  type: 'add_saga' | 'override_default' | 'add_instrument' | 'modify_account_type'
  description: string
  patch: Record<string, unknown>
  createdAt: number
}

interface DraftState {
  baseVersion: string | null
  changes: DraftChange[]
  addChange: (change: Omit<DraftChange, 'id' | 'createdAt'>) => void
  removeChange: (id: string) => void
  clearAll: () => void
  setBaseVersion: (version: string) => void
}

export const useDraftStore = create<DraftState>()(
  persist(
    (set) => ({
      baseVersion: null,
      changes: [],

      addChange: (change) =>
        set((state) => ({
          changes: [
            ...state.changes,
            {
              ...change,
              id: crypto.randomUUID(),
              createdAt: Date.now(),
            },
          ],
        })),

      removeChange: (id) =>
        set((state) => ({
          changes: state.changes.filter((c) => c.id !== id),
        })),

      clearAll: () => set({ changes: [] }),

      setBaseVersion: (version) => set({ baseVersion: version }),
    }),
    {
      name: 'economy-draft',
    }
  )
)

/**
 * Applies draft changes to a base manifest, returning a merged copy.
 * Patches are applied in order; later patches override earlier ones for top-level keys.
 * The original manifest is not mutated.
 */
export function mergeDraftChanges(
  baseManifest: Record<string, unknown>,
  changes: DraftChange[]
): Record<string, unknown> {
  return changes.reduce<Record<string, unknown>>(
    (acc, change) => ({ ...acc, ...change.patch }),
    { ...baseManifest }
  )
}

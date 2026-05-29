import { useCallback, useState } from 'react'
import type { ManifestNodeType } from '../../lib/manifest-graph-model'
import { NODE_TYPE_REGISTRY } from '../../lib/node-type-registry'

const STORAGE_KEY = 'meridian:graph-visible-types'

const allNodeTypes = (): Set<ManifestNodeType> =>
  new Set<ManifestNodeType>(Object.keys(NODE_TYPE_REGISTRY) as ManifestNodeType[])

/** Read persisted visible types, falling back to "all visible" on any error. */
function loadVisibleTypes(): Set<ManifestNodeType> {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) {
      const parsed: unknown = JSON.parse(stored)
      if (Array.isArray(parsed)) {
        const known = new Set<string>(Object.keys(NODE_TYPE_REGISTRY))
        const valid = (parsed as unknown[]).filter(
          (t): t is ManifestNodeType => typeof t === 'string' && known.has(t),
        )
        // Respect an empty array as a valid "hide all" preference.
        return new Set<ManifestNodeType>(valid)
      }
    }
  } catch {
    // ignore localStorage / JSON errors and fall through to the default
  }
  return allNodeTypes()
}

export interface VisibleTypesControls {
  visibleTypes: Set<ManifestNodeType>
  toggleType: (type: ManifestNodeType) => void
  showAllTypes: () => void
  hideAllTypes: () => void
}

/** Manage the set of visible node types, persisted to localStorage. */
export function useVisibleTypes(): VisibleTypesControls {
  const [visibleTypes, setVisibleTypes] = useState<Set<ManifestNodeType>>(loadVisibleTypes)

  const persist = useCallback((types: Set<ManifestNodeType>) => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify([...types]))
    } catch {
      // ignore localStorage errors
    }
  }, [])

  const toggleType = useCallback(
    (type: ManifestNodeType) => {
      setVisibleTypes((prev) => {
        const next = new Set(prev)
        if (next.has(type)) {
          next.delete(type)
        } else {
          next.add(type)
        }
        persist(next)
        return next
      })
    },
    [persist],
  )

  const showAllTypes = useCallback(() => {
    const all = allNodeTypes()
    setVisibleTypes(all)
    persist(all)
  }, [persist])

  const hideAllTypes = useCallback(() => {
    const empty = new Set<ManifestNodeType>()
    setVisibleTypes(empty)
    persist(empty)
  }, [persist])

  return { visibleTypes, toggleType, showAllTypes, hideAllTypes }
}
